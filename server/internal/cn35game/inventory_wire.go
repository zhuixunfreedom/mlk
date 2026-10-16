// inventory_wire.go PLAY350-07：账号库存（InventoryStore）+ 模板数值
// （cardcatalog）→ user_units/user_monsters 生产形态字段映射。
//
// 证据基线：handoffs/03/inventory-contract.md（3.5 消费代码）+
// assets/shared/reference/serve_quest_execute_1.json 内嵌生产记录（id=152 单位、
// id=1 魔宠模板）。要点：
//   - id/leader_unit_id 等比较字段发 JSON NUMBER（i() 对 STRING 恒 0）。
//   - rare_type_id=rare*10、rare_type_count=rare、rare_type_name="╂"×rare、
//     attack_ms=aspd*1000、asset_name="unit/unit_{id}"（生产记录实测）。
//   - country_id 必须下发 JSON NUMBER：客户端 Unit.CalcCountrySpBonusRate
//     以 i() 读 country_id 统计同国人数加成初始 SP（i() 对 STRING 恒 0）；
//     Enum(key,id) 做 id→name 时两侧经 s() 比较，数值 id 亦可命中。
//     catalog 无官方国家 id 表，CountryID 由 cardcatalog.Load 按「持有
//     该国的最小 unit id」确定性编号，同包下发 {id:数值,name:中文名} 枚举行。
//   - sex_id 仅经 cache.Enum 做 id→name：以 gender 数字串为 id 下发，
//     同包附 sex 枚举行。
//   - cost=100*rare【推断·重建策略】：生产样本 ★5 单位 152 cost=500
//     （召唤扣 cost，非原服逐位值）。
//   - bonus_sp=20*rare*(dup+5)【实机证据】：招募结果页 SP加成 栏
//     ★4 dup0=400/dup15=1600、★2 dup15=800（用户验收截图），即
//     基础 100*rare + 每觉醒 +20*rare。客户端 PowerupButton 按
//     bonus_sp×同国加成率计入初始 SP——读面与战斗同口径。
//   - max_level：仅 rare5=70 有生产证据（同 fixture）；其余星级按
//     10*rare+20 插值（未证，显示用）。
//   - uuid 为单位实例键：客户端 Quest.AddUnit/Unit.cs:267 以它作 unitMap
//     键（编队上场、死亡移除都按 uuid 寻址），缺键全员撞 "" 只能派出一位；
//     由 unit_id 确定性派生（详见 unitInstanceUUID 注释）。
//   - 战斗行为字段（act_type_id/act_target_id/hit_frame/multi_hit_count 等）
//     来自内嵌 unit_battle_behavior.json：官方样本覆盖 + 按武器重建默认，
//     祭司暂按普通攻击（用户决策 2026-09-28）；缺省单位 Idle 不出手。
//   - 缺字段省略键（不填零）：skill 等 catalog 未建模值不下发。
package cn35game

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// attrName element→中文属性名（README 枚举：1炎2水3风4光5暗）。
var attrName = map[int]string{1: "炎", 2: "水", 3: "风", 4: "光", 5: "暗"}

// genderName catalog gender→显示名（2男3女；1=怪物型/其他）。
var genderName = map[int]string{1: "不明", 2: "男", 3: "女"}

// growthTypeName catalog type→成长类型名。生产证据：152 type=3↔晩成
// （serve_quest_execute_1.json hp_growth_type_name）；1早熟/2平均为 MLK
// 三段式常识推断（未证，显示用）。
var growthTypeName = map[int]string{1: "早熟", 2: "平均", 3: "晩成"}

// weaponTypeName weapon_id→武器类型名【事实】：serve_app_conf_android.json
// 同伴筛选器 options 逐值（"请选择要筛选的武器类型"：斩击1/突击2/打击3/
// 远程4/魔法5/枪弹6/治疗7）。客户端 UserUnitDetail.cs:327 经
// cache.Enum("weapon", weapon_id) 映射显示名——同包须下发 weapon 枚举行。
var weaponTypeName = map[int]string{
	1: "斩击", 2: "突击", 3: "打击", 4: "远程", 5: "魔法", 6: "枪弹", 7: "治疗",
}

// buildUnitDoc 组装一条 user_units 行：实例态（库存）+ 模板面（catalog）。
// 模板缺失 → 调用方先查 catalog 报错；实例态越界（dup 不在 [0,max]）→ 返回
// 明确错误（不静默截断，C0.1）。
//
// 数值面（statmodel）：hp/at 是该单位「当前等级+觉醒」下的面板终值，客户端
// 直接读进战斗、不做等级换算（Piece.cs:716-717、PieceParameter.cs:93）；
// 同时下发成长区间（hp_min/hp_max/at_min/at_max/growth_type_id/base_max_level）
// 与满觉端点（hp_15_max/at_15_max，0 时客户端显示空白）供升级弹窗预览。
//
// max_level 语义（C0.2 冻结）：= current_max_level = base_max_level + dup×5。
// 证据：UnitListRow.IsTrainingable()/UnitLevelupModal.CanLevelup() 用它作为
// 正常等级训练的上界（UnitListRow.cs:340-343、UnitLevelupModal.cs:94,208-219）；
// 若取 base_max_level，dup=15 的实例在 Lv70 即被判满级，与同文档下发的
// max_duplicated_count/hp_15_max 自相矛盾。未证：原服逐位一致；替换点即此行。
//
// 战斗行为字段（act_type_id/act_target_id/hit_frame/multi_hit_count 等）
// 由 applyUnitBattleBehavior 按 unit_battle_behavior.json 下发：官方样本
// 覆盖 + 按武器重建默认，祭司暂按普通攻击（用户决策 2026-09-28）。
//
// 字段语义与证据见 docs/策划案/CN3.5数值体系.md §3/§4/§11.2。
func buildUnitDoc(rec UnitRecord, u *cardcatalog.Unit) (map[string]any, error) {
	dispName := u.Name
	if u.Title != "" {
		// 称号+名：Title 自带方括号，直接拼接。客户端 DecordReturn 不解 \[
		// 转义（Document.cs），反斜杠会原样上屏（09 实机验收发现）。
		dispName = u.Title + u.Name
	}
	panel, err := statmodel.ComputeUnitPanel(statmodel.UnitInput{
		Type:            u.Type,              // units.json.type 与 growth_type_id 同序（type=3↔晩成）
		Rare:            u.Rare,              // 决定 base_max_level 与觉醒上限
		HpMin:           u.Life,              // life ↔ hp_min（Lv1 基础值）
		AtMin:           u.Atk,               // atk ↔ at_min
		Level:           rec.Level,           // 实例态：当前等级
		DuplicatedCount: rec.DuplicatedCount, // 实例态：已觉醒次数
	})
	if err != nil {
		return nil, fmt.Errorf("cn35 单位 %d 实例态非法（level=%d dup=%d）: %w",
			rec.UnitID, rec.Level, rec.DuplicatedCount, err)
	}
	doc := map[string]any{
		"id": rec.UnitID,
		// uuid：客户端 Quest.AddUnit/Unit.cs:267 以它为 unitMap 键寻址同伴，
		// 缺失时全员键撞 ""，战斗中只能派出第一位。
		"uuid": unitInstanceUUID(rec.UnitID),
		"name": dispName,
		// prefix_name 是纯称号文字：剥掉 Title 中全部 [] 括号字符。
		// 不能 strings.Trim("[]")——它只削两端，❤/㈱/㊥ 前缀挡住左括号
		// 时会留下裸 "["（尾部 "]" 已被削），字符串内不平衡括号会击穿
		// 客户端 JSONObject 解析（gacha350 实机 Now-Loading 事故）。
		"prefix_name": strings.NewReplacer("[", "", "]", "").Replace(u.Title),
		"main_name":   u.Name,
		"level":       rec.Level,
		// max_level = 当前觉醒上限（C0.2）。避免与 base_max_level 混用。
		"max_level": panel.CurrentMaxLevel,
		"hp":        panel.HP, // 面板终值（非 Lv1 基础值）
		"at":        panel.At, // 面板终值
		// 成长区间 + 满觉端点：升级弹窗预览消费（UnitLevelupModal/AdvancedUnitLevelModal）。
		"hp_min":               panel.HpMin,
		"hp_max":               panel.HpMax,
		"at_min":               panel.AtMin,
		"at_max":               panel.AtMax,
		"growth_type_id":       panel.GrowthTypeID,
		"base_max_level":       panel.BaseMaxLevel,
		"max_duplicated_count": panel.MaxDuplicatedCount,
		"hp_15_max":            panel.HP15Max,
		"at_15_max":            panel.At15Max,
		"rare_type_id":         u.Rare * 10,
		"rare_type_count":      u.Rare,
		"rare_type_name":       strings.Repeat("╂", u.Rare),
		// SP/召唤经济字段：cost=100*rare（生产样本 ★5=500，重建策略）；
		// bonus_sp=100*rare+20*rare*dup（招募结果页实机证据，见文件头）。
		// 客户端 UnitControllerPanel 按 cost 扣召唤/士气升级 SP，
		// PowerupButton 按 bonus_sp×同国加成率计入初始 SP。
		"cost":             100 * u.Rare,
		"bonus_sp":         20 * u.Rare * (rec.DuplicatedCount + 5),
		"attr_id":          u.Element,
		"attr_name":        attrName[u.Element],
		"weapon_id":        u.Weapon,
		"aarea":            u.AArea,
		"reach":            u.AArea,
		"duplicated_count": rec.DuplicatedCount,
		// can_duplicate：觉醒书 boost 选人按钮可用判定
		// （UserUnitsListRow/UseClick→Stack select 读 document.b("can_duplicate")，
		// false 弹“好感度招募角色无法使用觉醒之书”）。
		"can_duplicate": rec.DuplicatedCount < panel.MaxDuplicatedCount,
		"protected":     rec.Protected,
		"newcomer":      rec.Newcomer,
		// 潜能字段（upgrade_level/max_upgrade_level）在 05B 取证 unit_upgrade_config
		// 前**不下发、也不发 0 伪造**（C0.2）。客户端 UnitListRow.cs:687-694 在
		// rare_type_count!=1 时渲染 "潜能指数:x/y"，缺键显示为空——该显示缺口
		// 登记为 05B 阻塞项，不冒充已实现。
		"is_evolutable":  false,
		"evolution_map":  map[string]any{},
		"monster_uuid":   rec.MonsterUUID,
		"skills":         []any{},
		"passive_skills": []any{},
		"asset_name":     fmt.Sprintf("unit/unit_%d", u.ID),
		"unit_detail_message": fmt.Sprintf(
			"name=UserUnitDetail&base=Common/Pages/UserUnitDetail&method=Stack&id=%d", u.ID),
	}
	if u.MSpd != nil {
		doc["speed"] = *u.MSpd
	}
	if u.ASPd != nil {
		doc["attack_ms"] = int(*u.ASPd * 1000)
	}
	if u.Tenacity != nil {
		doc["taghness"] = *u.Tenacity
	}
	if u.ANum != nil {
		doc["attackable_count"] = *u.ANum
	}
	if u.Hits != nil {
		doc["hits"] = *u.Hits
	}
	setRate(doc, "fire_rate", u.Rates.Fire)
	setRate(doc, "water_rate", u.Rates.Aqua)
	setRate(doc, "wind_rate", u.Rates.Wind)
	setRate(doc, "light_rate", u.Rates.Light)
	setRate(doc, "dark_rate", u.Rates.Dark)
	if u.Age != nil {
		doc["age"] = *u.Age
	}
	if u.Gender != nil {
		// sex_id 语义=展示用枚举键：直接发 catalog gender 值字符串，
		// 并同包下发 sex 枚举行（见 enumDocs）。
		doc["sex_id"] = strconv.Itoa(*u.Gender)
	}
	if n, ok := growthTypeName[u.Type]; ok {
		doc["hp_growth_type_name"] = n
	}
	if u.Country != "" {
		// country_id 发 NUMBER（同国 SP 加成按 i() 计数，STRING 恒 0）；
		// 显示名经同包 country 枚举行 {id:数值,name:中文名} 命中。
		doc["country_id"] = u.CountryID
	}
	// 介绍/档案文本：CN wiki 优先（官方译名），缺则纪念版汉化补缺
	// （unit_text 只收录 CN 缺口条目；用户决策 2026-09-28 同步翻译数据）。
	ut := memorialUnitText(u.ID)
	// 武器名（"weapon"）：生产样本 152 下发 "大剣"；CN catalog 无武器名字段，
	// 纪念版 weaponLabel 补缺（unit_text 恒收；UserUnitDetail.cs:333 消费）。
	if w, ok := ut["weapon_label"].(string); ok && w != "" {
		doc["weapon"] = w
	}
	career := u.Career
	if career == "" {
		career, _ = ut["profession"].(string)
	}
	if career != "" {
		doc["profession"] = career
	}
	interest := u.Interest
	if interest == "" {
		interest, _ = ut["favorite"].(string)
	}
	if interest != "" {
		doc["favorite"] = interest
	}
	nature := u.Nature
	if nature == "" {
		nature, _ = ut["personality"].(string)
	}
	if nature != "" {
		doc["personality"] = nature
	}
	remark := u.Remark
	if remark == "" {
		remark, _ = ut["description"].(string)
	}
	if remark != "" {
		doc["description"] = remark
	}
	if u.Age == nil {
		if a, ok := ut["age"].(string); ok && a != "" {
			doc["age"] = a
		}
	}
	if u.Gender == nil {
		if s, ok := ut["sex_id"].(string); ok && s != "" {
			doc["sex_id"] = s
		}
	}
	if u.Obtain != "" {
		doc["obtain"] = u.Obtain
	}
	// 战斗行为字段（act_type_id/act_target_id/hit_frame/multi_hit_count/
	// attack_range 与特效键）：客户端 BaseAI/TriggerRange 依其驱动出手，
	// 缺省 act_target_id="" 时 targets 恒空、单位永远 Idle。
	applyUnitBattleBehavior(doc, u)
	return doc, nil
}

// unitInstanceUUID 单位实例 uuid 派生：cn35_player_units 主键为
// (user_id, unit_id)，同账号每 unit_id 仅一行，故由 unit_id 确定性派生
// 即可保证账号内唯一且跨请求稳定。
// 【重建策略】"unit-<id>" 非原服 uuid 格式，仅保证唯一性与稳定性。
func unitInstanceUUID(unitID int) string {
	return fmt.Sprintf("unit-%d", unitID)
}

// buildMonsterDoc 组装一条 user_monsters 行。
//
// Size 面（STAT350-04，C0.3）：
//   - `scale`（展示用）与 `real_scale`（推进式计算用，客户端预览同口径）分别
//     取库存值（十进制字符串原样下发）；面板 hp/at/seed_at 用 real_scale 计算
//     （回退顺序 real_scale → scale → 1.000）。
//   - 库存无值（NULL）时为「显式 Size=1.000 降级」：仍按 1.000 计算面板，
//     缺口在 07 验收矩阵登记；不写伪值、不发伪布尔。
//   - `scale_limit`/`is_rare` 未证（NULL）时**省略键**，不填 0/false 伪造。
//   - 客户端消费：Monster.GetScaleCategory（scale/is_rare/scale_limit/real_scale）、
//     MonsterPowerupModal.InitDisplay（hp/seed_at）、UpdateLabel（base_hp/base_seed_at）。
func buildMonsterDoc(rec MonsterRecord, m *cardcatalog.Monster) (map[string]any, error) {
	// 有效 Size：展示用与推进用分别回退，最后落到显式降级值 1.000。
	// 实机证据（2026-09-30）：客户端 Document.dec/f 按数值解析，字符串态
	// 会 FormatException 并令强化按钮无响应——必须以 JSON 数值下发。
	_, displayScaleF, err := effectiveScale(rec.Scale, rec.RealScale)
	if err != nil {
		return nil, fmt.Errorf("cn35 魔宠 %s scale 非法: %w", rec.UUID, err)
	}
	_, progScale, err := effectiveScale(rec.RealScale, rec.Scale)
	if err != nil {
		return nil, fmt.Errorf("cn35 魔宠 %s real_scale 非法: %w", rec.UUID, err)
	}
	doc := map[string]any{
		"uuid":            rec.UUID,
		"id":              rec.MonsterID,
		"name":            m.Name,
		"level":           rec.Level,
		"max_level":       99, // 魔宠等级上限：无生产证据，UI 显示用（未证）
		"rare_type_id":    m.Rare * 10,
		"rare_type_count": m.Rare,
		"rare_type_name":  strings.Repeat("╂", m.Rare),
		"attr_id":         m.Element,
		"attr_name":       attrName[m.Element],
		"skin_id":         m.Skin,
		"sklsp":           m.SklSP,
		// 种子技 SP 消耗：客户端 SeedControllerPanel.onInit 读 cost 扣 SP
		// （生产样本魔宠 1 sklsp=250 ↔ cost=250 同值锚定）。
		"cost":          m.SklSP,
		"protected":     rec.Protected,
		"asset_name":    fmt.Sprintf("monster/monster_%d", m.ID),
		"seed_skill_id": 0, // 技能模板 id 不在 catalog，发 0（详情文本显示走 skill）
		// Size 展示值：库存值换算为数值下发（客户端按 dec/f 解析，非字符串），
		// 缺省为显式降级 1.000。
		"scale":      displayScaleF,
		"real_scale": progScale,
	}
	// scale_limit/is_rare 未证时省略键（不填伪值）。
	if rec.ScaleLimit != nil {
		limitF, lerr := strconv.ParseFloat(strings.TrimSpace(*rec.ScaleLimit), 64)
		if lerr != nil {
			return nil, fmt.Errorf("cn35 魔宠 %s scale_limit 非法: %w", rec.UUID, lerr)
		}
		doc["scale_limit"] = limitF
	}
	if rec.IsRare != nil {
		doc["is_rare"] = *rec.IsRare
	}
	// 介绍/出没地：CN wiki 优先（官方同源日文），缺则纪念版补缺
	// （monster_text 只收录缺口条目；用户决策 2026-09-28 同步魔宠数据）。
	mt := memorialMonsterText(m.ID)
	if m.Remark != "" {
		doc["description"] = m.Remark
	} else if d, ok := mt["description"].(string); ok && d != "" {
		doc["description"] = d
	}
	if m.Obtain != "" {
		// 上游 obtain≈出没地；stage_names 精确值未证
		doc["appear_stage_names"] = m.Obtain
	} else if o, ok := mt["obtain"].(string); ok && o != "" {
		doc["appear_stage_names"] = o
	}
	if m.Skill != "" {
		doc["skill"] = m.Skill
	}
	if rec.UnitID != nil {
		doc["unit_id"] = *rec.UnitID
	}
	// 数值补缺：wiki 有值用 wiki（CN 真源），缺/垃圾值取纪念版
	// catalog_fill（mfill）；魔宠行为字段全集见 BattleMonsterDoc。
	mfill := memorialMonsterFill(m.ID)
	mentry, mcovered := memorialMonsterEntry(m.ID)
	if m.SklCD != nil {
		doc["sklcd"] = *m.SklCD
		// 种子技冷却毫秒：生产样本 sklcd=21.9 → call_interval_ms=21899。
		// 经 float32 中转复现浮点截断——双精度 int(21.9*1000) 因 IEEE
		// 最近值舍入会得 21900；客户端 sklcd 为单精度，f32 链路截断后
		// 与生产值一致（整数秒不受影响）。
		doc["call_interval_ms"] = int(float64(float32(*m.SklCD)) * 1000)
	} else if v, ok := mfill["sklcd"]; ok {
		doc["sklcd"] = v
		doc["call_interval_ms"] = mentry["call_interval_ms"]
	}
	if m.SklSP == 0 {
		if v, ok := mfill["cost"]; ok {
			doc["cost"], doc["sklsp"] = v, v
		}
	}
	if m.SArea != nil {
		doc["sarea"] = *m.SArea
	}
	if m.MSpd != nil && *m.MSpd > 0 {
		doc["speed"] = *m.MSpd
	} else if v, ok := mfill["speed"]; ok {
		doc["speed"] = v
	}
	if m.ASPd != nil && *m.ASPd > 0 && *m.ASPd <= 10 {
		doc["attack_ms"] = int(*m.ASPd * 1000)
	} else if v, ok := mfill["attack_ms"]; ok {
		doc["attack_ms"] = v
	}
	if mcovered {
		// reach=reachValue：15/15 官方魔宠文档对拍证实；
		// wiki aarea 对怪物与 doc.reach 大面积不符，不再使用。
		doc["reach"] = mentry["reach"]
	} else if m.AArea != nil {
		doc["reach"] = *m.AArea
	}
	if m.Tenacity != nil && *m.Tenacity > 0 {
		doc["taghness"] = *m.Tenacity
	} else if v, ok := mfill["taghness"]; ok {
		doc["taghness"] = v
	}
	if m.ANum != nil && *m.ANum >= 1 {
		doc["attackable_count"] = *m.ANum
	} else if v, ok := mfill["attackable_count"]; ok {
		doc["attackable_count"] = v
	}
	// 魔宠 Size 面板：base_hp/base_seed_at 是 Size=1.000 基值，hp/at/seed_at 是
	// 当前 Size 下的值（MonsterPowerupModal.cs:120/139-146）。当前 Size 取
	// real_scale（客户端预览同口径）；库存缺值时 progScale 为显式降级 1.000。
	if m.Life != nil && *m.Life > 0 {
		doc["base_hp"] = *m.Life
		doc["hp"] = statmodel.MonsterHP(*m.Life, progScale)
	} else if v, ok := mentry["hp"].(float64); ok && v > 0 {
		// wiki 无 life 或占位 0（见怪 524）：纪念版基值补缺，同走 Size 缩放。
		base := int(v)
		doc["base_hp"] = base
		doc["hp"] = statmodel.MonsterHP(base, progScale)
	}
	if m.Atk != nil && *m.Atk > 0 {
		doc["base_seed_at"] = *m.Atk
		at := statmodel.MonsterAt(*m.Atk, progScale)
		doc["at"] = at
		doc["seed_at"] = at
	} else if v, ok := mentry["at"].(float64); ok && v > 0 {
		base := int(v)
		doc["base_seed_at"] = base
		at := statmodel.MonsterAt(base, progScale)
		doc["at"] = at
		doc["seed_at"] = at
	}
	setRate(doc, "fire_rate", m.Rates.Fire)
	setRate(doc, "water_rate", m.Rates.Aqua)
	setRate(doc, "wind_rate", m.Rates.Wind)
	setRate(doc, "light_rate", m.Rates.Light)
	setRate(doc, "dark_rate", m.Rates.Dark)
	return doc, nil
}

// MonsterSizeDegraded 库存无 Size 时读面采用的显式降级值（C0.3）。
const MonsterSizeDegraded = "1.000"

// effectiveScale 取有效 Size：primary 优先，其次 fallback，都缺则显式降级
// 1.000。返回十进制字符串原样（保形态）与精确解析值；非法数值明确报错。
func effectiveScale(primary, fallback *string) (string, float64, error) {
	for _, p := range []*string{primary, fallback} {
		if p == nil {
			continue
		}
		v := strings.TrimSpace(*p)
		if v == "" {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", 0, fmt.Errorf("非十进制字符串 %q: %w", v, err)
		}
		return v, f, nil
	}
	f, _ := strconv.ParseFloat(MonsterSizeDegraded, 64)
	return MonsterSizeDegraded, f, nil
}

func setRate(doc map[string]any, key string, v *float64) {
	if v != nil {
		doc[key] = *v
	}
}

// enumDocs 组装 Enum() 消费的旁路枚举行：country（id=cardcatalog 派生
// 的数值 CountryID、name=中文名，与 country_id 的 NUMBER 下发配对；
// 客户端 Enum 经 s() 两侧比较命中）/sex（id=gender 数字串）。
func enumDocs(units []UnitRecord, cat UnitCatalog) map[string]any {
	countryIDs := map[string]int{} // 中文名 → CountryID
	sexes := map[string]bool{}
	weapons := map[string]bool{}
	for _, r := range units {
		if u, err := cat.Unit(r.UnitID); err == nil {
			if u.Country != "" {
				countryIDs[u.Country] = u.CountryID
			}
			if u.Gender != nil {
				sexes[strconv.Itoa(*u.Gender)] = true
			}
			if _, ok := weaponTypeName[u.Weapon]; ok {
				weapons[strconv.Itoa(u.Weapon)] = true
			}
		}
	}
	type countryRow struct {
		id   int
		name string
	}
	rows := make([]countryRow, 0, len(countryIDs))
	for name, id := range countryIDs {
		rows = append(rows, countryRow{id, name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	countryList := []any{}
	for _, r := range rows {
		countryList = append(countryList, map[string]any{"id": r.id, "name": r.name})
	}
	sexList := []any{}
	for _, g := range sortedStrKeys(sexes) {
		gi, _ := strconv.Atoi(g)
		sexList = append(sexList, map[string]any{"id": g, "name": genderName[gi]})
	}
	// weapon 枚举行：UserUnitDetail 经 cache.Enum("weapon", s("weapon_id"))
	// 取武器类型名；id 与 weapon_id 同形态发字符串（Enum 两侧 s() 比较）。
	weaponList := []any{}
	for _, w := range sortedStrKeys(weapons) {
		wi, _ := strconv.Atoi(w)
		weaponList = append(weaponList, map[string]any{"id": w, "name": weaponTypeName[wi]})
	}
	return map[string]any{"country": countryList, "sex": sexList, "weapon": weaponList}
}

func sortedStrKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// numericStringToNumber preset_data 出包修正：leader_unit_id/helper_unit_id
// 等"DB 存 TEXT、契约要 NUMBER"的键统一转整（s() 兼容，i() 必须）。
func numericFieldsToNumber(user map[string]any, keys ...string) {
	for _, k := range keys {
		if s, ok := user[k].(string); ok {
			if n, err := strconv.Atoi(s); err == nil {
				user[k] = n
			}
		}
	}
}

// inventoryDataFor 读会话账号库存并组装 data 键集合
// （user_units/user_monsters + country/sex 枚举行）。账号从 req.UserID
// 来（DecodeGameRequest 已验证会话），不接受请求参数指定账号。
// 库存行引用不存在模板 → 明确数据错误（不静默跳过伪造空档）；
// Inventory/Catalog 未装配（-mem 桩）→ 空列表合法返回。
func inventoryDataFor(dep Deps, c *httpapi.Ctx, userID string) (map[string]any, error) {
	empty := map[string]any{"user_units": []any{}, "user_monsters": []any{}}
	if dep.Inventory == nil || dep.Catalog == nil {
		return empty, nil
	}
	urecs, err := dep.Inventory.Units(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 库存单位读取失败: %w", err)
	}
	mrecs, err := dep.Inventory.Monsters(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 库存魔宠读取失败: %w", err)
	}
	// RUNE350-01：装配 Skills 时按宿主分组符文库存，buildUnitDoc 出包后
	// 合入槽位字段与战斗加成（读面与 execute 同口径，Piece.cs 消费字段）。
	skillsByUnit, err := skillsByUnitFor(dep, userID)
	if err != nil {
		return nil, err
	}
	units := []any{}
	for _, r := range urecs {
		u, uerr := dep.Catalog.Unit(r.UnitID)
		if uerr != nil {
			return nil, fmt.Errorf("cn35 库存单位模板缺失 unit_id=%d: %w", r.UnitID, uerr)
		}
		udoc, derr := buildUnitDoc(r, u)
		if derr != nil {
			return nil, derr
		}
		applyUnitSkills(udoc, u.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		units = append(units, udoc)
	}
	monsters := []any{}
	for _, r := range mrecs {
		m, merr := dep.Catalog.Monster(r.MonsterID)
		if merr != nil {
			return nil, fmt.Errorf("cn35 库存魔宠模板缺失 monster_id=%d uuid=%s: %w",
				r.MonsterID, r.UUID, merr)
		}
		mdoc, derr := buildMonsterDoc(r, m)
		if derr != nil {
			return nil, derr
		}
		monsters = append(monsters, mdoc)
	}
	data := map[string]any{"user_units": units, "user_monsters": monsters}
	for k, v := range enumDocs(urecs, dep.Catalog) {
		data[k] = v
	}
	c.Decoded = map[string]any{
		"cn35": true, "uid": userID,
		"units": len(units), "monsters": len(monsters),
	}
	return data, nil
}

// userUnitsHandler GET|POST /user_units：同伴列表。响应捆绑
// user_monsters——UserUnitDetail/UserMonsterDetail 的 WaitForData 都等
// 双键，单向请求也必须把对方键写进缓存（handoffs/03 §消费链）。
func userUnitsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := inventoryDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// userMonstersHandler GET|POST /user_monsters：魔宠列表（同一组装面）。
func userMonstersHandler(dep Deps) httpapi.Handler {
	return userUnitsHandler(dep)
}
