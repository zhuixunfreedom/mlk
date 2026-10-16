// monster_powerup_test.go 魔宠体型强化写面边界：
//   - 素材必须匹配魔宠属性与体型档位（种子/花蕾/花）；
//   - 上限边界 1.70：允许最后一步越界、只扣可执行步数，超限个体拒绝；
//   - is_rare=1（成本表未下发）拒绝且不扣素材；
//   - 正常步进落库。
package cn35store

import (
	"errors"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func insertPowerupMonster(t *testing.T, s *Store, uid, uuid, scale string, isRare int) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_monsters
		(user_id,uuid,monster_id,level,protected,created_at,updated_at,scale,real_scale,is_rare)
		VALUES (?,?,39,1,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',?,?,?)`,
		uid, uuid, scale, scale, isRare); err != nil {
		t.Fatal(err)
	}
}

func itemQty(t *testing.T, s *Store, uid, key string) int {
	t.Helper()
	var q int
	if err := s.DB().QueryRow(`SELECT quantity FROM cn35_player_items
		WHERE user_id=? AND item_key=?`, uid, key).Scan(&q); err != nil {
		t.Fatal(err)
	}
	return q
}

func monsterScale(t *testing.T, s *Store, uid, uuid string) string {
	t.Helper()
	var v string
	if err := s.DB().QueryRow(`SELECT real_scale FROM cn35_player_monsters
		WHERE user_id=? AND uuid=?`, uid, uuid).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// 素材 content_id：3=风之种子、8=风之花蕾、13=风之花（目录种子
// element=3、form=1/2/3）；卡卡鲁 monster_id=39 attr=风(3)。
const (
	windSeedID   = 3
	windBudID    = 8
	windFlowerID = 13
	windAttr     = 3
)

func TestMonsterPowerupStepAndDebit(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := questUserID(t, s, "dev-pw-"+randHex(6))
	insertPowerupMonster(t, s, uid, "m1", "1.000", 0)
	creditItem(t, s, uid, "powerup_item_3", 5)

	res, err := s.MonsterPowerup(uid, "m1", windAttr, windSeedID, 2)
	if err != nil {
		t.Fatalf("powerup: %v", err)
	}
	if res.RealScale != "1.040" || res.ChargedCount != 2 {
		t.Fatalf("结果错误: %+v", res)
	}
	if got := itemQty(t, s, uid, "powerup_item_3"); got != 3 {
		t.Fatalf("素材应剩 3, got %d", got)
	}
	if got := monsterScale(t, s, uid, "m1"); got != "1.040" {
		t.Fatalf("real_scale = %s, want 1.040", got)
	}
}

func TestMonsterPowerupItemMismatch(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := questUserID(t, s, "dev-pw-"+randHex(6))
	insertPowerupMonster(t, s, uid, "m1", "1.400", 0) // 1.40 档：花蕾
	creditItem(t, s, uid, "powerup_item_3", 5)        // 风之种子（错档位）
	creditItem(t, s, uid, "powerup_item_1", 5)        // 火之种子（错属性）
	creditItem(t, s, uid, "powerup_item_8", 5)        // 风之花蕾（正确）

	if _, err := s.MonsterPowerup(uid, "m1", windAttr, windSeedID, 1); !errors.Is(err, cn35game.ErrMonsterPowerupItem) {
		t.Fatalf("低档素材 want ErrMonsterPowerupItem, got %v", err)
	}
	if _, err := s.MonsterPowerup(uid, "m1", windAttr, 1, 1); !errors.Is(err, cn35game.ErrMonsterPowerupItem) {
		t.Fatalf("错属性素材 want ErrMonsterPowerupItem, got %v", err)
	}
	res, err := s.MonsterPowerup(uid, "m1", windAttr, windBudID, 1)
	if err != nil {
		t.Fatalf("花蕾应可用: %v", err)
	}
	if res.RealScale != "1.420" || res.ChargedCount != 1 {
		t.Fatalf("结果错误: %+v", res)
	}
}

func TestMonsterPowerupCapChargesOnlyExecutable(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := questUserID(t, s, "dev-pw-"+randHex(6))
	// 1.68→边界 1.70：客户端 while scale<=1.70 计数允许 2 步（至 1.72）。
	insertPowerupMonster(t, s, uid, "m1", "1.680", 0)
	creditItem(t, s, uid, "powerup_item_13", 5)

	res, err := s.MonsterPowerup(uid, "m1", windAttr, windFlowerID, 5)
	if err != nil {
		t.Fatalf("powerup: %v", err)
	}
	if res.RealScale != "1.720" || res.ChargedCount != 2 {
		t.Fatalf("应只执行 2 步收 2 件: %+v", res)
	}
	if got := itemQty(t, s, uid, "powerup_item_13"); got != 3 {
		t.Fatalf("素材应剩 3（只扣 2）, got %d", got)
	}
}

func TestMonsterPowerupOverCapRejected(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := questUserID(t, s, "dev-pw-"+randHex(6))
	// 战斗掉落超限个体（>1.70）天生不可强化。
	insertPowerupMonster(t, s, uid, "m1", "1.850", 0)
	insertPowerupMonster(t, s, uid, "m2", "1.720", 0)
	creditItem(t, s, uid, "powerup_item_13", 5)

	for _, uuid := range []string{"m1", "m2"} {
		if _, err := s.MonsterPowerup(uid, uuid, windAttr, windFlowerID, 1); !errors.Is(err, cn35game.ErrMonsterScaleCap) {
			t.Fatalf("%s want ErrMonsterScaleCap, got %v", uuid, err)
		}
	}
	if got := itemQty(t, s, uid, "powerup_item_13"); got != 5 {
		t.Fatalf("拒绝后不得扣素材, got %d", got)
	}
}

func TestMonsterPowerupRareRejected(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := questUserID(t, s, "dev-pw-"+randHex(6))
	insertPowerupMonster(t, s, uid, "m1", "1.000", 1)
	creditItem(t, s, uid, "powerup_item_3", 5)

	if _, err := s.MonsterPowerup(uid, "m1", windAttr, windSeedID, 1); !errors.Is(err, cn35game.ErrMonsterRarePowerup) {
		t.Fatalf("want ErrMonsterRarePowerup, got %v", err)
	}
	if got := itemQty(t, s, uid, "powerup_item_3"); got != 5 {
		t.Fatalf("拒绝后不得扣素材, got %d", got)
	}
	if got := monsterScale(t, s, uid, "m1"); got != "1.000" {
		t.Fatalf("拒绝后不得推进 Size, got %s", got)
	}
}

func TestMonsterPowerupNotOwned(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := questUserID(t, s, "dev-pw-"+randHex(6))
	if _, err := s.MonsterPowerup(uid, "ghost", windAttr, windSeedID, 1); !errors.Is(err, cn35game.ErrMonsterNotOwned) {
		t.Fatalf("want ErrMonsterNotOwned, got %v", err)
	}
}
