package cn35game_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestLoadAndValidateStageRosterGrant(t *testing.T) {
	base := filepath.Join("..", "..", "assets", "cn350", "stage_roster_grant.json")
	cfg, err := cn35game.LoadStageRosterGrantConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cn35game.ValidateStageRosterGrant(cat, cfg); err != nil {
		t.Fatalf("valid phase config rejected: %v", err)
	}
	if got := len(cfg.CompanionIDs()); got != 20 {
		t.Fatalf("unit targets=%d, want 20", got)
	}
	if got := len(cfg.PetIDs()); got != 1 || cfg.PetIDs()[0] != 39 {
		t.Fatalf("pet targets=%v, want [39]", cfg.PetIDs())
	}
	if cfg.InitialLevel != 1 || cfg.ValueScope == "" || cfg.ValueScope == "max-level" {
		t.Fatalf("base-value scope lost: %+v", cfg)
	}
}

func TestValidateStageRosterRejectsImplicitVersionDrift(t *testing.T) {
	cfg, err := cn35game.LoadStageRosterGrantConfig(filepath.Join("..", "..", "assets", "cn350", "stage_roster_grant.json"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.UnitIDs[0] = 151
	if err := cn35game.ValidateStageRosterGrant(cat, cfg); !errors.Is(err, cn35game.ErrStageRosterConfig) {
		t.Fatalf("changed candidate set must fail closed, got %v", err)
	}
}

func loadCatalog(t *testing.T) *cardcatalog.Catalog {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// generalStageRosterCfg 构造一个合法通用批次：候选集由配置声明
// （不限于 v1 固定 20+1），content_sha256 按代码口径自举。
func generalStageRosterCfg(t *testing.T, cat *cardcatalog.Catalog) *cn35game.StageRosterGrantConfig {
	t.Helper()
	meta := cat.Meta()
	cfg := &cn35game.StageRosterGrantConfig{
		GrantVersion: "unit414-rebuild-v1",
		InitialLevel: 1,
		UnitIDs:      []int{414},
		MonsterIDs:   []int{},
		Source: cn35game.StageRosterSource{
			UnitCatalogVersion:    meta.UnitsVersion,
			UnitCatalogSHA256:     meta.UnitsSHA256,
			MonsterCatalogVersion: meta.MonstersVersion,
			MonsterCatalogSHA256:  meta.MonstersSHA256,
		},
		UnitGrantKey: "cn350-unit414-rebuild-v1-unit",
		ValueScope:   "confirmed catalog base values; level 1; not max-level",
	}
	sum, err := cn35game.StageRosterConfigSHA256(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ContentSHA256 = sum
	return cfg
}

func TestValidateStageRosterGeneralAcceptsNewBatch(t *testing.T) {
	cat := loadCatalog(t)
	if err := cn35game.ValidateStageRosterGrant(cat, generalStageRosterCfg(t, cat)); err != nil {
		t.Fatalf("general batch rejected: %v", err)
	}
}

func TestValidateStageRosterGeneralRejects(t *testing.T) {
	cat := loadCatalog(t)
	cases := []struct {
		name   string
		mutate func(*cn35game.StageRosterGrantConfig)
	}{
		{"empty version", func(c *cn35game.StageRosterGrantConfig) { c.GrantVersion = "" }},
		{"zero level", func(c *cn35game.StageRosterGrantConfig) { c.InitialLevel = 0 }},
		{"empty id sets", func(c *cn35game.StageRosterGrantConfig) { c.UnitIDs = nil; c.UnitGrantKey = "" }},
		{"missing unit key", func(c *cn35game.StageRosterGrantConfig) { c.UnitGrantKey = "" }},
		{"reserved five-star key", func(c *cn35game.StageRosterGrantConfig) {
			c.UnitGrantKey = cn35game.FiveStarGrantKey
		}},
		{"reserved v1 unit key", func(c *cn35game.StageRosterGrantConfig) {
			c.UnitGrantKey = cn35game.StageRosterUnitGrantKey
		}},
		{"duplicate ids", func(c *cn35game.StageRosterGrantConfig) { c.UnitIDs = []int{414, 414} }},
		{"unknown unit", func(c *cn35game.StageRosterGrantConfig) { c.UnitIDs = []int{999999} }},
		{"catalog drift", func(c *cn35game.StageRosterGrantConfig) { c.Source.UnitCatalogSHA256 = "x" }},
		{"content drift", func(c *cn35game.StageRosterGrantConfig) {
			c.UnitIDs = []int{426} // sha 未同步 → content_sha256 不符
		}},
	}
	for _, tc := range cases {
		cfg := generalStageRosterCfg(t, cat)
		tc.mutate(cfg)
		if err := cn35game.ValidateStageRosterGrant(cat, cfg); !errors.Is(err, cn35game.ErrStageRosterConfig) {
			t.Fatalf("%s: got %v, want ErrStageRosterConfig", tc.name, err)
		}
	}
	// 两键相同（非预留键）也应拒绝
	cfg := generalStageRosterCfg(t, cat)
	cfg.MonsterIDs = []int{39}
	cfg.MonsterGrantKey = cfg.UnitGrantKey
	if err := cn35game.ValidateStageRosterGrant(cat, cfg); !errors.Is(err, cn35game.ErrStageRosterConfig) {
		t.Fatalf("shared typed key: got %v, want ErrStageRosterConfig", err)
	}
}
