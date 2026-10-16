package cn35game

import (
	mathrand "math/rand"
	"testing"
)

// TestRuneCollectFactor 收集符文乘算 F=∏(1+魔力/200)。
func TestRuneCollectFactor(t *testing.T) {
	skills := []PlayerSkill{
		{SkillID: 30, Level: 20.0},
		{SkillID: 30, Level: 30.0},
		{SkillID: 29, Level: 30.0}, // 金币符文不参与
	}
	want := (1 + 20.0/200) * (1 + 30.0/200)
	if got := RuneCollectFactor(skills); got != want {
		t.Fatalf("F=%v want %v", got, want)
	}
	if got := RuneCollectFactor(nil); got != 1.0 {
		t.Fatalf("空装 F=%v", got)
	}
}

// TestRollRuneDropsDeterministic 同 seed 结果可复现；收集乘算提升掉率。
func TestRollRuneDropsDeterministic(t *testing.T) {
	spec := &RuneDropSpec{
		Seed:         42,
		MobChanceBP:  1900,
		BossChanceBP: 7000,
		MobQ:         [4]int{2000, 7000, 980, 20},
		BossQ:        [4]int{0, 1500, 6500, 2000},
	}
	for i := 0; i < 155; i++ {
		spec.Spawns = append(spec.Spawns, RuneSpawn{
			Wave: 1, Ms: i + 1, Element: (i % 5) + 1, Boss: i%31 == 0,
		})
	}
	a := RollRuneDrops(spec, 1.0)
	b := RollRuneDrops(spec, 1.0)
	if len(a) != len(b) {
		t.Fatalf("确定性: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("确定性: %+v vs %+v", a[i], b[i])
		}
		if a[i].SkillID <= 0 || a[i].Level < 5.0 || a[i].Level > 30.0 {
			t.Fatalf("非法掉落: %+v", a[i])
		}
	}
	many := 0
	for seed := int64(0); seed < 40; seed++ {
		cp := *spec
		cp.Seed = seed
		many += len(RollRuneDrops(&cp, 4.0)) // 满配收集 F=4
	}
	low := 0
	for seed := int64(0); seed < 40; seed++ {
		cp := *spec
		cp.Seed = seed
		low += len(RollRuneDrops(&cp, 1.0))
	}
	if many <= low {
		t.Fatalf("收集乘算未提升掉率: F=4 %d vs F=1 %d", many, low)
	}
}

// TestRollRuneQualityTier 抽中档与客户端展示档一致（SkillLevelCategory
// 严格小于阈值）：各档内全部魔力值必须分类回本档。
func TestRollRuneQualityTier(t *testing.T) {
	rnd := mathrand.New(mathrand.NewSource(7))
	for tier := 0; tier < 4; tier++ {
		var weights [4]int
		weights[tier] = 10000
		for i := 0; i < 400; i++ {
			lv := rollRuneQuality(weights, rnd)
			if got := SkillLevelCategory(lv); got != tier+1 {
				t.Fatalf("档%d 魔力 %.3f 展示档=%d", tier+1, lv, got)
			}
		}
	}
}

// TestRollRuneElementPool 属性限定符文只出自同元素怪。
func TestRollRuneElementPool(t *testing.T) {
	rnd := mathrand.New(mathrand.NewSource(1))
	for i := 0; i < 500; i++ {
		id := pickRuneSkillID(2, rnd)
		if id == 4 || id == 6 || id == 7 || id == 8 ||
			id == 11 || id == 13 || id == 14 || id == 15 {
			t.Fatalf("非水属性限定符文 %d 被摇中", id)
		}
	}
}
