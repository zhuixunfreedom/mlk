package cn35store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func loadStageRosterInputs(t *testing.T) (*cn35game.StageRosterGrantConfig, *cardcatalog.Catalog) {
	t.Helper()
	cfg, err := cn35game.LoadStageRosterGrantConfig(filepath.Join("..", "..", "assets", "cn350", "stage_roster_grant.json"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cn35game.ValidateStageRosterGrant(cat, cfg); err != nil {
		t.Fatal(err)
	}
	return cfg, cat
}

func newStageRosterTestUser(t *testing.T, s *Store, device string) string {
	t.Helper()
	userID, _, err := s.EnsureDevAccount("min350-stage-" + device)
	if err != nil {
		t.Fatal(err)
	}
	return userID
}

func TestStageRosterGrantIdempotentAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mercd35.sqlite3")
	s := openStore(t, dir)
	cfg, cat := loadStageRosterInputs(t)
	userID := newStageRosterTestUser(t, s, "restart")

	first, err := s.GrantStageRoster(userID, cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if first.Idempotent || len(first.InsertedUnitIDs) != 20 || len(first.InsertedMonsterUUIDs) != 1 || len(first.AlreadyOwnedUnitIDs) != 0 {
		t.Fatalf("first result=%+v", first)
	}
	if first.UnitGrantKey == first.MonsterGrantKey || first.UnitGrantKey != cn35game.StageRosterUnitGrantKey || first.MonsterGrantKey != cn35game.StageRosterMonsterGrantKey {
		t.Fatalf("type-specific grant keys lost: %+v", first)
	}
	units, err := s.Units(userID)
	if err != nil || len(units) != 20 {
		t.Fatalf("units=%d err=%v", len(units), err)
	}
	for _, unit := range units {
		if unit.Level != 1 {
			t.Fatalf("unit %d level=%d, want base level 1", unit.UnitID, unit.Level)
		}
	}
	monsters, err := s.Monsters(userID)
	if err != nil || len(monsters) != 1 || monsters[0].MonsterID != 39 || monsters[0].Level != 1 {
		t.Fatalf("monsters=%+v err=%v", monsters, err)
	}
	var unitLedger, monsterLedger int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants WHERE user_id=? AND grant_key=?`, userID, cfg.UnitGrantKey).Scan(&unitLedger); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants WHERE user_id=? AND grant_key=?`, userID, cfg.MonsterGrantKey).Scan(&monsterLedger); err != nil {
		t.Fatal(err)
	}
	if unitLedger != 20 || monsterLedger != 1 {
		t.Fatalf("typed ledgers unit=%d monster=%d", unitLedger, monsterLedger)
	}

	second, err := s.GrantStageRoster(userID, cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Idempotent || len(second.InsertedUnitIDs) != 0 || len(second.InsertedMonsterUUIDs) != 0 || len(second.AlreadyOwnedUnitIDs) != 20 || second.AlreadyOwnedMonsterCount != 1 {
		t.Fatalf("replay result=%+v", second)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenExistingForGrant(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	afterRestart, err := reopened.GrantStageRoster(userID, cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !afterRestart.Idempotent || len(afterRestart.InsertedUnitIDs) != 0 || len(afterRestart.InsertedMonsterUUIDs) != 0 {
		t.Fatalf("restart replay=%+v", afterRestart)
	}
}

func TestStageRosterGrantPreservesOwnedRowsAndIsolatesAccounts(t *testing.T) {
	s := openStore(t, t.TempDir())
	cfg, cat := loadStageRosterInputs(t)
	userA := newStageRosterTestUser(t, s, "owner")
	userB := newStageRosterTestUser(t, s, "other")
	// Seed higher levels and a pre-existing pet row; stage issuance must keep them intact.
	if err := s.WithTx(func(tx *sql.Tx) error {
		if err := InsertUnitTx(tx, userA, cn35game.UnitRecord{UnitID: 152, Level: 50, Protected: true}); err != nil {
			return err
		}
		return InsertMonsterTx(tx, userA, cn35game.MonsterRecord{UUID: "prior-monster-39", MonsterID: 39, Level: 88, Protected: true})
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.GrantStageRoster(userA, cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InsertedUnitIDs) != 19 || !reflect.DeepEqual(res.AlreadyOwnedUnitIDs, []int{152}) || len(res.InsertedMonsterUUIDs) != 0 || res.AlreadyOwnedMonsterCount != 1 {
		t.Fatalf("no-downgrade result=%+v", res)
	}
	var level, protected int
	if err := s.DB().QueryRow(`SELECT level,protected FROM cn35_player_units WHERE user_id=? AND unit_id=152`, userA).Scan(&level, &protected); err != nil {
		t.Fatal(err)
	}
	if level != 50 || protected != 1 {
		t.Fatalf("pre-owned unit changed: level=%d protected=%d", level, protected)
	}
	if err := s.DB().QueryRow(`SELECT level,protected FROM cn35_player_monsters WHERE user_id=? AND uuid=?`, userA, "prior-monster-39").Scan(&level, &protected); err != nil {
		t.Fatal(err)
	}
	if level != 88 || protected != 1 {
		t.Fatalf("pre-owned pet changed: level=%d protected=%d", level, protected)
	}
	var bUnits, bPets, bGrants int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=?`, userB).Scan(&bUnits); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_monsters WHERE user_id=?`, userB).Scan(&bPets); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants WHERE user_id=? AND grant_key IN (?,?)`, userB, cfg.UnitGrantKey, cfg.MonsterGrantKey).Scan(&bGrants); err != nil {
		t.Fatal(err)
	}
	if bUnits != 0 || bPets != 0 || bGrants != 0 {
		t.Fatalf("account B changed: units=%d pets=%d grants=%d", bUnits, bPets, bGrants)
	}
}

func TestStageRosterGrantRollsBackUnitsAndBothLedgersOnPetFailure(t *testing.T) {
	s := openStore(t, t.TempDir())
	cfg, cat := loadStageRosterInputs(t)
	userID := newStageRosterTestUser(t, s, "rollback")
	forced := errors.New("forced pet UUID failure")
	_, err := s.grantStageRosterWithUUID(userID, cfg, cat, func() (string, error) { return "", forced })
	if !errors.Is(err, forced) {
		t.Fatalf("error=%v, want forced UUID failure", err)
	}
	for _, table := range []string{"cn35_player_units", "cn35_player_monsters", "cn35_inventory_grants"} {
		var n int
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE user_id=?`
		if err := s.DB().QueryRow(q, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("rollback left %d rows in %s", n, table)
		}
	}
}

func TestStageRosterGrantRejectsPartialTypedLedger(t *testing.T) {
	s := openStore(t, t.TempDir())
	cfg, cat := loadStageRosterInputs(t)
	userID := newStageRosterTestUser(t, s, "partial-ledger")
	if err := s.WithTx(func(tx *sql.Tx) error {
		return RecordGrantTx(tx, userID, cfg.UnitGrantKey, cfg.UnitIDs[0])
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantStageRoster(userID, cfg, cat); !errors.Is(err, cn35game.ErrGrantVersionConflict) {
		t.Fatalf("partial typed ledger must fail closed, got %v", err)
	}
	if n := countTable(t, s, "cn35_player_units", userID); n != 0 {
		t.Fatalf("partial ledger must not mutate inventory, got %d", n)
	}
}

// generalStageRosterCfg 通用批次配置：候选集由配置声明，不锁 v1 固定 20+1。
func generalStageRosterCfg(t *testing.T, cat *cardcatalog.Catalog, version, key string, unitIDs []int) *cn35game.StageRosterGrantConfig {
	t.Helper()
	meta := cat.Meta()
	cfg := &cn35game.StageRosterGrantConfig{
		GrantVersion: version,
		InitialLevel: 1,
		UnitIDs:      unitIDs,
		MonsterIDs:   []int{},
		Source: cn35game.StageRosterSource{
			UnitCatalogVersion:    meta.UnitsVersion,
			UnitCatalogSHA256:     meta.UnitsSHA256,
			MonsterCatalogVersion: meta.MonstersVersion,
			MonsterCatalogSHA256:  meta.MonstersSHA256,
		},
		UnitGrantKey: key,
		ValueScope:   "confirmed catalog base values; level 1; not max-level",
	}
	sum, err := cn35game.StageRosterConfigSHA256(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ContentSHA256 = sum
	return cfg
}

func TestStageRosterGrantGeneralBatchCoexistsWithV1(t *testing.T) {
	s := openStore(t, t.TempDir())
	v1cfg, cat := loadStageRosterInputs(t)
	userID := newStageRosterTestUser(t, s, "general-batch")
	if _, err := s.GrantStageRoster(userID, v1cfg, cat); err != nil {
		t.Fatal(err)
	}
	// 与 v1 台账独立的新批次键发放重建同伴 414。
	cfg414 := generalStageRosterCfg(t, cat, "unit414-rebuild-v1", "cn350-unit414-rebuild-v1-unit", []int{414})
	res, err := s.GrantStageRoster(userID, cfg414, cat)
	if err != nil {
		t.Fatal(err)
	}
	if res.Idempotent || !reflect.DeepEqual(res.InsertedUnitIDs, []int{414}) || len(res.InsertedMonsterUUIDs) != 0 {
		t.Fatalf("general batch result=%+v", res)
	}
	units, err := s.Units(userID)
	if err != nil || len(units) != 21 {
		t.Fatalf("units=%d err=%v, want 21", len(units), err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants WHERE user_id=? AND grant_key=?`,
		userID, "cn350-unit414-rebuild-v1-unit").Scan(&n); err != nil || n != 1 {
		t.Fatalf("general ledger rows=%d err=%v", n, err)
	}
	// 重放幂等
	replay, err := s.GrantStageRoster(userID, cfg414, cat)
	if err != nil || !replay.Idempotent || len(replay.InsertedUnitIDs) != 0 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	// 同键不同 ID 集 → 版本冲突拒绝
	conflict := generalStageRosterCfg(t, cat, "unit414-rebuild-v1", "cn350-unit414-rebuild-v1-unit", []int{426})
	if _, err := s.GrantStageRoster(userID, conflict, cat); !errors.Is(err, cn35game.ErrGrantVersionConflict) {
		t.Fatalf("same-key different set: got %v, want ErrGrantVersionConflict", err)
	}
	// v1 台账不受通用批次影响
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants WHERE user_id=? AND grant_key=?`,
		userID, cn35game.StageRosterUnitGrantKey).Scan(&n); err != nil || n != 20 {
		t.Fatalf("v1 ledger rows=%d err=%v", n, err)
	}
}

func TestOpenReadOnlyStageStoreRejectsWrites(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	var queryOnly int
	if err := ro.DB().QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Fatalf("query_only=%d err=%v", queryOnly, err)
	}
	if _, err := ro.DB().Exec(`CREATE TABLE should_not_exist (id INTEGER)`); err == nil {
		t.Fatal("read-only stage preview connection allowed a write")
	}
}
