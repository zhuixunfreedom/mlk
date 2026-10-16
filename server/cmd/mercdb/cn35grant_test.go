// cn35grant_test.go 回归：`cn35-grant preview|status` 必须是**只读观察**——
// 不得对目标库触发 Migrate。
//
// 背景（2026-09-23 实测事故）：grantStatus 原用 cn35store.Open（会 Migrate），
// 对未升级的目标库跑一次 `cn35-grant preview -user ...` 就把该库从 0007 升到了
// 0009。修复为 OpenReadOnly，本测试锁住该行为。
package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// pre0009Migrations 0009 之前的全部迁移（回放成"未升级的目标库"）。
var pre0009Migrations = []string{
	"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql",
	"0004_cn35_guest_device.sql", "0005_cn35_inventory.sql",
	"0006_cn35_quest_progress.sql", "0007_cn35_quest_battle.sql",
	"0008_cn35_account_decks.sql",
}

func migrationPath(name string) string {
	return filepath.Join("..", "..", "internal", "database", "migrations", name)
}

func applyMigrations(t *testing.T, db *sql.DB, names ...string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations
		(name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := os.ReadFile(migrationPath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES (?,?,?)`,
			name, fmt.Sprintf("%x", sum), "2026-09-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func appliedMigrations(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestGrantPreviewNeverMigrates 对停在 0008 的库跑 preview/status，
// 迁移台账不得变化（0009 仍 pending）。
func TestGrantPreviewNeverMigrates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-0008.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyMigrations(t, db, pre0009Migrations...)
	if _, err := db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('he-1','u-1','t','s',0,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := appliedMigrations(t, path)

	// preview（带 -user 会走 grantStatus）。相对路径按测试工作目录（cmd/mercdb）给足。
	seed := filepath.Join("..", "..", "assets", "cn350", "five_star_grant.json")
	cards := filepath.Join("..", "..", "assets", "shared", "cards")
	for _, action := range []string{"preview", "status"} {
		if err := cn35Grant([]string{action, "-db", path, "-user", "u-1",
			"-seed", seed, "-cards", cards}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if after := appliedMigrations(t, path); after != before {
		t.Fatalf("只读预览改动了迁移台账: %d → %d（不得触发 Migrate）", before, after)
	}
	// 0009 必须仍然缺席（列也不应存在）。
	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var has int
	if err := db2.QueryRow(
		`SELECT count(*) FROM pragma_table_info('cn35_player_monsters') WHERE name='scale'`).Scan(&has); err != nil {
		t.Fatal(err)
	}
	if has != 0 {
		t.Fatal("只读预览不得给旧库补上 0009 的 scale 列")
	}
}
