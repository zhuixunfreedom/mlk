// Package statmodel CN3.5 单位/魔宠数值引擎（无副作用纯函数）。
//
// 职责边界（决策笔记 notes/proposed/2026-09-23-cn35-stat-ownership.md）：
//   - 服务端负责「面板终值」与「成长区间字段」的计算与下发；
//   - 战斗内倍率/伤害/Guts/外皮/技能全在客户端，本包不参与，
//     也不接收客户端上报的数值面板（不存在该上行链路）。
//
// 逐式复刻客户端 GrowthType（artifacts/tasks/toto-cn-3.5.0-onboarding/
// run/ilspy-cs/GrowthType.cs，CN3.5 Assembly-CSharp.dll 反编译）：
//   - Lv1→未觉醒满级：幂函数曲线，指数 growth_type_id=3→1.6 / 2→1.0 / 其它→0.5；
//   - 超未觉醒满级：线性段，每级增量 = (max-min)/(base_max_level-1)（整数除法）；
//   - C# `(int)` 向零截断 → Go `int(float32)` 同为向零截断（本包输入恒非负）。
//
// 也复刻魔宠 Size 公式（MonsterPowerupModal.cs:139-146）：
//   - HP = floor(base_hp × size)，攻击 = floor(base_seed_at × size^2.36)。
//
// 也复刻潜能（技术力）预览公式（GrowthType.cs:49-90）：
//   - CalculateTechnicalHp/At：floor(technicalStatus / addition)，超未觉醒满级时
//     叠加以「觉醒前上限」CalculateBeforAwakeMaxmum 为端点的线性段；
//   - addition 由客户端按 rare_type_id 取（★2=4.6 / ★3=3.8 / ★4=2.7 / ★5=2.2）。
//
// 等级三态（C0.1 冻结，plans/cn35-stat-system-2026-09-23/README.md）：
//   - base_max_level    未觉醒上限（= GrowthType.maxLevel，成长曲线分母）；
//   - current_max_level = base_max_level + duplicated_count × 5（训练合法上界）；
//   - full_max_level    = base_max_level + 觉醒上限 × 5（满觉满级，★5=145）。
//
// 重建策略与未证边界（不得当作原服事实）：
//   - 区间端点倍率 f = 1.8 + 0.1×growth_type_id（两源一致：客户端不乘 f，
//     直接读服务端下发的 hp_max；f 只用于服务端重建 hp_max/at_max）。
//   - 觉醒上限 ★1=5、其余=15；每次觉醒 +5 级（145 项 expTable 反证 ★5 满觉 145）。
//   - 端点值改用精确整数运算 floor(base×(18+type)/10)，规避 IEEE754 下
//     float64 表示 1.9/2.1 带来的 ±1 误差；若要改回浮点口径，替换 EndpointMax 即可。
//   - 浮点曲线按 float32 复刻（客户端 Mathf.Pow 为 float32 域），保留向零截断。
package statmodel

import (
	"errors"
	"fmt"
	"math"
)

// ErrDuplicatedOutOfRange 觉醒次数越界：0 <= duplicated_count <= MaxDuplicatedCount(rare)。
// 越界不静默截断——调用方必须显式处理（拒绝或降级并标注）。
var ErrDuplicatedOutOfRange = errors.New("statmodel: 觉醒次数越界")

// ErrLevelOutOfRange 等级越界当前觉醒上限（1 <= level <= current_max_level）。
// 这是写入/入库门禁（05A/06）的判据，不是读面渲染的判据——客户端渲染时
// 自身会 Mathf.Min(_level, base_max_level)（GrowthType.cs:29,40）。
var ErrLevelOutOfRange = errors.New("statmodel: 等级越界当前觉醒上限")

// 成长类型 id（units.json 的 type 与客户端 growth_type_id 同序，已有
// type=3↔晩成 的生产证据）。客户端 switch 为 3/2/其它，故 1 与未知同归「早熟」。
const (
	GrowthEarly   = 1 // 早熟：指数 0.5（前期快、后期慢）
	GrowthAverage = 2 // 平均：指数 1.0（线性）
	GrowthLate    = 3 // 晩成：指数 1.6（前期慢、后期快）
)

// AwakenLevelStep 每次觉醒解锁的等级数（未证：社区资料 + 145 项 expTable 间接印证）。
const AwakenLevelStep = 5

// baseMaxLevelBase/perRare：未觉醒等级上限 = 20 + 10×rare。
// 生产证据仅覆盖 ★5=70（serve_quest_execute_1.json），其余星级为同式插值【推断】。
const (
	baseMaxLevelBase   = 20
	baseMaxLevelPerRar = 10
)

// NormalizeGrowthType 把任意 growth_type_id 归一到 1/2/3：
// 客户端 `_ =>` 分支等价于「非 2 非 3 即 0.5 指数」，也就是早熟。
func NormalizeGrowthType(gt int) int {
	switch gt {
	case GrowthAverage, GrowthLate:
		return gt
	default:
		return GrowthEarly
	}
}

// BaseMaxLevel 未觉醒等级上限。rare<=0 时按 ★1 处理，保证结果 >= 30
// （客户端 maxLevel==1 会除零，服务端下发必须避免 base_max_level < 2）。
func BaseMaxLevel(rare int) int {
	if rare <= 0 {
		rare = 1
	}
	return baseMaxLevelBase + baseMaxLevelPerRar*rare
}

// MaxDuplicatedCount 觉醒（堆叠）次数上限：★1=5，★2–★5=15【推断，社区资料】。
// 客户端只暴露 max_duplicated_count 与 hp_15_max/at_15_max 命名，无 5/15 判定逻辑。
func MaxDuplicatedCount(rare int) int {
	if rare <= 1 {
		return 5
	}
	return 15
}

// CurrentMaxLevel 当前实例可达到的正常等级上限 = base_max_level + duplicated_count×5。
// 这是客户端 max_level（UnitListRow.IsTrainingable / UnitLevelupModal.CanLevelup）
// 与训练写面（05A）、入库校验（06）的共同口径（C0.1/C0.2）。
//
// duplicatedCount 越界由调用方先用 ValidateInstance 判定；本函数对负值按 0 处理
// （不产生低于 base_max_level 的上限），刻意不做上限截断以免掩盖越界。
func CurrentMaxLevel(rare, duplicatedCount int) int {
	if duplicatedCount < 0 {
		duplicatedCount = 0
	}
	return BaseMaxLevel(rare) + duplicatedCount*AwakenLevelStep
}

// FullMaxLevel 满觉满级等级 = base_max_level + 觉醒上限×5（★5=145）。
// 等价于 hp_15_max/at_15_max 的取值等级。
func FullMaxLevel(rare int) int {
	return BaseMaxLevel(rare) + MaxDuplicatedCount(rare)*AwakenLevelStep
}

// ValidateInstance 单位实例态合法性（写入/入库门禁，非读面渲染判据）：
//   - 0 <= duplicatedCount <= MaxDuplicatedCount(rare)
//   - 1 <= level <= CurrentMaxLevel(rare, duplicatedCount)
//
// 越界返回包装了 ErrDuplicatedOutOfRange / ErrLevelOutOfRange 的错误，
// 调用方（05A/06）据此拒绝请求；不得静默截断后继续写入。
func ValidateInstance(rare, level, duplicatedCount int) error {
	maxDup := MaxDuplicatedCount(rare)
	if duplicatedCount < 0 || duplicatedCount > maxDup {
		return fmt.Errorf("%w: rare=%d dup=%d 不在 [0,%d]", ErrDuplicatedOutOfRange, rare, duplicatedCount, maxDup)
	}
	cur := CurrentMaxLevel(rare, duplicatedCount)
	if level < 1 || level > cur {
		return fmt.Errorf("%w: rare=%d dup=%d level=%d 不在 [1,%d]",
			ErrLevelOutOfRange, rare, duplicatedCount, level, cur)
	}
	return nil
}

// EndpointFactorTenths 区间端点倍率 f 的十倍整数表示：f = (18+type)/10
// （早熟 19/10 / 平均 20/10 / 晩成 21/10）。刻意不返回 float64——1.8+0.1
// 在 IEEE754 下是 1.9000000000000001，会污染下游 floor 结果。
func EndpointFactorTenths(gt int) int {
	return 18 + NormalizeGrowthType(gt)
}

// EndpointMax 未觉醒满级端点值 = floor(base × f)，用精确整数运算
// base×(18+type)/10 实现（非负整数除法即向下取整），避免浮点 ±1。
func EndpointMax(base, gt int) int {
	if base <= 0 {
		return 0
	}
	return base * EndpointFactorTenths(gt) / 10
}

// CalculateStatus 曲线上一点（level 已被调用方截断到 baseMaxLevel 以内）。
// 复刻 GrowthType.CalculateStatus：float32 运算 + 向零截断。
func CalculateStatus(level, max, min, gt, baseMaxLevel int) int {
	if baseMaxLevel <= 1 {
		// 客户端会除零（异常数据）；服务端降级为下线值，避免 NaN/Inf 上屏。
		return min
	}
	x := (float32(level) - 1) / float32(baseMaxLevel-1)
	var exp float64
	switch NormalizeGrowthType(gt) {
	case GrowthLate:
		exp = float64(float32(1.6))
	case GrowthAverage:
		exp = 1.0
	default:
		exp = float64(float32(0.5))
	}
	g := float32(math.Pow(float64(x), exp))
	return int(float32(min) + float32(max-min)*g)
}

// CalculateOverStatus 超未觉醒满级的线性增量，每级 (max-min)/(baseMaxLevel-1)，
// 整数除法（先除后乘）；复刻 GrowthType.CalculateOverStatus。
func CalculateOverStatus(level, max, min, baseMaxLevel int) int {
	if baseMaxLevel <= 1 {
		return 0
	}
	return (max - min) / (baseMaxLevel - 1) * (level - baseMaxLevel)
}

// CalculateValue 面板值（HP 或攻击）：level<=baseMaxLevel 走曲线；
// level>baseMaxLevel 时先按 baseMaxLevel 截断取端点，再叠加线性段。
// 复刻 GrowthType.CalculateHp/CalculateAt。
func CalculateValue(level, max, min, gt, baseMaxLevel int) int {
	if level < 1 {
		level = 1
	}
	lv := level
	if lv > baseMaxLevel {
		lv = baseMaxLevel
	}
	v := CalculateStatus(lv, max, min, gt, baseMaxLevel)
	if baseMaxLevel < level {
		v += CalculateOverStatus(level, max, min, baseMaxLevel)
	}
	return v
}

// CalculateTechnicalStatus 技术力（潜能）曲线值（不含基础值）：
//   - 晩成(3)：floor((max-min) × pow(x, 1.6))
//   - 平均(2)：floor((max-min) × x)
//   - 其它(1)：floor((max-min) × pow(x, 0.5))
//
// 逐式复刻 GrowthType.CalculateTechnicalStatus（GrowthType.cs:102-110）。
// 注意与 CalculateStatus 的差别：技术力**不减基础值 min**。
func CalculateTechnicalStatus(level, max, min, gt, baseMaxLevel int) int {
	if baseMaxLevel <= 1 {
		return 0
	}
	x := (float32(level) - 1) / float32(baseMaxLevel-1)
	var exp float64
	switch NormalizeGrowthType(gt) {
	case GrowthLate:
		exp = float64(float32(1.6))
	case GrowthAverage:
		exp = 1.0
	default:
		exp = float64(float32(0.5))
	}
	g := float32(math.Pow(float64(x), exp))
	return int(float32(max-min) * g)
}

// TechnicalAwakeFactor 觉醒前上限系数：晩成 1.1 / 平均 1.0 / 早熟 0.9
// （GrowthType.cs:73-90 CalculateBeforAwakeMaxmum 的 switch）。
func TechnicalAwakeFactor(gt int) float32 {
	switch NormalizeGrowthType(gt) {
	case GrowthLate:
		return 1.1
	case GrowthAverage:
		return 1.0
	default:
		return 0.9
	}
}

// CalculateBeforAwakeMaxmum 技术服务用的"觉醒前上限"端点：
// floor((max-min) × (1/addition) × factor + min)，float32 域。
// 逐式复刻 GrowthType.CalculateBeforAwakeMaxmum（GrowthType.cs:73-90）。
func CalculateBeforAwakeMaxmum(max, min, gt int, addition float64) int {
	f := float32(max-min)*(1/float32(addition))*TechnicalAwakeFactor(gt) + float32(min)
	return int(math.Floor(float64(f)))
}

// CalculateTechnicalValue 技术力面板值（潜能预览用），复刻
// GrowthType.CalculateTechnicalHp/At（GrowthType.cs:49-71）：
//
//	level' = min(level, baseMaxLevel)
//	num    = FloorToInt(technicalStatus(level') / addition)
//	if baseMaxLevel < level: num += OverStatus(level, beforeAwakeMax, min)
func CalculateTechnicalValue(level, max, min, gt, baseMaxLevel int, addition float64) int {
	if addition <= 0 {
		addition = 1
	}
	lv := level
	if lv > baseMaxLevel {
		lv = baseMaxLevel
	}
	if lv < 1 {
		lv = 1
	}
	status := CalculateTechnicalStatus(lv, max, min, gt, baseMaxLevel)
	// Mathf.FloorToInt((float)status / addition)：float32 除法后向下取整。
	num := int(math.Floor(float64(float32(status) / float32(addition))))
	if baseMaxLevel < level {
		m := CalculateBeforAwakeMaxmum(max, min, gt, addition)
		num += CalculateOverStatus(level, m, min, baseMaxLevel)
	}
	return num
}

// UnitInput 单位面板计算的输入：master 基础值（catalog）+ 实例态（库存）。
//
// 注意：HpMin/AtMin 即 units.json 的 life/atk（Lv1 基础值）；
// hp_max/at_max 上游未提供，由 EndpointMax 重建（见包注释）。
type UnitInput struct {
	Type            int // growth_type_id（= units.json.type）
	Rare            int // 星级（决定 base_max_level / 觉醒上限）
	HpMin           int // 体力基础值=Lv1 HP（units.json life）
	AtMin           int // 攻击基础值=Lv1 攻击（units.json atk）
	Level           int // 当前等级（>=1）
	DuplicatedCount int // 已觉醒次数
}

// UnitPanel 单位面板与成长区间字段（服务端下发面）。
// 字段名与客户端 GrowthType/Piece/UnitLevelupModal 的读取键一一对应。
type UnitPanel struct {
	HP int // hp：当前面板最大 HP（Piece.cs:716）
	At int // at：当前面板攻击（PieceParameter.cs:93）

	HpMin int // hp_min：Lv1 值（GrowthType.cs:21）
	HpMax int // hp_max：未觉醒满级值（GrowthType.cs:22）
	AtMin int // at_min（GrowthType.cs:23）
	AtMax int // at_max（GrowthType.cs:24）

	GrowthTypeID int // growth_type_id（GrowthType.cs:19）
	BaseMaxLevel int // base_max_level：未觉醒等级上限（GrowthType.cs:20）

	// CurrentMaxLevel：当前觉醒次数下的正常等级上限（base_max_level + dup×5）。
	// 由 02 下发为 max_level（UnitListRow.IsTrainingable / UnitLevelupModal.CanLevelup 消费），
	// 并由 05A/06 作为写入与入库的合法区间上界（C0.1/C0.2）。
	CurrentMaxLevel int

	MaxDuplicatedCount int // max_duplicated_count：觉醒次数上限（UnitLevelupModal.cs:107）
	HP15Max            int // hp_15_max：满觉满级 HP（0=客户端显示空白）
	At15Max            int // at_15_max：满觉满级攻击
}

// FullMaxLevel 满觉满级等级 = base_max_level + 觉醒上限×5（★5=145）。
// 保留 FullAwakenMaxLevel 作为别名以兼容既有调用。
func FullAwakenMaxLevel(rare int) int { return FullMaxLevel(rare) }

// ComputeUnitPanel 计算单位面板与成长区间（纯函数，无 DB、无副作用）。
//
// 输入越界（DuplicatedCount 不在 [0,max] 或 Level<1）返回明确错误而非静默截断。
// 注意 level > current_max_level **不**在此拒绝：读面渲染由客户端自身
// Mathf.Min(level, base_max_level) 兜底（GrowthType.cs:29,40），而写入/入库
// 门禁应改用 ValidateInstance（05A/06）。
func ComputeUnitPanel(in UnitInput) (UnitPanel, error) {
	if in.Level < 1 {
		return UnitPanel{}, fmt.Errorf("%w: level=%d", ErrLevelOutOfRange, in.Level)
	}
	maxDup := MaxDuplicatedCount(in.Rare)
	if in.DuplicatedCount < 0 || in.DuplicatedCount > maxDup {
		return UnitPanel{}, fmt.Errorf("%w: rare=%d dup=%d 不在 [0,%d]",
			ErrDuplicatedOutOfRange, in.Rare, in.DuplicatedCount, maxDup)
	}
	gt := NormalizeGrowthType(in.Type)
	baseMax := BaseMaxLevel(in.Rare)
	hpMax := EndpointMax(in.HpMin, gt)
	atMax := EndpointMax(in.AtMin, gt)
	full := baseMax + maxDup*AwakenLevelStep
	return UnitPanel{
		HP:                 CalculateValue(in.Level, hpMax, in.HpMin, gt, baseMax),
		At:                 CalculateValue(in.Level, atMax, in.AtMin, gt, baseMax),
		HpMin:              in.HpMin,
		HpMax:              hpMax,
		AtMin:              in.AtMin,
		AtMax:              atMax,
		GrowthTypeID:       gt,
		BaseMaxLevel:       baseMax,
		CurrentMaxLevel:    baseMax + in.DuplicatedCount*AwakenLevelStep,
		MaxDuplicatedCount: maxDup,
		HP15Max:            CalculateValue(full, hpMax, in.HpMin, gt, baseMax),
		At15Max:            CalculateValue(full, atMax, in.AtMin, gt, baseMax),
	}, nil
}

// ---- 魔宠 Size ----

// MonsterHP 当前 Size 下的 HP = floor(base_hp × size)（MonsterPowerupModal.cs:144）。
func MonsterHP(baseHp int, scale float64) int {
	if baseHp <= 0 {
		return 0
	}
	return int(float32(float64(baseHp) * scale))
}

// MonsterAt 当前 Size 下的攻击 = floor(base_seed_at × size^2.36)
// （MonsterPowerupModal.cs:142-143，指数 2.36 硬编码于客户端）。
func MonsterAt(baseSeedAt int, scale float64) int {
	if baseSeedAt <= 0 {
		return 0
	}
	s := float32(scale)
	p := float32(math.Pow(float64(s), float64(float32(2.36))))
	return int(float32(baseSeedAt) * p)
}

// ScaleAfterPowerup 强化 count 次后的 Size：
// real_scale + step×count，step = 新魔宠 0.01 / 非新 0.02
// （ScaleCategory.cs:203-211）。count 为「强化道具个数 / 单次消耗」，此处按次数。
func ScaleAfterPowerup(realScale float64, newMonster bool, count int) float64 {
	if count < 0 {
		count = 0
	}
	step := 0.02
	if newMonster {
		step = 0.01
	}
	return realScale + step*float64(count)
}
