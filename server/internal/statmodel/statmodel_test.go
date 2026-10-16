// statmodel_test.go 表驱动数值向量测试。
//
// 向量来源：
//   - id=152（units.json：life 3680 / atk 3600 / type 3 / rare 5）；
//   - 生产样本 serve_quest_execute_1.json 的 hp_growth_type_name="晩成" 互证 type=3；
//   - 客户端 GrowthType.cs 的整数除法 / 向零截断语义；
//   - 等级三态与 max_level 语义：plans/cn35-stat-system-2026-09-23/README.md C0.1/C0.2。
package statmodel

import (
	"errors"
	"testing"
)

func mustPanel(t *testing.T, in UnitInput) UnitPanel {
	t.Helper()
	p, err := ComputeUnitPanel(in)
	if err != nil {
		t.Fatalf("ComputeUnitPanel(%+v) 意外错误: %v", in, err)
	}
	return p
}

func TestBaseMaxLevelAndAwaken(t *testing.T) {
	cases := []struct {
		rare, maxLevel, maxDup, full int
	}{
		{1, 30, 5, 55},
		{2, 40, 15, 115},
		{3, 50, 15, 125},
		{4, 60, 15, 135},
		{5, 70, 15, 145},
		{0, 30, 5, 55}, // 非法星级按 ★1 降级，避免 base_max_level<2 除零
	}
	for _, c := range cases {
		if got := BaseMaxLevel(c.rare); got != c.maxLevel {
			t.Errorf("BaseMaxLevel(%d)=%d want %d", c.rare, got, c.maxLevel)
		}
		if got := MaxDuplicatedCount(c.rare); got != c.maxDup {
			t.Errorf("MaxDuplicatedCount(%d)=%d want %d", c.rare, got, c.maxDup)
		}
		if got := FullMaxLevel(c.rare); got != c.full {
			t.Errorf("FullMaxLevel(%d)=%d want %d", c.rare, got, c.full)
		}
	}
}

// TestCurrentMaxLevel 覆盖 dup 0/5/15 三档（C0.1 三分之一的冻结向量）。
func TestCurrentMaxLevel(t *testing.T) {
	cases := []struct {
		rare, dup, cur int
	}{
		// ★5：未觉醒 70；每次觉醒 +5；满觉 145。
		{5, 0, 70}, {5, 5, 95}, {5, 10, 120}, {5, 15, 145},
		// ★1：未觉醒 30；上限 5 次 → 55。
		{1, 0, 30}, {1, 5, 55},
		// ★3：未觉醒 50；上限 15 次 → 125。
		{3, 0, 50}, {3, 15, 125},
		// 负 dup 按 0 处理（不产生低于 base_max_level 的上限）。
		{5, -3, 70},
	}
	for _, c := range cases {
		if got := CurrentMaxLevel(c.rare, c.dup); got != c.cur {
			t.Errorf("CurrentMaxLevel(%d,%d)=%d want %d", c.rare, c.dup, got, c.cur)
		}
	}
	// current_max_level(dup=max) 必须等于 full_max_level。
	for _, rare := range []int{1, 2, 3, 4, 5} {
		if got, want := CurrentMaxLevel(rare, MaxDuplicatedCount(rare)), FullMaxLevel(rare); got != want {
			t.Errorf("rare=%d: CurrentMaxLevel(dup=max)=%d != FullMaxLevel=%d", rare, got, want)
		}
	}
}

func TestEndpoint(t *testing.T) {
	// 端点倍率 (18+type)/10：早熟 19 / 平均 20 / 晩成 21（十倍整数口径）。
	for gt, want := range map[int]int{1: 19, 2: 20, 3: 21, 0: 19, 9: 19} {
		if got := EndpointFactorTenths(gt); got != want {
			t.Errorf("EndpointFactorTenths(%d)=%d want %d", gt, got, want)
		}
	}
	// floor(base × f)，精确整数口径。
	cases := []struct{ base, gt, want int }{
		{3680, 3, 7728}, {3600, 3, 7560},
		{1000, 1, 1900}, {1000, 2, 2000}, {1000, 3, 2100},
		{0, 3, 0},
	}
	for _, c := range cases {
		if got := EndpointMax(c.base, c.gt); got != c.want {
			t.Errorf("EndpointMax(%d,%d)=%d want %d", c.base, c.gt, got, c.want)
		}
	}
}

func TestComputeUnitPanel152(t *testing.T) {
	// units.json 152：摩尔沙，rare5 / type3 / life 3680 / atk 3600。
	in := UnitInput{Type: 3, Rare: 5, HpMin: 3680, AtMin: 3600}
	p1 := mustPanel(t, withLevel(in, 1, 0))

	if p1.GrowthTypeID != 3 || p1.BaseMaxLevel != 70 || p1.MaxDuplicatedCount != 15 {
		t.Fatalf("成长元信息: %+v", p1)
	}
	if p1.HpMin != 3680 || p1.AtMin != 3600 || p1.HpMax != 7728 || p1.AtMax != 7560 {
		t.Fatalf("成长区间: %+v", p1)
	}
	// Lv1 面板 = Lv1 基础值（与既有 PLAY350-07 断言一致）。
	if p1.HP != 3680 || p1.At != 3600 {
		t.Fatalf("Lv1 面板 hp=%d at=%d want 3680/3600", p1.HP, p1.At)
	}
	// C0.2：max_level 冻结为 current_max_level（dup=0 → 70）。
	if p1.CurrentMaxLevel != 70 {
		t.Fatalf("dup=0 CurrentMaxLevel=%d want 70", p1.CurrentMaxLevel)
	}
	// 未觉醒满级 = 端点值（曲线在 level==maxLevel 时恰为端点）。
	p70 := mustPanel(t, withLevel(in, 70, 0))
	if p70.HP != 7728 || p70.At != 7560 {
		t.Fatalf("Lv70 面板 hp=%d at=%d want 7728/7560", p70.HP, p70.At)
	}
	// 满觉满级 145：端点 + 线性段。整数除法 (7728-3680)/69=58、(7560-3600)/69=57。
	p145 := mustPanel(t, withLevel(in, 145, 15))
	if p145.HP != 12078 || p145.At != 11835 {
		t.Fatalf("Lv145 面板 hp=%d at=%d want 12078/11835", p145.HP, p145.At)
	}
	if p145.CurrentMaxLevel != 145 {
		t.Fatalf("dup=15 CurrentMaxLevel=%d want 145", p145.CurrentMaxLevel)
	}
	if p1.HP15Max != 12078 || p1.At15Max != 11835 {
		t.Fatalf("满觉端点 hp_15_max=%d at_15_max=%d want 12078/11835", p1.HP15Max, p1.At15Max)
	}
}

func TestCalculateOverStatusIntegerDivision(t *testing.T) {
	// (7728-3680)/(70-1) = 4048/69 = 58（截断，非 58.67）× (145-70)=75 → 4350。
	if got := CalculateOverStatus(145, 7728, 3680, 70); got != 4350 {
		t.Fatalf("CalculateOverStatus=%d want 4350", got)
	}
	// 先除后乘：差分小于 (baseMaxLevel-1) 时每级增量被截断为 0，
	// (1020-1000)/(30-1)=0 → 0×5 = 0（浮点口径会得到 3，故这是可判定差异）。
	if got := CalculateOverStatus(35, 1020, 1000, 30); got != 0 {
		t.Fatalf("CalculateOverStatus 小差分=%d want 0（整数除法）", got)
	}
}

func TestGrowthCurveShapes(t *testing.T) {
	// 平均（type2，线性）：Lv1→Max 每级等价步长。
	avg := UnitInput{Type: 2, Rare: 1, HpMin: 1000, AtMin: 1000} // baseMax 30, hpMax 2000
	if p := mustPanel(t, withLevel(avg, 1, 0)); p.HP != 1000 {
		t.Fatalf("平均 Lv1=%d want 1000", p.HP)
	}
	if p := mustPanel(t, withLevel(avg, 2, 0)); p.HP != 1034 {
		t.Fatalf("平均 Lv2=%d want 1034", p.HP)
	}
	if p := mustPanel(t, withLevel(avg, 30, 0)); p.HP != 2000 {
		t.Fatalf("平均 Lv30=%d want 2000", p.HP)
	}
	// 超满级线性：(2000-1000)/29=34 × 15 = 510 → 2510；满觉 55 → 34×25=850 → 2850。
	if p := mustPanel(t, withLevel(avg, 45, 0)); p.HP != 2510 {
		t.Fatalf("平均 Lv45=%d want 2510", p.HP)
	}
	if p := mustPanel(t, withLevel(avg, 55, 5)); p.HP != 2850 {
		t.Fatalf("平均 Lv55=%d want 2850", p.HP)
	}

	// 早熟（type1）：端点倍率 1.9。
	early := UnitInput{Type: 1, Rare: 1, HpMin: 1000, AtMin: 1000}
	if p := mustPanel(t, withLevel(early, 30, 0)); p.HpMax != 1900 || p.HP != 1900 {
		t.Fatalf("早熟 Lv30 hp_max=%d hp=%d want 1900", p.HpMax, p.HP)
	}
}

func TestPanelMonotonic(t *testing.T) {
	// 152 全等级 1..145 非递减，且端点严格大于 Lv1。
	in := UnitInput{Type: 3, Rare: 5, HpMin: 3680, AtMin: 3600}
	prevHp, prevAt := 0, 0
	for lv := 1; lv <= FullMaxLevel(in.Rare); lv++ {
		p := mustPanel(t, withLevel(in, lv, MaxDuplicatedCount(in.Rare)))
		if p.HP < prevHp || p.At < prevAt {
			t.Fatalf("Lv%d 面板回退: hp %d→%d at %d→%d", lv, prevHp, p.HP, prevAt, p.At)
		}
		prevHp, prevAt = p.HP, p.At
	}
	if prevHp <= 3680 || prevAt <= 3600 {
		t.Fatalf("满级未超过 Lv1: hp=%d at=%d", prevHp, prevAt)
	}
}

// TestComputeUnitPanelRejectsOutOfRange 验证「不静默截断」：
// dup 越界与 level<1 必须返回明确错误（C0.1 规则向量）。
func TestComputeUnitPanelRejectsOutOfRange(t *testing.T) {
	base := UnitInput{Type: 3, Rare: 5, HpMin: 3680, AtMin: 3600}

	if _, err := ComputeUnitPanel(withLevel(base, 1, 16)); !errors.Is(err, ErrDuplicatedOutOfRange) {
		t.Errorf("dup=16（★5 上限 15）应报 ErrDuplicatedOutOfRange，got %v", err)
	}
	if _, err := ComputeUnitPanel(withLevel(base, 1, -1)); !errors.Is(err, ErrDuplicatedOutOfRange) {
		t.Errorf("dup=-1 应报 ErrDuplicatedOutOfRange，got %v", err)
	}
	if _, err := ComputeUnitPanel(withLevel(base, 0, 0)); !errors.Is(err, ErrLevelOutOfRange) {
		t.Errorf("level=0 应报 ErrLevelOutOfRange，got %v", err)
	}
	// 读面允许 level > current_max_level（客户端自身 Mathf.Min 兜底），
	// 只有写面/入库门禁（ValidateInstance）拒绝。
	if _, err := ComputeUnitPanel(withLevel(base, 145, 0)); err != nil {
		t.Errorf("读面 level=145/dup=0 不应报错（渲染由客户端兜底），got %v", err)
	}
}

// TestValidateInstance 写入/入库门禁（05A/06 共用）。
func TestValidateInstance(t *testing.T) {
	ok := []struct{ rare, level, dup int }{
		{5, 1, 0}, {5, 70, 0}, {5, 95, 5}, {5, 145, 15}, {1, 30, 0}, {1, 55, 5},
	}
	for _, c := range ok {
		if err := ValidateInstance(c.rare, c.level, c.dup); err != nil {
			t.Errorf("ValidateInstance(%d,%d,%d) 应通过，got %v", c.rare, c.level, c.dup, err)
		}
	}
	bad := []struct {
		rare, level, dup int
		want             error
	}{
		// level 超当前觉醒上限：dup=0 却持有满觉等级（C0.1 明令禁止）。
		{5, 145, 0, ErrLevelOutOfRange},
		{5, 71, 0, ErrLevelOutOfRange},
		{5, 96, 5, ErrLevelOutOfRange},
		{5, 0, 0, ErrLevelOutOfRange},
		{5, 1, 16, ErrDuplicatedOutOfRange},
		{5, 1, -1, ErrDuplicatedOutOfRange},
	}
	for _, c := range bad {
		if err := ValidateInstance(c.rare, c.level, c.dup); !errors.Is(err, c.want) {
			t.Errorf("ValidateInstance(%d,%d,%d) 应报 %v，got %v", c.rare, c.level, c.dup, c.want, err)
		}
	}
}

// TestTechnicalValue 复刻 GrowthType.CalculateTechnicalHp/At（GrowthType.cs:49-90）。
// 采用「平均」成长类型与 hand-verified 整数友好向量，避免 float32 边界噪声。
func TestTechnicalValue(t *testing.T) {
	// 平均（type2），rare2 → baseMax 40，hpMin 1000，hpMax 2000。
	gt, baseMax, min, max := 2, 40, 1000, 2000
	// level=40（=baseMax）：status = int(1000×1) = 1000；
	// num = floor(1000/2.2f) = 454（2.2×454=998.8 ≤ 1000 < 2.2×455=1001）。
	if got := CalculateTechnicalValue(40, max, min, gt, baseMax, 2.2); got != 454 {
		t.Fatalf("技术力 Lv40 addition=2.2 = %d want 454", got)
	}
	// level=1：x=0 → status=0 → num=0。
	if got := CalculateTechnicalValue(1, max, min, gt, baseMax, 2.2); got != 0 {
		t.Fatalf("技术力 Lv1 = %d want 0", got)
	}
	// level=20：x=19/39=0.4871795 → status=int(487.1795)=487 → floor(487/2.2f)=221。
	if got := CalculateTechnicalValue(20, max, min, gt, baseMax, 2.2); got != 221 {
		t.Fatalf("技术力 Lv20 addition=2.2 = %d want 221", got)
	}
	// level=50（>baseMax）：m = floor(1000/2.2f + 1000) = 1454；
	// over = (1454-1000)/(40-1)×(50-40) = 11×10 = 110；num = 454+110 = 564。
	if got := CalculateTechnicalValue(50, max, min, gt, baseMax, 2.2); got != 564 {
		t.Fatalf("技术力 Lv50 addition=2.2 = %d want 564", got)
	}
	// addition<=0 归一到 1（防御，客户端不会传 0）。
	if got := CalculateTechnicalValue(40, max, min, gt, baseMax, 0); got != 1000 {
		t.Fatalf("技术力 addition=0 归一后 = %d want 1000", got)
	}
}

// TestTechnicalMonotonic 覆盖 ★5—★2 四档 addition 的差分方向（单调非负）。
func TestTechnicalMonotonic(t *testing.T) {
	// 152：rare5 / type3 / hpMin 3680 / hpMax 7728 → baseMax 70。
	gt, baseMax, min, max := 3, 70, 3680, 7728
	// getAdditonValue：rare_type_id 20→4.6(★2) / 30→3.8(★3) / 40→2.7(★4) / 50→2.2(★5)。
	for _, addition := range []float64{4.6, 3.8, 2.7, 2.2} {
		prev := -1
		for lv := 1; lv <= baseMax; lv++ {
			v := CalculateTechnicalValue(lv, max, min, gt, baseMax, addition)
			if v < prev {
				t.Fatalf("addition=%v Lv%d 技术力回退: %d→%d", addition, lv, prev, v)
			}
			if v < 0 {
				t.Fatalf("addition=%v Lv%d 技术力为负: %d", addition, lv, v)
			}
			prev = v
		}
		if prev <= 0 {
			t.Fatalf("addition=%v 满级技术力未超过 0: %d", addition, prev)
		}
	}
}

// TestTechnicalAwakeFactor 觉醒前上限系数（GrowthType.cs:76-87）。
func TestTechnicalAwakeFactor(t *testing.T) {
	for gt, want := range map[int]float32{3: 1.1, 2: 1.0, 1: 0.9, 0: 0.9, 9: 0.9} {
		if got := TechnicalAwakeFactor(gt); got != want {
			t.Errorf("TechnicalAwakeFactor(%d)=%v want %v", gt, got, want)
		}
	}
	// CalculateBeforAwakeMaxmum：floor((max-min)×(1/addition)×factor + min)。
	if got := CalculateBeforAwakeMaxmum(2000, 1000, 2, 2.2); got != 1454 {
		t.Errorf("CalculateBeforAwakeMaxmum=%d want 1454", got)
	}
}

func TestMonsterSize(t *testing.T) {
	// Size=1.000 时面板等于基值。
	if got := MonsterHP(1158, 1.0); got != 1158 {
		t.Fatalf("MonsterHP(1158,1.0)=%d want 1158", got)
	}
	if got := MonsterAt(634, 1.0); got != 634 {
		t.Fatalf("MonsterAt(634,1.0)=%d want 634", got)
	}
	// HP 线性：floor(1158×1.3)=1505。
	if got := MonsterHP(1158, 1.3); got != 1505 {
		t.Fatalf("MonsterHP(1158,1.3)=%d want 1505", got)
	}
	// 攻击 size^2.36：floor(634×2^2.36)=floor(3254.4)=3254。
	if got := MonsterAt(634, 2.0); got != 3254 {
		t.Fatalf("MonsterAt(634,2.0)=%d want 3254", got)
	}
	if got := MonsterHP(0, 2.0); got != 0 {
		t.Fatalf("MonsterHP(0)=%d want 0", got)
	}
}

func TestScaleAfterPowerup(t *testing.T) {
	cases := []struct {
		real       float64
		newMonster bool
		count      int
		want       float64
	}{
		{1.0, false, 1, 1.02},
		{1.0, true, 1, 1.01},
		{1.3, true, 0, 1.3},
		{1.0, false, -3, 1.0}, // 负 count 归零
	}
	for _, c := range cases {
		if got := ScaleAfterPowerup(c.real, c.newMonster, c.count); got != c.want {
			t.Errorf("ScaleAfterPowerup(%v,%v,%d)=%v want %v", c.real, c.newMonster, c.count, got, c.want)
		}
	}
}

func withLevel(in UnitInput, level, dup int) UnitInput {
	in.Level = level
	in.DuplicatedCount = dup
	return in
}
