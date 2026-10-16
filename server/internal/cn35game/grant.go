// grant.go PLAY350-08：默认测试账号五张五星礼包的领域定义。
//
// 配置真源：server/assets/cn350/five_star_grant.json（PLAY350-05 锁定）。
//   - grant_key 固定 "cn350-default-five-star-v1"（卡面指定来源键）。
//   - content_sha256 = SHA-256(json.dumps(units, sort_keys, compact,
//     ensure_ascii=False))——Go 侧以 map 重 marshal（键序自动排序、
//     紧凑、UTF-8 不转义）得到同字节流，加载即校验防配置漂移。
//   - 校验拒绝：非 5 张/重复模板/模板不存在/catalog rare!=5/
//     leader 不在五卡内/初始等级非正。
//
// 发放编排（事务/幂等/并发/队长修正）在 cn35store.GrantFiveStar。
package cn35game

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// FiveStarGrantKey 固定来源键（台账 grant_key；卡面指定）。
const FiveStarGrantKey = "cn350-default-five-star-v1"

// ErrGrantVersionConflict 同一 grant_key 已发放但模板集与当前配置不同——
// 拒绝再发/覆盖，提示版本冲突。
var ErrGrantVersionConflict = errors.New("cn35game: 发放版本冲突（同键不同模板集）")

// ErrGrantTarget 发放目标/配置非法（账号不存在、模板重复、非五星等）。
var ErrGrantTarget = errors.New("cn35game: 发放目标非法")

// FiveStarUnit 礼包中一张卡的授权要素（其余模板面由 catalog 出）。
type FiveStarUnit struct {
	ID           int    `json:"id"`
	InitialLevel int    `json:"initial_level"`
	Role         string `json:"role"` // "leader"|"member"
}

// FiveStarConfig five_star_grant.json 的校验后视图。
type FiveStarConfig struct {
	GrantVersion  string         `json:"grant_version"`
	LeaderUnitID  int            `json:"leader_unit_id"`
	Units         []FiveStarUnit `json:"units"`
	ContentSHA256 string         `json:"content_sha256"`
}

// UnitIDs 返回配置的模板 ID 集（保序）。
func (c *FiveStarConfig) UnitIDs() []int {
	out := make([]int, 0, len(c.Units))
	for _, u := range c.Units {
		out = append(out, u.ID)
	}
	return out
}

// LoadFiveStarConfig 读种子配置并做 content_sha256 完整性校验：
// 哈希口径 = units 数组 canonical JSON（sort_keys+紧凑+UTF-8）的 SHA-256。
// Go json.Marshal(map) 键序自动排序、紧凑、非 ASCII 不转义 → 同字节流。
func LoadFiveStarConfig(path string) (*FiveStarConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cn35 五卡配置读取失败: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("cn35 五卡配置非法 JSON: %w", err)
	}
	var cfg FiveStarConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("cn35 五卡配置字段非法: %w", err)
	}
	// units 按 map 重 marshal → canonical 字节流。UseNumber 保数字原文
	// （Python canonical 中 1.0 与 3680 须按原样出，Go 默认会把 float64(1.0)
	// 渲染成 "1" 导致哈希不同）。
	var units []map[string]any
	dec := json.NewDecoder(bytes.NewReader(doc["units"]))
	dec.UseNumber()
	if err := dec.Decode(&units); err != nil {
		return nil, fmt.Errorf("cn35 五卡配置 units 非法: %w", err)
	}
	canon, err := json.Marshal(units)
	if err != nil {
		return nil, fmt.Errorf("cn35 五卡配置 canonical 化失败: %w", err)
	}
	sum := sha256.Sum256(canon)
	got := hex.EncodeToString(sum[:])
	if cfg.ContentSHA256 == "" || got != cfg.ContentSHA256 {
		return nil, fmt.Errorf("%w: content_sha256 不符 got=%s want=%s",
			ErrGrantTarget, got, cfg.ContentSHA256)
	}
	if cfg.GrantVersion == "" {
		return nil, fmt.Errorf("%w: grant_version 为空", ErrGrantTarget)
	}
	return &cfg, nil
}

// ValidateFiveStar 校验配置语义（对 catalog）：恰好 5 张、互不相同、
// 模板存在、catalog rare==5（以真源为准，不信配置自报）、
// leader ∈ 五卡、initial_level>=1。任一不符 → ErrGrantTarget。
func ValidateFiveStar(cat *cardcatalog.Catalog, cfg *FiveStarConfig) error {
	if len(cfg.Units) != 5 {
		return fmt.Errorf("%w: 礼包须恰为 5 张（got %d）", ErrGrantTarget, len(cfg.Units))
	}
	seen := map[int]bool{}
	hasLeader := false
	for _, u := range cfg.Units {
		if u.ID <= 0 || u.InitialLevel <= 0 {
			return fmt.Errorf("%w: 非法卡面 id=%d level=%d", ErrGrantTarget, u.ID, u.InitialLevel)
		}
		if seen[u.ID] {
			return fmt.Errorf("%w: 模板重复 id=%d", ErrGrantTarget, u.ID)
		}
		seen[u.ID] = true
		cu, err := cat.Unit(u.ID)
		if err != nil {
			return fmt.Errorf("%w: 模板 %d 不在 catalog: %v", ErrGrantTarget, u.ID, err)
		}
		if cu.Rare != 5 {
			return fmt.Errorf("%w: 模板 %d catalog rare=%d 非五星", ErrGrantTarget, u.ID, cu.Rare)
		}
		if u.ID == cfg.LeaderUnitID {
			hasLeader = true
		}
	}
	if !hasLeader {
		return fmt.Errorf("%w: leader_unit_id=%d 不在五卡内", ErrGrantTarget, cfg.LeaderUnitID)
	}
	return nil
}

// GrantResult 一次发放的完整结果（幂等重放返回同一结果）。
type GrantResult struct {
	UserID        string `json:"user_id"`
	GrantKey      string `json:"grant_key"`
	UnitIDs       []int  `json:"unit_ids"`       // 五卡模板 ID（=实例键，堆叠模型）
	Inserted      []int  `json:"inserted"`       // 本次新插入的模板
	AlreadyOwned  []int  `json:"already_owned"`  // 发放前已合法拥有（未动其行）
	LeaderUnitID  int    `json:"leader_unit_id"` // 发放后生效队长
	LeaderChanged bool   `json:"leader_changed"` // 是否修正了悬空队长引用
	Idempotent    bool   `json:"idempotent"`     // true=台账已应用，未做新写入
}
