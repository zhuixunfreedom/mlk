// balance_grant_test.go GACHA350 验收：发放到位、前值留档、幂等、
// 他账号隔离、白名单拒绝、不存在账号拒绝、负值拒绝。
package cn35store

import (
	"errors"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestGrantBalancesSetAndIdempotent(t *testing.T) {
	s := openStore(t, t.TempDir())
	r, _ := s.QuickLogin("dev-grant-bal")
	uid, _, _ := s.ChooseServer(r.HeAccountID, r.Token, "1")

	res, err := s.GrantBalances(uid, map[string]int64{"coin": 999999, "gacha_point": 999999})
	if err != nil {
		t.Fatalf("GrantBalances: %v", err)
	}
	if res.Before["coin"] != 0 || res.Before["gacha_point"] != 302 {
		t.Fatalf("before 应为种子值 0/302: %+v", res.Before)
	}
	if res.After["coin"] != 999999 || res.After["gacha_point"] != 999999 {
		t.Fatalf("after 应为 999999/999999: %+v", res.After)
	}
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["gacha_point"] != int64(999999) ||
		snap.UserCoin["amount"] != int64(999999) {
		t.Fatalf("snapshot 未反映发放: gp=%v coin=%v",
			snap.User["gacha_point"], snap.UserCoin["amount"])
	}
	// 幂等：重复发放前值=999999，后值不变。
	res2, err := s.GrantBalances(uid, map[string]int64{"coin": 999999, "gacha_point": 999999})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Before["coin"] != 999999 || res2.After["coin"] != 999999 {
		t.Fatalf("重复发放不幂等: %+v", res2)
	}
}

func TestGrantBalancesIsolatedAndValidated(t *testing.T) {
	s := openStore(t, t.TempDir())
	r1, _ := s.QuickLogin("dev-grant-A")
	uid1, _, _ := s.ChooseServer(r1.HeAccountID, r1.Token, "1")
	r2, _ := s.QuickLogin("dev-grant-B")
	uid2, _, _ := s.ChooseServer(r2.HeAccountID, r2.Token, "1")

	if _, err := s.GrantBalances(uid1, map[string]int64{"coin": 999999}); err != nil {
		t.Fatal(err)
	}
	// 他账号不受影响。
	snap2, err := s.Snapshot(uid2)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.UserCoin["amount"] != int64(0) || snap2.User["gacha_point"] != int64(302) {
		t.Fatalf("他账号被波及: %+v", snap2.UserCoin)
	}
	// 白名单/负值/空 user/不存在账号逐项拒绝。
	if _, err := s.GrantBalances(uid1, map[string]int64{"diamond": 1}); err == nil {
		t.Fatal("未知货币应拒绝")
	}
	if _, err := s.GrantBalances(uid1, map[string]int64{"coin": -5}); err == nil {
		t.Fatal("负值应拒绝")
	}
	if _, err := s.GrantBalances("", map[string]int64{"coin": 1}); err == nil {
		t.Fatal("空 user 应拒绝")
	}
	if _, err := s.GrantBalances(uid1, map[string]int64{}); err == nil {
		t.Fatal("空发放集应拒绝")
	}
	if _, err := s.GrantBalances("no-such-user", map[string]int64{"coin": 1}); !errors.Is(err, cn35game.ErrNoAccount) {
		t.Fatalf("不存在账号应 ErrNoAccount: %v", err)
	}
}
