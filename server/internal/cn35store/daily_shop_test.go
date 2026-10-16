// daily_shop_test.go SHOP350-01：每日商店领取台账事务与周期限购。
package cn35store

import (
	"errors"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestDailyShopClaimTx(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	r, _ := s.QuickLogin("dev-daily-shop")
	uid, _, _ := s.ChooseServer(r.HeAccountID, r.Token, "1")

	// 首领：coin+100，台账计数 1。
	bal, err := s.ClaimDailyPack(uid, "daily_free_diamond", "2026-10-03", 100, 1)
	if err != nil {
		t.Fatalf("首领失败: %v", err)
	}
	if bal != 100 {
		t.Fatalf("入账后余额 = %d, want 100", bal)
	}
	claims, err := s.DailyShopClaims(uid, "2026-10-03")
	if err != nil {
		t.Fatal(err)
	}
	if claims["daily_free_diamond"] != 1 {
		t.Fatalf("claims = %v", claims)
	}

	// 同周期再领 → ErrDailyShopClaimed，余额不重复入账。
	if _, err = s.ClaimDailyPack(uid, "daily_free_diamond", "2026-10-03", 100, 1); !errors.Is(err, cn35game.ErrDailyShopClaimed) {
		t.Fatalf("同周期再领应报已领: %v", err)
	}
	if bal, _ := s.ClaimDailyPack(uid, "daily_free_diamond", "2026-10-04", 100, 1); bal != 200 {
		t.Fatalf("次周期领取后余额 = %d, want 200", bal)
	}
	// 其他账号不受占用影响。
	r2, _ := s.QuickLogin("dev-daily-shop-2")
	uid2, _, _ := s.ChooseServer(r2.HeAccountID, r2.Token, "1")
	if bal, err = s.ClaimDailyPack(uid2, "daily_free_diamond", "2026-10-03", 100, 1); err != nil || bal != 100 {
		t.Fatalf("另一账号领取 = %d,%v", bal, err)
	}
	// 非法参数拒绝。
	if _, err = s.ClaimDailyPack(uid, "daily_free_diamond", "2026-10-05", 0, 1); !errors.Is(err, cn35game.ErrInvalidRequest) {
		t.Fatalf("coin=0 应拒绝: %v", err)
	}
}
