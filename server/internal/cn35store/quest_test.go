// quest_test.go STORY350-03 剧情进度持久化验收：建号种子、进度/通关读取、
// 双账号隔离、重启保持、0005 旧库升级回填。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/database"
)

// applyThrough0005 手工回放 0001-0005 + 台账行，模拟 0006 前的真实旧库。
func applyThrough0005(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql",
		"0004_cn35_guest_device.sql", "0005_cn35_inventory.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
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
}

func seedClear(t *testing.T, s *Store, userID string, questID int, diff string, count int) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_quest_clears
		(user_id,quest_id,difficulty,clear_count,updated_at) VALUES (?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		userID, questID, diff, count); err != nil {
		t.Fatalf("seed clear: %v", err)
	}
}

func questUserID(t *testing.T, s *Store, device string) string {
	t.Helper()
	r, err := s.QuickLogin(device)
	if err != nil {
		t.Fatal(err)
	}
	uid, _, err := s.ChooseServer(r.HeAccountID, r.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

// 建号即种进度行（createAccount 同事务）：新账号读回 (1,1,1,0)+空 clears。
func TestQuestProgressSeedOnCreate(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-quest-seed")

	p, err := s.QuestProgress(uid)
	if err != nil {
		t.Fatal(err)
	}
	if p.StageID != 1 || p.AreaID != 1 || p.QuestID != 1 || p.HardModeStageID != 0 {
		t.Fatalf("初始进度应为 (1,1,1,0)，got %+v", p)
	}
	if len(p.Clears) != 0 {
		t.Fatalf("初始通关表应空: %+v", p.Clears)
	}
	// 台账式直查：建号事务确实落了行（不是读面默认兜底）。
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_quest_progress WHERE user_id=?`, uid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("进度行未落库: n=%d err=%v", n, err)
	}
}

// preset_data 注入面：Snapshot user 带四个进度字段且与进度表一致。
func TestSnapshotQuestProgressFields(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-quest-snap")

	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]int{
		"stage_id": 1, "area_id": 1, "quest_id": 1, "hard_mode_stage_id": 0,
	} {
		if snap.User[k] != want {
			t.Fatalf("user.%s=%v want %d", k, snap.User[k], want)
		}
	}
	// 推进后快照跟随（测试用 SQL 直改模拟 07 写面结果）。
	if _, err = s.DB().Exec(`UPDATE cn35_quest_progress SET stage_id=2,area_id=3,quest_id=6,hard_mode_stage_id=1 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	snap2, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.User["stage_id"] != 2 || snap2.User["area_id"] != 3 ||
		snap2.User["quest_id"] != 6 || snap2.User["hard_mode_stage_id"] != 1 {
		t.Fatalf("进度推进未反映到快照: %v", snap2.User)
	}
}

// 通关计数读取 + 双账号隔离：A 的 clear 不污染 B；重启后不丢。
func TestQuestClearsIsolationAndRestart(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uidA := questUserID(t, s, "dev-quest-A")
	uidB := questUserID(t, s, "dev-quest-B")

	seedClear(t, s, uidA, 1, "normal", 2)
	seedClear(t, s, uidA, 1, "hard", 1)
	seedClear(t, s, uidA, 3, "normal", 1)
	seedClear(t, s, uidB, 2, "very_hard", 5)

	pA, err := s.QuestProgress(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if pA.Clears[1]["normal"] != 2 || pA.Clears[1]["hard"] != 1 || pA.Clears[3]["normal"] != 1 {
		t.Fatalf("A 通关计数: %+v", pA.Clears)
	}
	pB, err := s.QuestProgress(uidB)
	if err != nil {
		t.Fatal(err)
	}
	if pB.Clears[2]["very_hard"] != 5 || len(pB.Clears) != 1 {
		t.Fatalf("B 通关计数被污染: %+v", pB.Clears)
	}
	s.Close()

	// 重启：同一文件重开 Store，进度/通关全保持。
	s2 := openStore(t, dir)
	defer s2.Close()
	pA2, err := s2.QuestProgress(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if pA2.Clears[1]["normal"] != 2 || pA2.Clears[1]["hard"] != 1 || pA2.Clears[3]["normal"] != 1 {
		t.Fatalf("重启后 A 通关计数丢失: %+v", pA2.Clears)
	}
}

// 0005 旧库升级 0006：代表性 cn35 账号升级后获得回填进度行；
// 存量业务行（账号/profile/库存）不受影响。
func TestQuestProgressBackfillOnUpgrade(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mercd35.sqlite3")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	applyThrough0005(t, db)
	// 代表性旧账号：0005 时代的 cn35 账号（建号三件仍手写，因为当时
	// createAccount 不产进度行）。
	if _, err = db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,user_name,account_name,password_hash,sdk_account_id,device_id,is_new_user,tutorial_finish,last_server_id,created_at,updated_at)
		VALUES ('he-old','uid-old','tok','sk','老玩家','','','','dev-old',0,1,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_player_profiles
		(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,login_bonus_receivable_sec,guild_name_snapshot,created_at,updated_at)
		VALUES ('uid-old',10,0,100,20,20,'2026-01-01T00:00:00Z',300,300,'348',0,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openStore(t, dir) // 0006 在 Open 内应用
	p, err := s.QuestProgress("uid-old")
	if err != nil {
		t.Fatal(err)
	}
	if p.StageID != 1 || p.AreaID != 1 || p.QuestID != 1 || p.HardModeStageID != 0 {
		t.Fatalf("旧账号未回填初始进度: %+v", p)
	}
	snap, err := s.Snapshot("uid-old")
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["stage_id"] != 1 || snap.User["level"] != int64(10) {
		t.Fatalf("升级后快照异常: %v", snap.User)
	}
}

// 约束验证：非法难度/负计数被拒；进度 FK 限制删除账号。
func TestQuestTablesConstraints(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-quest-c")

	if _, err := s.DB().Exec(`INSERT INTO cn35_quest_clears
		(user_id,quest_id,difficulty,clear_count,updated_at) VALUES (?,1,'impossible',1,'x')`, uid); err == nil {
		t.Fatal("impossible 难度应被 CHECK 拒绝")
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_quest_clears
		(user_id,quest_id,difficulty,clear_count,updated_at) VALUES (?,1,'normal',-1,'x')`, uid); err == nil {
		t.Fatal("负 clear_count 应被 CHECK 拒绝")
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_quest_clears
		(user_id,quest_id,difficulty,clear_count,updated_at) VALUES ('ghost',1,'normal',1,'x')`); err == nil {
		t.Fatal("无账号的 clear 行应被 FK 拒绝")
	}
	if _, err := s.DB().Exec(`DELETE FROM cn35_accounts WHERE user_id=?`, uid); err == nil {
		t.Fatal("有进度行的账号应被 ON DELETE RESTRICT 拦截")
	}
}
