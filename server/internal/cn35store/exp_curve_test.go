// exp_curve_test.go 用户等级经验表接入 store 层的结算与迁移验收：
//   - 低级多级跳升级、max_exp 同事务维护；
//   - 封顶冻结（2100 级无下一级）；
//   - 快照 need_exp/exp_table 按等级推导；
//   - 0021→0022 旧库升级回填存量 max_exp。
//
// 曲线来源与证据见 cn35game/user_exp_table.go、notes/proposed/2026-10-01-cn35-user-exp-curve.md。
package cn35store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

// profileExp 读取 profile 的 level/exp/max_exp。
func profileExp(t *testing.T, s *Store, uid string) (level, exp, maxExp int) {
	t.Helper()
	if err := s.DB().QueryRow(`SELECT level,exp,max_exp FROM cn35_player_profiles WHERE user_id=?`,
		uid).Scan(&level, &exp, &maxExp); err != nil {
		t.Fatal(err)
	}
	return level, exp, maxExp
}

// settleExp 建号→建档→扣点→结算一条龙，返回结算后的 profile 三元组。
func settleExp(t *testing.T, s *Store, device string, gain int) string {
	t.Helper()
	uid := questUserID(t, s, device)
	sess := battleFixture(uid, 1, 1, 2)
	sess.Exp = gain
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatalf("OpenBattle: %v", err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatalf("PayBattleAP: %v", err)
	}
	res, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 5})
	if err != nil || res.Already {
		t.Fatalf("SettleBattle: %v %+v", err, res)
	}
	return uid
}

// TestSettleExpMultiLevelUp 1 级 50/100 收 550：跨 need(1..5)=500 → 6 级余 100，
// max_exp 同事务维护为 need(6)=400；快照窗口 [400,1000]。
func TestSettleExpMultiLevelUp(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := settleExp(t, s, "dev-exp-multi", 550)
	level, exp, maxExp := profileExp(t, s, uid)
	if level != 6 || exp != 100 || maxExp != 400 {
		t.Fatalf("多级跳结算: level=%d exp=%d max_exp=%d, want 6/100/400", level, exp, maxExp)
	}
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.User["need_exp"]; got != 400 {
		t.Fatalf("快照 need_exp = %v, want 400", got)
	}
	if win, ok := snap.User["exp_table"].([]int64); !ok || win[0] != 400 || win[1] != 1000 {
		t.Fatalf("快照 exp_table = %v, want [400 1000]", snap.User["exp_table"])
	}
}

// TestSettleExpCapFreeze 封顶冻结：2100 级增益丢弃，三元组不变；
// 2099 级跨入 2100 保留扣减余数且 max_exp 保持末档。
func TestSettleExpCapFreeze(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-exp-cap")
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET level=2100,exp=0,max_exp=4200000 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if got := settleExp2(t, s, uid, 100000); got != [3]int{2100, 0, 4200000} {
		t.Fatalf("封顶冻结: %v, want [2100 0 4200000]", got)
	}
	// 跨入封顶：2099 级差 5 点收 10 → 2100 级余 5，max_exp 保持末档。
	uid2 := questUserID(t, s, "dev-exp-cap2")
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET level=2099,exp=4199995,max_exp=4200000 WHERE user_id=?`, uid2); err != nil {
		t.Fatal(err)
	}
	if got := settleExp2(t, s, uid2, 10); got != [3]int{2100, 5, 4200000} {
		t.Fatalf("跨入封顶: %v, want [2100 5 4200000]", got)
	}
}

// settleExp2 对既有账号直接结算一场 gain 经验的战斗，返回结算后三元组。
func settleExp2(t *testing.T, s *Store, uid string, gain int) [3]int {
	t.Helper()
	sess := battleFixture(uid, 1, 1, 2)
	sess.Exp = gain
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatalf("OpenBattle: %v", err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatalf("PayBattleAP: %v", err)
	}
	res, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 5})
	if err != nil || res.Already {
		t.Fatalf("SettleBattle: %v %+v", err, res)
	}
	level, exp, maxExp := profileExp(t, s, uid)
	return [3]int{level, exp, maxExp}
}

// TestFirstLevelSettleKeepsBehavior 既有行为回归：1 级 50/100 收 60 → 2 级余 10，
// max_exp=need(2)=100（原 happy path 断言语义不变，仅 max_exp 由表维护）。
func TestFirstLevelSettleKeepsBehavior(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := settleExp(t, s, "dev-exp-first", 60)
	level, exp, maxExp := profileExp(t, s, uid)
	if level != 2 || exp != 10 || maxExp != 100 {
		t.Fatalf("单级结算: level=%d exp=%d max_exp=%d, want 2/10/100", level, exp, maxExp)
	}
}

// applyThrough0021 手工回放 0001-0021 + 台账行，模拟 0022 之前的真实旧库
// （沿用 TestBattleUpgradeFrom0006/applyThrough0005 模式：真实 SQL + 真实 SHA256）。
func applyThrough0021(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql",
		"0004_cn35_guest_device.sql", "0005_cn35_inventory.sql",
		"0006_cn35_quest_progress.sql", "0007_cn35_quest_battle.sql",
		"0008_cn35_account_decks.sql", "0009_cn35_monster_size.sql",
		"0010_cn35_messages.sql", "0011_cn35_messages_seed.sql",
		"0012_cn35_gacha.sql", "0013_cn35_gacha_roster.sql",
		"0014_cn35_app_conf_versions.sql", "0015_cn35_presents_infos.sql",
		"0016_cn35_unit_helper.sql", "0017_cn35_items_resources.sql",
		"0018_cn35_low_intensity_waves.sql", "0019_cn35_tutorial_task_claims.sql",
		"0020_cn35_item_grants_and_material_goods.sql", "0021_cn35_item_bag_category_fix.sql",
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			t.Fatalf("exec %s: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err = tx.Exec(`INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, name, sum); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestUserExpCurveUpgradeFrom0021 0022 回填：旧占位 max_exp=100 的存量行按
// 等级纠正（8 级→1166、999 级→1178410），表内不变的等级（3 级=100）不受影响；
// exp/level 不被迁移改动；重放（再次 Open）幂等。
func TestUserExpCurveUpgradeFrom0021(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mercd35.sqlite3")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	applyThrough0021(t, db)
	for _, uid := range []string{"uid-exp3", "uid-exp8", "uid-exp999"} {
		if _, err = db.Exec(`INSERT INTO cn35_accounts
			(he_account_id,user_id,token,sk,user_name,account_name,password_hash,sdk_account_id,device_id,is_new_user,tutorial_finish,last_server_id,created_at,updated_at)
			VALUES (?,?,'tok','sk','旧玩家','','','',?,0,1,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
			uid, uid, uid); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO cn35_player_profiles
			(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,login_bonus_receivable_sec,guild_name_snapshot,created_at,updated_at)
			VALUES (?,?,?,100,20,20,'2026-01-01T00:00:00Z',300,300,'348',0,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
			uid, levelOf(uid), expOf(uid)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s := openStore(t, dir) // Open 内应用 0022
	assertLatestSchema(t, s)
	for _, tc := range []struct {
		uid                string
		wantLevel, wantMax int
		wantExpUnchanged   int
	}{
		{"uid-exp3", 3, 100, 30},
		{"uid-exp8", 8, 1166, 20},
		{"uid-exp999", 999, 1178410, 500},
	} {
		level, exp, maxExp := profileExp(t, s, tc.uid)
		if level != tc.wantLevel || maxExp != tc.wantMax {
			t.Fatalf("%s: level=%d max_exp=%d, want %d/%d", tc.uid, level, maxExp, tc.wantLevel, tc.wantMax)
		}
		if exp != tc.wantExpUnchanged {
			t.Fatalf("%s: exp=%d 被迁移改动, want %d", tc.uid, exp, tc.wantExpUnchanged)
		}
	}
	s.Close()

	// 幂等：0022 已登记，重开不再变更。
	s2 := openStore(t, dir)
	if _, exp, maxExp := profileExp(t, s2, "uid-exp8"); exp != 20 || maxExp != 1166 {
		t.Fatalf("重开后 uid-exp8: exp=%d max_exp=%d, want 20/1166", exp, maxExp)
	}
}

func levelOf(uid string) int {
	switch uid {
	case "uid-exp3":
		return 3
	case "uid-exp8":
		return 8
	default:
		return 999
	}
}

func expOf(uid string) int {
	switch uid {
	case "uid-exp3":
		return 30
	case "uid-exp8":
		return 20
	default:
		return 500
	}
}
