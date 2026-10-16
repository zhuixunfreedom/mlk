// monster_size_test.go STAT350-04 存储层边界：
//   - 只读打开未应用 0009 的旧库时 Monsters 退回旧列集（Size=nil），不报错；
//   - OpenExistingForGrant 把 0009 纳入必需迁移清单（发放门禁）。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// applyMigrationFiles 手工回放指定迁移名（模拟真实旧库的 schema 版本）。
func applyMigrationFiles(t *testing.T, db *sql.DB, names ...string) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations
		(name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			name, fmt.Sprintf("%x", sum)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

var preSizeMigrations = []string{
	"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql",
	"0004_cn35_guest_device.sql", "0005_cn35_inventory.sql",
	"0006_cn35_quest_progress.sql", "0007_cn35_quest_battle.sql",
	"0008_cn35_account_decks.sql",
}

// TestMonstersReadOnlyOnPreSizeSchema 未应用 0009 的库用只读打开时，
// Monsters 必须退回旧列集（Size 全 nil），不报错、不迁移、不代填。
func TestMonstersReadOnlyOnPreSizeSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-0008.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationFiles(t, db, preSizeMigrations...)
	// 代表性旧行（0005 表结构，无 Size 列）。
	if _, err := db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('he-1','u-1','t','s',0,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cn35_player_monsters
		(user_id,uuid,monster_id,level,protected,created_at,updated_at)
		VALUES ('u-1','legacy-m1',39,1,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer st.Close()
	if st.monsterSizeColumns() {
		t.Fatal("0008 库不应被判定有 Size 列")
	}
	recs, err := st.Monsters("u-1")
	if err != nil {
		t.Fatalf("旧库 Monsters: %v", err)
	}
	if len(recs) != 1 || recs[0].MonsterID != 39 {
		t.Fatalf("旧库 Monsters=%+v", recs)
	}
	if recs[0].Scale != nil || recs[0].RealScale != nil || recs[0].ScaleLimit != nil || recs[0].IsRare != nil {
		t.Fatalf("Size 应全部为 nil（未证/降级）: %+v", recs[0])
	}
}

// TestOpenExistingForGrantRequires0009 发放门禁必须拒绝 0009 未应用的库。
func TestOpenExistingForGrantRequires0009(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gated.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationFiles(t, db, preSizeMigrations...)
	db.Close()

	if _, err := OpenExistingForGrant(path); err == nil {
		t.Fatal("0009 pending 时 OpenExistingForGrant 应拒绝")
	}
	// 补齐 0009 后放行。
	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationFiles(t, db2, "0009_cn35_monster_size.sql")
	db2.Close()
	st, err := OpenExistingForGrant(path)
	if err != nil {
		t.Fatalf("0009 应用后应放行: %v", err)
	}
	defer st.Close()
	if !st.monsterSizeColumns() {
		t.Fatal("0009 应用后应判定有 Size 列")
	}
}
