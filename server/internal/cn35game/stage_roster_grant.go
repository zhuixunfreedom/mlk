package cn35game

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

const (
	StageRosterGrantVersion       = "min350-04-stage-base-v1"
	StageRosterManifestSHA256     = "d060427174c8d162f1f338430c78cea566eb033c42229631f13a17074e097b02"
	StageRosterContentSHA256      = "4d3711c416a632fd74228d9cfde63e00f270e4b768aa61a47088c00cdcd04adc"
	StageRosterUnitGrantKey       = "cn350-min350-04-stage-base-v1-unit"
	StageRosterMonsterGrantKey    = "cn350-min350-04-stage-base-v1-monster"
	StageRosterValueScope         = "confirmed catalog base values; level 1; not max-level"
	stageRosterUnitsCatalogVer    = "202009041551"
	stageRosterMonstersCatalogVer = "201906151559"
	stageRosterUnitsCatalogSHA    = "516d7debdcceddcfd39544c56ad31797e2928dff611f29d3ec3b71193c5c07e1"
	stageRosterMonstersCatalogSHA = "7f1014466e2312a5a39ef51ef5e020730cb8adb8180685e7cf84a9258dac7667"
)

var ErrStageRosterConfig = errors.New("cn35game: stage roster 配置无效")

var stageRosterUnits = []int{152, 153, 154, 155, 156, 157, 158, 159, 160, 161, 230, 280, 290, 296, 331, 336, 348, 368, 375, 396}
var stageRosterMonsters = []int{39}

// StageRosterSource 指向阶段选择使用的 03 manifest 和共享静态 catalog。
// catalog 来源是 assets/shared 共享目录，不表示 CN3.5 实机加载证据。
type StageRosterSource struct {
	ManifestSHA256        string `json:"manifest_sha256"`
	UnitCatalogVersion    string `json:"unit_catalog_version"`
	UnitCatalogSHA256     string `json:"unit_catalog_sha256"`
	MonsterCatalogVersion string `json:"monster_catalog_version"`
	MonsterCatalogSHA256  string `json:"monster_catalog_sha256"`
}

// StageRosterGrantConfig 锁定用户授权的阶段基础值礼包。
// grant_version / 两类来源键均显式固定；内容摘要只校验漂移，不生成新键。
type StageRosterGrantConfig struct {
	GrantVersion    string            `json:"grant_version"`
	InitialLevel    int               `json:"initial_level"`
	UnitIDs         []int             `json:"unit_ids"`
	MonsterIDs      []int             `json:"monster_ids"`
	Source          StageRosterSource `json:"source"`
	ContentSHA256   string            `json:"content_sha256"`
	UnitGrantKey    string            `json:"unit_grant_key"`
	MonsterGrantKey string            `json:"monster_grant_key"`
	ValueScope      string            `json:"value_scope"`
}

func (c *StageRosterGrantConfig) CompanionIDs() []int {
	return append([]int(nil), c.UnitIDs...)
}

func (c *StageRosterGrantConfig) PetIDs() []int {
	return append([]int(nil), c.MonsterIDs...)
}

// LoadStageRosterGrantConfig 拒绝未知字段、尾随数据和坏 JSON。
func LoadStageRosterGrantConfig(path string) (*StageRosterGrantConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: 读取失败: %v", ErrStageRosterConfig, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfg StageRosterGrantConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: JSON 解码失败: %v", ErrStageRosterConfig, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: JSON 有尾随内容", ErrStageRosterConfig)
	}
	return &cfg, nil
}

// StageRosterConfigSHA256 使用键排序、紧凑 UTF-8 JSON 对 grant_version、等级、
// ID 集和来源版本/哈希生成摘要。grant keys 不包含摘要，避免配置误改静默开新键。
func StageRosterConfigSHA256(cfg *StageRosterGrantConfig) (string, error) {
	source := map[string]any{
		"manifest_sha256":         cfg.Source.ManifestSHA256,
		"unit_catalog_version":    cfg.Source.UnitCatalogVersion,
		"unit_catalog_sha256":     cfg.Source.UnitCatalogSHA256,
		"monster_catalog_version": cfg.Source.MonsterCatalogVersion,
		"monster_catalog_sha256":  cfg.Source.MonsterCatalogSHA256,
	}
	payload := map[string]any{
		"grant_version": cfg.GrantVersion,
		"initial_level": cfg.InitialLevel,
		"unit_ids":      cfg.UnitIDs,
		"monster_ids":   cfg.MonsterIDs,
		"source":        source,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateStageRosterGrant 按 grant_version 分流校验：
//   - 固定版本 min350-04-stage-base-v1 走既有全量锁定（版本/键/ID 集/来源/摘要/稀有度）；
//   - 其他版本走通用校验——候选集由配置声明（按「资源已重建」逐批发放），
//     约束收敛为 catalog 存在性、台账键独立与 content_sha256 防漂移。
func ValidateStageRosterGrant(cat *cardcatalog.Catalog, cfg *StageRosterGrantConfig) error {
	if cat == nil || cfg == nil {
		return fmt.Errorf("%w: catalog/config 为空", ErrStageRosterConfig)
	}
	if cfg.GrantVersion == StageRosterGrantVersion {
		return validateStageRosterV1(cat, cfg)
	}
	return validateStageRosterGeneral(cat, cfg)
}

// validateStageRosterV1 验证固定版本候选全集、摘要、catalog 来源及稀有度。
// v1 的 ID 集与来源锁死在代码常量，任何变更都必须显式建立新的代码/配置版本。
func validateStageRosterV1(cat *cardcatalog.Catalog, cfg *StageRosterGrantConfig) error {
	if cfg.InitialLevel != 1 ||
		cfg.ContentSHA256 != StageRosterContentSHA256 ||
		cfg.UnitGrantKey != StageRosterUnitGrantKey || cfg.MonsterGrantKey != StageRosterMonsterGrantKey ||
		cfg.ValueScope != StageRosterValueScope {
		return fmt.Errorf("%w: 固定版本、初始等级、来源键或基础值范围不符", ErrStageRosterConfig)
	}
	if !sameOrderedInts(cfg.UnitIDs, stageRosterUnits) || !sameOrderedInts(cfg.MonsterIDs, stageRosterMonsters) {
		return fmt.Errorf("%w: 候选 ID 集与固定授权集不符", ErrStageRosterConfig)
	}
	if cfg.Source != (StageRosterSource{
		ManifestSHA256:        StageRosterManifestSHA256,
		UnitCatalogVersion:    stageRosterUnitsCatalogVer,
		UnitCatalogSHA256:     stageRosterUnitsCatalogSHA,
		MonsterCatalogVersion: stageRosterMonstersCatalogVer,
		MonsterCatalogSHA256:  stageRosterMonstersCatalogSHA,
	}) {
		return fmt.Errorf("%w: manifest/catalog 来源版本或 SHA 不符", ErrStageRosterConfig)
	}
	if err := checkStageRosterContentSHA(cfg); err != nil {
		return err
	}
	meta := cat.Meta()
	if meta.UnitsVersion != cfg.Source.UnitCatalogVersion || meta.UnitsSHA256 != cfg.Source.UnitCatalogSHA256 ||
		meta.MonstersVersion != cfg.Source.MonsterCatalogVersion || meta.MonstersSHA256 != cfg.Source.MonsterCatalogSHA256 {
		return fmt.Errorf("%w: 实际 catalog 与锁定来源不符", ErrStageRosterConfig)
	}
	for _, id := range cfg.UnitIDs {
		unit, err := cat.Unit(id)
		if err != nil || unit.Rare != 5 {
			return fmt.Errorf("%w: 同伴模板 %d 缺失或非五星", ErrStageRosterConfig, id)
		}
	}
	for _, id := range cfg.MonsterIDs {
		monster, err := cat.Monster(id)
		if err != nil || monster.Rare < 4 || monster.Rare > 5 {
			return fmt.Errorf("%w: 魔宠模板 %d 缺失或稀有度不在 4—5", ErrStageRosterConfig, id)
		}
	}
	return nil
}

// reservedGrantKeys 通用批次不得复用的台账键：台账键即幂等边界，
// 复用会把不同批次 ID 集冲突误报成同键版本冲突。
var reservedGrantKeys = map[string]bool{
	FiveStarGrantKey:           true,
	StageRosterUnitGrantKey:    true,
	StageRosterMonsterGrantKey: true,
}

// validateStageRosterGeneral 通用批次校验：候选集不限于 v1 固定 20+1，
// 发放边界由「资源已重建」决定而非星级，故只做 catalog 存在性检查。
func validateStageRosterGeneral(cat *cardcatalog.Catalog, cfg *StageRosterGrantConfig) error {
	if cfg.GrantVersion == "" {
		return fmt.Errorf("%w: grant_version 为空", ErrStageRosterConfig)
	}
	if cfg.InitialLevel <= 0 {
		return fmt.Errorf("%w: initial_level 必须为正", ErrStageRosterConfig)
	}
	if len(cfg.UnitIDs) == 0 && len(cfg.MonsterIDs) == 0 {
		return fmt.Errorf("%w: unit_ids/monster_ids 不能同时为空", ErrStageRosterConfig)
	}
	if len(cfg.UnitIDs) > 0 && cfg.UnitGrantKey == "" {
		return fmt.Errorf("%w: 发放同伴时 unit_grant_key 必填", ErrStageRosterConfig)
	}
	if len(cfg.MonsterIDs) > 0 && cfg.MonsterGrantKey == "" {
		return fmt.Errorf("%w: 发放魔宠时 monster_grant_key 必填", ErrStageRosterConfig)
	}
	if cfg.MonsterGrantKey != "" && cfg.MonsterGrantKey == cfg.UnitGrantKey {
		return fmt.Errorf("%w: unit/monster 台账键不得相同", ErrStageRosterConfig)
	}
	if reservedGrantKeys[cfg.UnitGrantKey] || reservedGrantKeys[cfg.MonsterGrantKey] {
		return fmt.Errorf("%w: 台账键复用预留键 %q/%q", ErrStageRosterConfig, cfg.UnitGrantKey, cfg.MonsterGrantKey)
	}
	meta := cat.Meta()
	if meta.UnitsVersion != cfg.Source.UnitCatalogVersion || meta.UnitsSHA256 != cfg.Source.UnitCatalogSHA256 ||
		meta.MonstersVersion != cfg.Source.MonsterCatalogVersion || meta.MonstersSHA256 != cfg.Source.MonsterCatalogSHA256 {
		return fmt.Errorf("%w: 实际 catalog 与配置来源不符", ErrStageRosterConfig)
	}
	if err := checkStageRosterContentSHA(cfg); err != nil {
		return err
	}
	seen := map[int]bool{}
	for _, id := range cfg.UnitIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("%w: 同伴模板 id 非法或重复 %d", ErrStageRosterConfig, id)
		}
		seen[id] = true
		if _, err := cat.Unit(id); err != nil {
			return fmt.Errorf("%w: 同伴模板 %d 不在 catalog", ErrStageRosterConfig, id)
		}
	}
	seen = map[int]bool{}
	for _, id := range cfg.MonsterIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("%w: 魔宠模板 id 非法或重复 %d", ErrStageRosterConfig, id)
		}
		seen[id] = true
		if _, err := cat.Monster(id); err != nil {
			return fmt.Errorf("%w: 魔宠模板 %d 不在 catalog", ErrStageRosterConfig, id)
		}
	}
	return nil
}

func checkStageRosterContentSHA(cfg *StageRosterGrantConfig) error {
	contentSHA, err := StageRosterConfigSHA256(cfg)
	if err != nil {
		return fmt.Errorf("%w: 摘要计算失败: %v", ErrStageRosterConfig, err)
	}
	if contentSHA != cfg.ContentSHA256 {
		return fmt.Errorf("%w: content_sha256 不符 got=%s want=%s", ErrStageRosterConfig, contentSHA, cfg.ContentSHA256)
	}
	return nil
}

func sameOrderedInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
