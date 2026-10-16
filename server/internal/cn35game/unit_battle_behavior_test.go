// unit_battle_behavior_test.go 战斗行为字段单测：官方样本覆盖 / 武器默认
// 派生 / {element} 替换 / multi_hit_count 规则 / 全量 catalog 回归。
package cn35game

import (
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

func intPtr(v int) *int { return &v }

// TestUnitBehaviorOverride152 官方样本覆盖：152 逐值下发，不走武器默认。
func TestUnitBehaviorOverride152(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	u, err := cat.Unit(152)
	if err != nil {
		t.Fatalf("模板 152: %v", err)
	}
	doc, err := buildUnitDoc(UnitRecord{UnitID: 152, Level: 1}, u)
	if err != nil {
		t.Fatalf("buildUnitDoc: %v", err)
	}
	if doc["act_type_id"] != "attack" {
		t.Fatalf("act_type_id=%v", doc["act_type_id"])
	}
	if doc["act_target_id"] != "reachable_monsters" {
		t.Fatalf("act_target_id=%v", doc["act_target_id"])
	}
	if doc["hit_frame"].(float64) != 10 {
		t.Fatalf("hit_frame=%v", doc["hit_frame"])
	}
	if doc["multi_hit_count"].(float64) != 1 {
		t.Fatalf("multi_hit_count=%v", doc["multi_hit_count"])
	}
	if doc["attack_effect"] != "effect_pd/effect_fireHit" {
		t.Fatalf("attack_effect=%v", doc["attack_effect"])
	}
	if doc["attack_effect_x"].(float64) != 80 {
		t.Fatalf("attack_effect_x=%v", doc["attack_effect_x"])
	}
}

// TestUnitBehaviorWeaponDefaultElement 武器 5（魔法）默认行 + {element} 替换：
// element=2 → water；Hits=3 → multi_hit_count=3 且 interval 保留默认 0.4。
func TestUnitBehaviorWeaponDefaultElement(t *testing.T) {
	u := &cardcatalog.Unit{ID: 99999, Weapon: 5, Element: 2, Hits: intPtr(3)}
	doc := map[string]any{}
	applyUnitBattleBehavior(doc, u)
	if doc["target_effect"] != "effect_pd/effect_magichit_water_2" {
		t.Fatalf("target_effect=%v", doc["target_effect"])
	}
	if doc["multi_hit_count"] != 3 {
		t.Fatalf("multi_hit_count=%v", doc["multi_hit_count"])
	}
	if doc["multi_hit_interval"].(float64) != 0.4 {
		t.Fatalf("multi_hit_interval=%v", doc["multi_hit_interval"])
	}
	if doc["attack_range"].(float64) != 32 {
		t.Fatalf("attack_range=%v", doc["attack_range"])
	}
}

// TestUnitBehaviorPriestFallback 武器 7（祭司）、Hits nil：暂按普通攻击，
// multi_hit_count 兜底 1 且 multi_hit_interval 强制 0。
func TestUnitBehaviorPriestFallback(t *testing.T) {
	u := &cardcatalog.Unit{ID: 99998, Weapon: 7, Element: 4}
	doc := map[string]any{}
	applyUnitBattleBehavior(doc, u)
	if doc["act_type_id"] != "attack" {
		t.Fatalf("act_type_id=%v", doc["act_type_id"])
	}
	if doc["multi_hit_count"] != 1 {
		t.Fatalf("multi_hit_count=%v", doc["multi_hit_count"])
	}
	if doc["multi_hit_interval"].(float64) != 0 {
		t.Fatalf("multi_hit_interval=%v", doc["multi_hit_interval"])
	}
}

// TestUnitBehaviorInvalidWeapon 非法武器回退 "3" 默认行（hit_frame=13）。
func TestUnitBehaviorInvalidWeapon(t *testing.T) {
	u := &cardcatalog.Unit{ID: 99997, Weapon: 0, Element: 1, Hits: intPtr(1)}
	doc := map[string]any{}
	applyUnitBattleBehavior(doc, u)
	if doc["hit_frame"].(float64) != 13 {
		t.Fatalf("hit_frame=%v want 13（武器 3 默认）", doc["hit_frame"])
	}
}

// TestUnitBehaviorRawHitsString Raw["hits"] 数字串兜底（Hits nil 时取之）。
func TestUnitBehaviorRawHitsString(t *testing.T) {
	u := &cardcatalog.Unit{ID: 99996, Weapon: 3, Element: 1,
		Raw: map[string]any{"hits": "2"}}
	doc := map[string]any{}
	applyUnitBattleBehavior(doc, u)
	if doc["multi_hit_count"] != 2 {
		t.Fatalf("multi_hit_count=%v want 2", doc["multi_hit_count"])
	}
}

// TestUnitBehaviorRawHitsGarbage Raw["hits"] 为 "暂缺"/"暂无" 等非数字 → 1。
func TestUnitBehaviorRawHitsGarbage(t *testing.T) {
	for _, raw := range []string{"暂缺", "暂无"} {
		u := &cardcatalog.Unit{ID: 99995, Weapon: 3, Element: 1,
			Raw: map[string]any{"hits": raw}}
		doc := map[string]any{}
		applyUnitBattleBehavior(doc, u)
		if doc["multi_hit_count"] != 1 {
			t.Fatalf("hits=%q multi_hit_count=%v want 1", raw, doc["multi_hit_count"])
		}
	}
}

// TestUnitBehaviorFullCatalog 真实 catalog 全量回归：每单位经 buildUnitDoc
// 后 act_target_id 非空（否则 BaseAI targets 恒空）且 multi_hit_count≥1
// （否则伤害循环 0 次）。
func TestUnitBehaviorFullCatalog(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	ids := cat.UnitIDs()
	if len(ids) != 1829 {
		t.Logf("catalog 单位数=%d（期望 1829，继续逐条断言）", len(ids))
	}
	for _, id := range ids {
		u, err := cat.Unit(id)
		if err != nil {
			t.Fatalf("Unit(%d): %v", id, err)
		}
		doc, err := buildUnitDoc(UnitRecord{UnitID: id, Level: 1}, u)
		if err != nil {
			t.Fatalf("unit %d buildUnitDoc: %v", id, err)
		}
		if s, _ := doc["act_target_id"].(string); s == "" {
			t.Fatalf("unit %d act_target_id 为空", id)
		}
		n, ok := doc["multi_hit_count"]
		if !ok {
			t.Fatalf("unit %d multi_hit_count 缺键", id)
		}
		switch v := n.(type) {
		case float64:
			if v < 1 {
				t.Fatalf("unit %d multi_hit_count=%v", id, v)
			}
		case int:
			if v < 1 {
				t.Fatalf("unit %d multi_hit_count=%v", id, v)
			}
		default:
			t.Fatalf("unit %d multi_hit_count 类型 %T", id, n)
		}
	}
}
