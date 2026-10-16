// inventory_test.go PLAY350-06 库存持久化验收：空库 0005、0004 旧库升级、
// 事务写入/回滚、账号隔离、台账幂等冲突、模板校验、重启恢复。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func seedAccount(t *testing.T, s *Store, deviceID string) string {
	t.Helper()
	res, err := s.QuickLogin(deviceID)
	if err != nil {
		t.Fatal(err)
	}
	uid, _, err := s.ChooseServer(res.HeAccountID, res.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func grantFive(t *testing.T, s *Store, uid string) {
	t.Helper()
	err := s.WithTx(func(tx *sql.Tx) error {
		for _, id := range []int{152, 157, 161, 230, 348} {
			if err := InsertUnitTx(tx, uid, cn35game.UnitRecord{
				UnitID: id, Level: 1, Newcomer: true}); err != nil {
				return err
			}
			if err := RecordGrantTx(tx, uid, "play350-05-v1", id); err != nil {
				return err
			}
		}
		return InsertMonsterTx(tx, uid, cn35game.MonsterRecord{
			UUID: "mon-uuid-1", MonsterID: 1, Level: 1})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInventoryFreshSchema(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	var n int
	assertLatestSchema(t, s)
	for _, tbl := range []string{"cn35_player_units", "cn35_player_monsters", "cn35_inventory_grants"} { // 0009 追加的魔宠 Size 列（可空，旧行语义不变）。

		if err := s.DB().QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatalf("%s 不存在: %v", tbl, err)
		}
	}
	if _, err := s.DB().Exec(
		`SELECT uuid,scale,real_scale,scale_limit,is_rare FROM cn35_player_monsters LIMIT 0`); err != nil {
		t.Fatalf("0009 Size 列缺失: %v", err)
	}
}

// TestInventoryUpgradeFrom0004 手工回放 0001-0004 + 代表性账号，Open 追加 0005
// 后旧数据完整、库存为空表。
func TestInventoryUpgradeFrom0004(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mercd35.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql", "0004_cn35_guest_device.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("exec %s: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, name, sum); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 升级: %v", err)
	}
	defer s.Close()
	assertLatestSchema(t, s)
	// 旧账号建号仍工作（0003 表在升级前已在位）
	uid := seedAccount(t, s, "dev-upg")
	units, err := s.Units(uid)
	if err != nil || len(units) != 0 {
		t.Fatalf("旧账号库存应为空: %v %v", units, err)
	}
}

func TestInventoryGrantAndIsolation(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uidA := seedAccount(t, s, "dev-A")
	uidB := seedAccount(t, s, "dev-B")

	grantFive(t, s, uidA)

	units, err := s.Units(uidA)
	if err != nil || len(units) != 5 {
		t.Fatalf("A units=%v err=%v", units, err)
	}
	if units[0].UnitID != 152 || !units[0].Newcomer {
		t.Fatalf("unit 行: %+v", units[0])
	}
	mons, err := s.Monsters(uidA)
	if err != nil || len(mons) != 1 || mons[0].UUID != "mon-uuid-1" {
		t.Fatalf("A monsters=%v err=%v", mons, err)
	}
	// B 隔离：零库存
	if got, _ := s.Units(uidB); len(got) != 0 {
		t.Fatalf("B 应为空库存: %v", got)
	}

	// 台账查询与幂等冲突
	var applied map[int]bool
	err = s.WithTx(func(tx *sql.Tx) error {
		var err error
		applied, err = GrantAppliedTx(tx, uidA, "play350-05-v1")
		return err
	})
	if err != nil || len(applied) != 5 {
		t.Fatalf("台账: %v %v", applied, err)
	}
	err = s.WithTx(func(tx *sql.Tx) error {
		return RecordGrantTx(tx, uidA, "play350-05-v1", 152)
	})
	if !errors.Is(err, ErrGrantKeyConflict) {
		t.Fatalf("重复台账应 ErrGrantKeyConflict: %v", err)
	}
}

// TestInventoryFKAndRollback 复合 FK：魔宠装备到不存在/他账号单位 → 事务
// 整体回滚不留半写。
func TestInventoryFKAndRollback(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uidA := seedAccount(t, s, "dev-fk-A")
	uidB := seedAccount(t, s, "dev-fk-B")
	grantFive(t, s, uidA)

	host := 152
	// 宿主存在且同账号 → 成功
	err := s.WithTx(func(tx *sql.Tx) error {
		return InsertMonsterTx(tx, uidA, cn35game.MonsterRecord{
			UUID: "m-ok", MonsterID: 2, UnitID: &host, Level: 1})
	})
	if err != nil {
		t.Fatalf("合法装备失败: %v", err)
	}
	// 宿主不存在 → FK 拒绝、回滚
	err = s.WithTx(func(tx *sql.Tx) error {
		if err := InsertUnitTx(tx, uidB, cn35game.UnitRecord{UnitID: 999, Level: 1}); err != nil {
			return err // unit 999 行插入本身成功（无模板 FK），回滚点在 monster
		}
		bad := 999 // uidB 没有 unit 999?——刚插入…改用它账号不存在宿主
		return InsertMonsterTx(tx, uidA, cn35game.MonsterRecord{
			UUID: "m-bad", MonsterID: 3, UnitID: &bad, Level: 1})
	})
	if err == nil {
		t.Fatal("跨账号宿主/不存在宿主应触发 FK 失败")
	}
	// 回滚核验：m-bad 不存在；unit 999 归属 uidB（同事务其插入已回滚?——
	// 上面 tx 中 uidB 的 999 与该 monster 同 tx，故一起回滚）
	if mons, _ := s.Monsters(uidA); len(mons) != 2 {
		t.Fatalf("回滚后 A 魔宠数: %v", mons)
	}
	if unitsB, _ := s.Units(uidB); len(unitsB) != 0 {
		t.Fatalf("回滚后 B 单位应空: %v", unitsB)
	}
}

func TestTemplateValidation(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Skipf("cards 不可用: %v", err)
	}
	if err := cn35game.ValidateUnitTemplates(cat, []cn35game.UnitRecord{{UnitID: 348, Level: 1}}); err != nil {
		t.Fatalf("348 应合法: %v", err)
	}
	if err := cn35game.ValidateUnitTemplates(cat, []cn35game.UnitRecord{{UnitID: 999999, Level: 1}}); !errors.Is(err, cn35game.ErrUnknownTemplate) {
		t.Fatalf("未知模板: %v", err)
	}
	if err := cn35game.ValidateMonsterTemplates(cat, []cn35game.MonsterRecord{{UUID: "x", MonsterID: 1, Level: 1}}); err != nil {
		t.Fatalf("monster 1 应合法: %v", err)
	}
}

// TestInventoryRestartRecovery 关库重开后库存仍在。
func TestInventoryRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := seedAccount(t, s, "dev-restart")
	grantFive(t, s, uid)
	s.Close()

	s2, err := Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	units, err := s2.Units(uid)
	if err != nil || len(units) != 5 {
		t.Fatalf("重启恢复: %v %v", units, err)
	}
}
