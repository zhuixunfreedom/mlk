// hardness.go STAT350-03B：硬度（外皮）倍率表的分层下发。
//
// 结论性约束（C0.4 / docs 数值体系 §8.2）：
//   - 客户端一旦检测到 hardness 表存在（WeaponExtension.BonusRate 里
//     cache.Enum("hardness","4") != ""），全部外皮倍率改走服务端表，
//     缺失武器键按 1.0 处理——即「表存在」会关掉客户端已知的回退分支。
//   - 因此**默认 profile 不下发 hardness**，继续走客户端 4 类武器的粗糙回退；
//     只有拿到原服全表，或明确启用「重建测试 profile」时才下发。
//   - 测试 profile 的行值一律标注「重建值，非原服」，并保留单一替换入口
//     （只改 assets/cn350/serve_hardness_rebuild_test.json 的 rows）。
//
// 形状（客户端消费）：
//   - data.hardness = docList，每行 {id, name, bonus_rates}；
//   - id 顺序依客户端 Hardness 枚举（Hardness.cs:1-13）：
//     1 Soft / 2 Normal / 3 Hard / 4-7 SoftL2..L5 / 8-11 HardL2..L5；
//   - bonus_rates 的键依客户端 Weapon 枚举（Weapon.cs:1-10）：
//     1 Slash / 2 Rush / 3 Hit / 4 Arrow / 5 Magic / 6 Gun / 7 Heal。
package cn35game

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// HardnessProfileFile 重建测试 profile 的默认文件名（assets/cn350 下）。
const HardnessProfileFile = "serve_hardness_rebuild_test.json"

// HardnessCategoryCount / HardnessWeaponCount 表的固定维度（11 档 × 7 武器）。
// 与客户端 Hardness/Weapon 枚举成员数一致；不匹配即拒绝加载（不静默补齐）。
const (
	HardnessCategoryCount = 11
	HardnessWeaponCount   = 7
)

type hardnessRow struct {
	ID         int                `json:"id"`
	Name       string             `json:"name"`
	BonusRates map[string]float64 `json:"bonus_rates"`
}

type hardnessProfileDoc struct {
	Profile string        `json:"profile"`
	Rows    []hardnessRow `json:"rows"`
}

// LoadHardnessProfile 读取并校验重建测试 profile，返回可直接注入
// data.hardness 的行列表。校验失败明确报错（不降级、不补齐）。
func LoadHardnessProfile(path string) ([]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cn35 hardness profile 读取失败: %w", err)
	}
	var doc hardnessProfileDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("cn35 hardness profile 非法 JSON: %w", err)
	}
	if len(doc.Rows) != HardnessCategoryCount {
		return nil, fmt.Errorf("cn35 hardness profile 行数=%d，须为 %d 档",
			len(doc.Rows), HardnessCategoryCount)
	}
	out := make([]any, 0, len(doc.Rows))
	for i, r := range doc.Rows {
		want := i + 1
		if r.ID != want {
			return nil, fmt.Errorf("cn35 hardness profile 第 %d 行 id=%d，须为 %d（客户端 Hardness 顺序）",
				i, r.ID, want)
		}
		if r.Name == "" {
			return nil, fmt.Errorf("cn35 hardness profile id=%d 缺 name（客户端 Enum 判定用）", r.ID)
		}
		if len(r.BonusRates) != HardnessWeaponCount {
			return nil, fmt.Errorf("cn35 hardness profile id=%d bonus_rates 键数=%d，须为 %d",
				r.ID, len(r.BonusRates), HardnessWeaponCount)
		}
		rates := make(map[string]any, HardnessWeaponCount)
		for w := 1; w <= HardnessWeaponCount; w++ {
			k := strconv.Itoa(w)
			v, ok := r.BonusRates[k]
			if !ok {
				return nil, fmt.Errorf("cn35 hardness profile id=%d 缺 bonus_rates[%s]", r.ID, k)
			}
			rates[k] = v
		}
		out = append(out, map[string]any{
			"id":          r.ID,
			"name":        r.Name,
			"bonus_rates": rates,
		})
	}
	return out, nil
}
