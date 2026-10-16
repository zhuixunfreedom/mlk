// rune_hunt.go RUNE350-03：符文本逐怪符文掉落摇奖（设计真源
// rune-hunt-2026-10-03.md，record_key=resource-rune-hunt-01）。
//
// 规则（均为重建策划值，非原版算法）：
//   - 单怪至多 1 枚；基础概率 = 小怪/王宠各自 chance_bp；收集符文
//     （skill_id 30）按参战单位装备乘算 F=∏(1+魔力/200)，
//     p=min(1, 基础×F)；整场掉落由开战快照冻结（sess.Rewards）。
//   - 符文池 = 30 种基础符文（SkillCatalog 1-25、27-31）；属性补正
//     （4-8）与属性守护（11-15）必须出自同元素怪物：20 种非限定
//     各权重 1、当段同元素 2 种各权重 5、其余 8 种禁止，总权重 30。
//   - 品质四档：並 5.000-14.999 / 良 15.000-22.499 / 特良 22.500-26.999 /
//     極良 27.000-29.999（边界按客户端 SkillLevelCategory <15/<22.5/<27/<30
//     严格小于对齐，保证抽中档=展示档），档内千分位均匀。
package cn35game

import (
	mathrand "math/rand"
)

// RuneSpawn 符文本一个出怪位的掉落上下文。
type RuneSpawn struct {
	Wave    int  `json:"wave"`
	Ms      int  `json:"ms"`
	Element int  `json:"element"`
	Boss    bool `json:"boss"`
}

// RuneDropSpec 开战前由资源配置合成的逐怪掉落规格（含随机种子——
// 会话快照语义要求开战时点冻结，seed 由 execute 注入）。
type RuneDropSpec struct {
	Seed         int64       `json:"seed"`
	MobChanceBP  int         `json:"mob_chance_bp"`
	BossChanceBP int         `json:"boss_chance_bp"`
	MobQ         [4]int      `json:"mob_q"`
	BossQ        [4]int      `json:"boss_q"`
	Spawns       []RuneSpawn `json:"spawns"`
}

// RuneDropResult 一次摇中的符文掉落（Spawn 为 Spawns 下标，Wave/Ms 冗余
// 供 appear_table 注入定位）。
type RuneDropResult struct {
	Spawn   int     `json:"spawn"`
	Wave    int     `json:"wave"`
	Ms      int     `json:"ms"`
	SkillID int     `json:"skill_id"`
	Level   float64 `json:"level"`
}

// runeNonElementIDs 非属性限定的 20 种基础符文（各权重 1）。
var runeNonElementIDs = []int{
	1, 2, 3, 9, 10, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 27, 28, 29, 30, 31,
}

// runeElementIDs 属性限定符文：element 1-5 → {属性补正 skill_id, 属性守护
// skill_id}（各权重 5）；其他元素的属性符文在本段禁止掉落。
func runeElementIDs(element int) (boost, guard int, ok bool) {
	if element < 1 || element > 5 {
		return 0, 0, false
	}
	return 3 + element, 10 + element, true
}

// runeQualityRanges 品质档的魔力千分位区间 [lo,hi]（闭）。
// 边界对齐客户端 SkillLevelCategory 严格小于阈值（<15/<22.5/<27/<30），
// 保证抽中档与展示档一致；故上界比设计文档标称各低 0.001。
var runeQualityRanges = [4][2]int{
	{5000, 14999}, {15000, 22499}, {22500, 26999}, {27000, 29999},
}

// RuneCollectFactor 收集符文（skill_id 30）掉落率乘算因子：
// F=∏(1+魔力/200)，按参战单位装备的符文逐件乘算。
func RuneCollectFactor(skills []PlayerSkill) float64 {
	f := 1.0
	for _, sk := range skills {
		if sk.SkillID == 30 {
			f *= 1 + sk.Level/200
		}
	}
	return f
}

// pickRuneSkillID 同元素限定加权抽取符文种类（总权重 30）。
func pickRuneSkillID(element int, rnd *mathrand.Rand) int {
	boost, guard, _ := runeElementIDs(element)
	// 权重分布：20 非限定×1 + 同元素 2 种×5 = 30。
	r := rnd.Intn(30)
	if r < 5 {
		return boost
	}
	if r < 10 {
		return guard
	}
	return runeNonElementIDs[r-10]
}

// rollRuneQuality 按四档权重抽品质档，档内千分之一均匀取魔力值。
func rollRuneQuality(weights [4]int, rnd *mathrand.Rand) float64 {
	r := rnd.Intn(10000)
	tier := 0
	acc := 0
	for i, w := range weights {
		acc += w
		if r < acc {
			tier = i
			break
		}
	}
	lo, hi := runeQualityRanges[tier][0], runeQualityRanges[tier][1]
	return float64(lo+rnd.Intn(hi-lo+1)) / 1000.0
}

// RollRuneDrops 按规格逐怪判定符文掉落；collectFactor 为收集符文乘算
// 因子（OpenBattleForParty 内按参战符文计算后传入）。确定性：
// 同 seed+factor 结果可复现（审核/台账回放用）。
func RollRuneDrops(spec *RuneDropSpec, collectFactor float64) []RuneDropResult {
	rnd := mathrand.New(mathrand.NewSource(spec.Seed))
	var out []RuneDropResult
	for i, sp := range spec.Spawns {
		chance := spec.MobChanceBP
		weights := spec.MobQ
		if sp.Boss {
			chance = spec.BossChanceBP
			weights = spec.BossQ
		}
		eff := float64(chance) * collectFactor
		if eff >= 10000 {
			eff = 10000
		}
		if eff <= 0 || float64(rnd.Intn(10000)) >= eff {
			continue
		}
		out = append(out, RuneDropResult{
			Spawn:   i,
			Wave:    sp.Wave,
			Ms:      sp.Ms,
			SkillID: pickRuneSkillID(sp.Element, rnd),
			Level:   rollRuneQuality(weights, rnd),
		})
	}
	return out
}
