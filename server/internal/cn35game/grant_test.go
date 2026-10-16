// grant_test.go PLAY350-08 五卡配置加载/校验：
// 真实 five_star_grant.json 的 content_sha256 完整性 + catalog 语义门禁。
package cn35game_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestLoadFiveStarConfig(t *testing.T) {
	cfg, err := cn35game.LoadFiveStarConfig(
		filepath.Join("..", "..", "assets", "cn350", "five_star_grant.json"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if cfg.GrantVersion != "play350-05-v1" || cfg.LeaderUnitID != 348 {
		t.Fatalf("配置: %+v", cfg)
	}
	ids := cfg.UnitIDs()
	want := []int{152, 157, 161, 230, 348}
	if len(ids) != len(want) {
		t.Fatalf("卡数: %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("卡序: %v", ids)
		}
	}
	// 对真实 catalog 校验必须过（五卡均 rare5、模板存在）。
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if err := cn35game.ValidateFiveStar(cat, cfg); err != nil {
		t.Fatalf("真实配置校验失败: %v", err)
	}
}

// TestLoadFiveStarConfigTampered 内容哈希不符 → 拒绝（防配置漂移）。
func TestLoadFiveStarConfigTampered(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"grant_version":"x","leader_unit_id":1,"units":[{"id":1,"initial_level":1}],"content_sha256":"deadbeef"}`)
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := cn35game.LoadFiveStarConfig(p)
	if !errors.Is(err, cn35game.ErrGrantTarget) {
		t.Fatalf("篡改配置应拒绝: %v", err)
	}
}
