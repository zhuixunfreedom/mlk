package cn35game

import (
	"testing"
)

// RUNE350-02：符文行文档 id 必须下发数值 skill_id——客户端 i("id")
// 做同名判定与贴图路径（skill/skill_<id>），uuid 字符串使 i() 恒 0。
func TestSkillRowDocIDIsSkillID(t *testing.T) {
	unitID := 348
	sk := PlayerSkill{UUID: "sk-abc", SkillID: 29, Level: 5, BaseLevel: 5,
		UnitID: &unitID}
	doc := skillRowDoc(sk, map[int]string{348: "unit/unit_square_348"})
	if doc["id"] != 29 {
		t.Fatalf("id=%v want 29（数值 skill_id）", doc["id"])
	}
	if doc["uuid"] != "sk-abc" {
		t.Fatalf("uuid=%v", doc["uuid"])
	}
	if doc["icon_asset_name"] != "skill/skill_29" {
		t.Fatalf("icon_asset_name=%v", doc["icon_asset_name"])
	}
	if doc["unit_icon_asset_name"] != "unit/unit_square_348" {
		t.Fatalf("unit_icon_asset_name=%v", doc["unit_icon_asset_name"])
	}
	if doc["category"] != "普通" {
		t.Fatalf("category=%v", doc["category"])
	}
}

// RUNE350-02：unit doc 的 skills 条目须带 skill_id（UserUnitDetail
// SetIconRune 拼 questdrop_skill_<id>），rune_icon_name_N 覆盖全部槽位。
func TestApplyUnitSkillsRuneIcons(t *testing.T) {
	unitID := 348
	doc := map[string]any{}
	applyUnitSkills(doc, 3, 2, []PlayerSkill{
		{UUID: "sk-a", SkillID: 1, Level: 5, UnitID: &unitID},
		{UUID: "sk-b", SkillID: 29, Level: 5, UnitID: &unitID},
		{UUID: "sk-c", SkillID: 2, Level: 5}, // 未装备不计
	})
	if doc["skill_slot_count"] != 3 || doc["max_skill_slot_count"] != 4 {
		t.Fatalf("slot counts=%v/%v", doc["skill_slot_count"], doc["max_skill_slot_count"])
	}
	skills, _ := doc["skills"].([]any)
	if len(skills) != 2 {
		t.Fatalf("skills=%v", skills)
	}
	first := skills[0].(map[string]any)
	if first["uuid"] != "sk-a" || first["skill_id"] != 1 {
		t.Fatalf("skills[0]=%v", first)
	}
	if doc["rune_icon_name_1"] != "questdrop_skill_1" ||
		doc["rune_icon_name_2"] != "questdrop_skill_29" ||
		doc["rune_icon_name_3"] != "icon-runen_blank" {
		t.Fatalf("rune icons=%v/%v/%v", doc["rune_icon_name_1"],
			doc["rune_icon_name_2"], doc["rune_icon_name_3"])
	}
	if _, ok := doc["rune_icon_name_4"]; ok {
		t.Fatalf("超出槽位不应下发 rune_icon_name_4")
	}
}

// RUNE350-02：appear_table 金币显示面乘算金币符文因子，合计与快照一致。
func TestScaleAppearTableRewards(t *testing.T) {
	entry := func(count int) map[string]any {
		return map[string]any{"id": 1, "rewards": []any{
			map[string]any{"key": "gacha_point", "count": count},
			map[string]any{"key": "item", "id": 3, "count": 1},
		}}
	}
	table := map[string]any{
		"1": map[string]any{"10": entry(100), "20": entry(50)},
		"2": map[string]any{"10": entry(40)},
	}
	out := scaleAppearTableRewards(table, "gacha_point", 1.2, 228)
	got := 0
	for _, wv := range out {
		for _, ev := range wv.(map[string]any) {
			for _, rv := range ev.(map[string]any)["rewards"].([]any) {
				rm := rv.(map[string]any)
				if rm["key"] == "gacha_point" {
					got += rm["count"].(int)
				}
			}
		}
	}
	if got != 228 {
		t.Fatalf("scaled total=%d want 228", got)
	}
	// 原表不被改写。
	if table["1"].(map[string]any)["10"].(map[string]any)["rewards"].([]any)[0].(map[string]any)["count"] != 100 {
		t.Fatalf("catalog appear_table 被污染")
	}
}

// CollectRuneFactors 仅乘算经验(28)/金币(29)符文。
func TestCollectRuneFactors(t *testing.T) {
	expF, goldF := CollectRuneFactors([]PlayerSkill{
		{SkillID: 28, Level: 65}, {SkillID: 29, Level: 65},
		{SkillID: 29, Level: 130}, {SkillID: 1, Level: 30},
	})
	if expF != 1.1 {
		t.Fatalf("expFactor=%v", expF)
	}
	if goldF != 1.1*1.2 {
		t.Fatalf("goldFactor=%v", goldF)
	}
}
