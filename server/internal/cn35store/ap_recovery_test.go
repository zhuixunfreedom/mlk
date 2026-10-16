package cn35store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func setAPState(t *testing.T, s *Store, userID string, ap, maxAP int, anchor time.Time) {
	t.Helper()
	stamp := anchor.UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET ap=?,max_ap=?,ap_updated_at=?,updated_at=? WHERE user_id=?`,
		ap, maxAP, stamp, stamp, userID); err != nil {
		t.Fatal(err)
	}
}

func readAPAnchor(t *testing.T, s *Store, userID string) (int, int, time.Time) {
	t.Helper()
	var ap, maxAP int
	var raw string
	if err := s.DB().QueryRow(`SELECT ap,max_ap,ap_updated_at FROM cn35_player_profiles WHERE user_id=?`, userID).
		Scan(&ap, &maxAP, &raw); err != nil {
		t.Fatal(err)
	}
	anchor, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	return ap, maxAP, anchor.UTC()
}

func fixedStoreClock(s *Store, now time.Time) {
	s.clock = func() time.Time { return now }
}

func TestAPRecoverySnapshotUsesClientIntegerSecondProjection(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := questUserID(t, s, "dev-ap-snapshot")
	anchor := time.Date(2026, 9, 23, 4, 0, 0, 900_000_000, time.UTC)
	setAPState(t, s, uid, 2, 20, anchor)

	// The client subtracts integer Unix seconds. Although only 299.2 wall-clock
	// seconds elapsed, the two integer endpoints differ by 300 seconds.
	fixedStoreClock(s, anchor.Add(299*time.Second+200*time.Millisecond))
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["ap"] != int64(3) && snap.User["ap"] != 3 {
		t.Fatalf("AP at integer-second client boundary: %#v", snap.User["ap"])
	}
	if snap.User["current_ap_diff"] != 0 {
		t.Fatalf("service snapshot must clear client-local projection: %#v", snap.User["current_ap_diff"])
	}
	gotAnchor := snap.User["int_ap_updated_at"]
	if gotAnchor != anchor.Add(300*time.Second).Unix() {
		t.Fatalf("wire anchor Unix seconds = %#v, want %d", gotAnchor, anchor.Add(300*time.Second).Unix())
	}
	// At response time, the returned AP already contains the one recovered
	// point and the moved anchor has no remaining client-side full tick.
	serverNow := anchor.Add(299*time.Second + 200*time.Millisecond).Unix()
	projectedTicks := (serverNow - gotAnchor.(int64)) / apRecoveryIntervalSeconds
	if projectedTicks != 0 {
		t.Fatalf("client would duplicate recovered AP: remaining ticks=%d", projectedTicks)
	}

	// A value strictly below the next integer-second interval stays unchanged.
	// skew 容差内（299s+3s>=300）已可提前入账，断言点退到 296s。
	anchor2 := time.Date(2026, 9, 23, 5, 0, 0, 100_000_000, time.UTC)
	setAPState(t, s, uid, 4, 20, anchor2)
	fixedStoreClock(s, anchor2.Add(296*time.Second+800*time.Millisecond))
	snap, err = s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["ap"] != int64(4) {
		t.Fatalf("AP recovered before integer second boundary: %#v", snap.User["ap"])
	}
}

func TestAPExhaustedStaysBlockedUntilRecoveryTickThenCanPay(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := questUserID(t, s, "dev-ap-exhausted")
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	setAPState(t, s, uid, 0, 20, now)

	// 299s 已落入 skew 提前入账窗，边界前断言取 296s。
	fixedStoreClock(s, now.Add(296*time.Second))
	snap, err := s.Snapshot(uid)
	if err != nil || snap.User["ap"] != int64(0) {
		t.Fatalf("exhausted AP before recovery: snapshot=%v err=%v", snap, err)
	}
	blocked := battleFixture(uid, 1, 1, 1)
	if err := s.OpenBattle(blocked, 86400); !errors.Is(err, cn35game.ErrBattleAP) {
		t.Fatalf("OpenBattle before recovery error=%v, want ErrBattleAP", err)
	}
	if ap, _, anchor := readAPAnchor(t, s, uid); ap != 0 || !anchor.Equal(now) {
		t.Fatalf("AP changed before next tick: AP=%d anchor=%s", ap, anchor)
	}

	fixedStoreClock(s, now.Add(300*time.Second))
	ready := battleFixture(uid, 1, 1, 1)
	if err := s.OpenBattle(ready, 86400); err != nil {
		t.Fatalf("OpenBattle at recovery tick: %v", err)
	}
	if ap, _, anchor := readAPAnchor(t, s, uid); ap != 1 || !anchor.Equal(now.Add(300*time.Second)) {
		t.Fatalf("AP after recovery tick: AP=%d anchor=%s", ap, anchor)
	}
	if _, err := s.PayBattleAP(uid, ready.BattleID); err != nil {
		t.Fatalf("PayBattleAP after recovery: %v", err)
	}
	if ap, _, _ := readAPAnchor(t, s, uid); ap != 0 {
		t.Fatalf("AP after use_ap debit=%d, want 0", ap)
	}
}

func TestAPRecoveryRemainderCapFutureAnchorAndAccountIsolation(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid1 := questUserID(t, s, "dev-ap-isolation-1")
	uid2 := questUserID(t, s, "dev-ap-isolation-2")
	now := time.Date(2026, 9, 23, 6, 0, 0, 250_000_000, time.UTC)
	fixedStoreClock(s, now)
	anchor := now.Add(-905 * time.Second)
	setAPState(t, s, uid1, 3, 20, anchor)
	future := now.Add(time.Second)
	setAPState(t, s, uid2, 1, 20, future)

	snap, err := s.Snapshot(uid1)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["ap"] != int64(6) {
		t.Fatalf("905-second catch-up should credit 3 AP: %#v", snap.User["ap"])
	}
	ap, _, moved := readAPAnchor(t, s, uid1)
	if ap != 6 || !moved.Equal(anchor.Add(900*time.Second)) {
		t.Fatalf("sub-interval remainder not preserved: AP=%d anchor=%s want=%s", ap, moved, anchor.Add(900*time.Second))
	}
	if snap.User["int_ap_updated_at"] != moved.Unix() || snap.User["current_ap_diff"] != 0 {
		t.Fatalf("wire fields mismatch persisted recovery: %#v", snap.User)
	}
	if gotAP, _, gotAnchor := readAPAnchor(t, s, uid2); gotAP != 1 || !gotAnchor.Equal(future) {
		t.Fatalf("recovery crossed account boundary: AP=%d anchor=%s", gotAP, gotAnchor)
	}

	// Reaching the cap drops overflow and anchors at this transaction's time.
	setAPState(t, s, uid1, 19, 20, now.Add(-905*time.Second))
	snap, err = s.Snapshot(uid1)
	if err != nil {
		t.Fatal(err)
	}
	ap, _, moved = readAPAnchor(t, s, uid1)
	if ap != 20 || !moved.Equal(now) || snap.User["current_ap_diff"] != 0 {
		t.Fatalf("cap transition: AP=%d anchor=%s current_ap_diff=%#v", ap, moved, snap.User["current_ap_diff"])
	}
	// A future anchor must not be rewritten or generate AP.
	setAPState(t, s, uid1, 7, 20, future)
	snap, err = s.Snapshot(uid1)
	if err != nil {
		t.Fatal(err)
	}
	ap, _, moved = readAPAnchor(t, s, uid1)
	if ap != 7 || !moved.Equal(future) {
		t.Fatalf("future anchor unexpectedly recovered: AP=%d anchor=%s", ap, moved)
	}
}

func TestAPRecoveryCommitsOnInsufficientBattleAndPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mercd35.sqlite3")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	uid := questUserID(t, s, "dev-ap-restart")
	now := time.Date(2026, 9, 23, 7, 0, 0, 0, time.UTC)
	fixedStoreClock(s, now)
	anchor := now.Add(-306 * time.Second)
	setAPState(t, s, uid, 1, 20, anchor)

	// One recovered point leaves AP=2, still below cost=3. Recovery must commit
	// while no battle session is created.（elapsed 306+skew3=309→恰好 1 tick）
	sess := battleFixture(uid, 1, 1, 3)
	if err := s.OpenBattle(sess, 86400); !errors.Is(err, cn35game.ErrBattleAP) {
		t.Fatalf("OpenBattle error=%v, want ErrBattleAP", err)
	}
	if ap, _, gotAnchor := readAPAnchor(t, s, uid); ap != 2 || !gotAnchor.Equal(anchor.Add(300*time.Second)) {
		t.Fatalf("insufficient precheck rolled recovery back: AP=%d anchor=%s", ap, gotAnchor)
	}
	if _, err := s.BattleSession(uid, sess.BattleID); !errors.Is(err, cn35game.ErrBattleNotFound) {
		t.Fatalf("insufficient AP created session: %v", err)
	}

	// The same commit-on-insufficient rule applies to use_ap: a prepared
	// session remains prepared while partial natural recovery is persisted.
	uidDebit := questUserID(t, s, "dev-ap-insufficient-debit")
	setAPState(t, s, uidDebit, 3, 20, now)
	debitSession := battleFixture(uidDebit, 1, 1, 3)
	if err := s.OpenBattle(debitSession, 86400); err != nil {
		t.Fatalf("create debit session: %v", err)
	}
	setAPState(t, s, uidDebit, 1, 20, now.Add(-306*time.Second))
	if _, err := s.PayBattleAP(uidDebit, debitSession.BattleID); !errors.Is(err, cn35game.ErrBattleAP) {
		t.Fatalf("PayBattleAP error=%v, want ErrBattleAP", err)
	}
	if ap, _, gotAnchor := readAPAnchor(t, s, uidDebit); ap != 2 || !gotAnchor.Equal(now.Add(-6*time.Second)) {
		t.Fatalf("insufficient debit rolled recovery back: AP=%d anchor=%s", ap, gotAnchor)
	}
	if gotStatus := battleStatus(t, s, debitSession.BattleID); gotStatus != cn35game.BattlePrepared {
		t.Fatalf("insufficient debit changed session state: %s", gotStatus)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fixedStoreClock(s, now)
	if ap, _, gotAnchor := readAPAnchor(t, s, uid); ap != 2 || !gotAnchor.Equal(anchor.Add(300*time.Second)) {
		t.Fatalf("restart lost AP state: AP=%d anchor=%s", ap, gotAnchor)
	}
	fixedStoreClock(s, now.Add(300*time.Second))
	snap, err := s.Snapshot(uid)
	if err != nil || snap.User["ap"] != int64(3) {
		t.Fatalf("post-restart next tick: snapshot=%v err=%v", snap, err)
	}
}

func TestAPDebitResetsFullAnchorAndConcurrentPaymentsCannotOverdraw(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := questUserID(t, s, "dev-ap-debit")
	now := time.Date(2026, 9, 23, 8, 0, 0, 600_000_000, time.UTC)
	fixedStoreClock(s, now)
	setAPState(t, s, uid, 20, 20, now.Add(-24*time.Hour))

	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatal(err)
	}
	if ap, _, anchor := readAPAnchor(t, s, uid); ap != 18 || !anchor.Equal(now) {
		t.Fatalf("debit from full AP must reset anchor: AP=%d anchor=%s", ap, anchor)
	}
	fixedStoreClock(s, now.Add(296*time.Second))
	if snap, err := s.Snapshot(uid); err != nil || snap.User["ap"] != int64(18) {
		t.Fatalf("296-second post-debit state: snapshot=%v err=%v", snap, err)
	}
	fixedStoreClock(s, now.Add(300*time.Second))
	if snap, err := s.Snapshot(uid); err != nil || snap.User["ap"] != int64(19) {
		t.Fatalf("300-second post-debit state: snapshot=%v err=%v", snap, err)
	}

	// Two prepared sessions can pass execute-time precheck against the same AP;
	// serialized conditional debits must allow only one to spend the last point.
	uid2 := questUserID(t, s, "dev-ap-race")
	fixedStoreClock(s, now)
	setAPState(t, s, uid2, 1, 20, now)
	s1, s2 := battleFixture(uid2, 1, 1, 1), battleFixture(uid2, 2, 1, 1)
	if err := s.OpenBattle(s1, 86400); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenBattle(s2, 86400); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{s1.BattleID, s2.BattleID} {
		wg.Add(1)
		go func(bid string) {
			defer wg.Done()
			_, err := s.PayBattleAP(uid2, bid)
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	successes, insufficient := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, cn35game.ErrBattleAP):
			insufficient++
		default:
			t.Fatalf("concurrent PayBattleAP unexpected error: %v", err)
		}
	}
	if successes != 1 || insufficient != 1 {
		t.Fatalf("concurrent debit results: success=%d insufficient=%d", successes, insufficient)
	}
	if ap, _, _ := readAPAnchor(t, s, uid2); ap != 0 {
		t.Fatalf("concurrent debit AP=%d, want 0", ap)
	}
	prepared, paid := 0, 0
	for _, bid := range []string{s1.BattleID, s2.BattleID} {
		switch battleStatus(t, s, bid) {
		case cn35game.BattlePrepared:
			prepared++
		case cn35game.BattleAPPaid:
			paid++
		}
	}
	if prepared != 1 || paid != 1 {
		t.Fatalf("race session states: prepared=%d paid=%d", prepared, paid)
	}
}

func TestSnapshotMayObserveBeforeOrAfterConcurrentDebitButNeverTornAPAnchor(t *testing.T) {
	s := openStore(t, t.TempDir())
	now := time.Date(2026, 9, 23, 9, 0, 0, 600_000_000, time.UTC)
	fixedStoreClock(s, now)

	for i := 0; i < 16; i++ {
		uid := questUserID(t, s, fmt.Sprintf("dev-ap-snapshot-race-%d", i))
		setAPState(t, s, uid, 20, 20, now)
		sess := battleFixture(uid, 1, 1, 1)
		if err := s.OpenBattle(sess, 86400); err != nil {
			t.Fatal(err)
		}
		// The next snapshot catches up 2 AP to 4 and advances the anchor by
		// 600s. A concurrent use_ap then atomically changes the row to AP=3,
		// anchor=now. Snapshot is allowed to return either complete row.
		setAPState(t, s, uid, 2, 20, now.Add(-605*time.Second))
		start := make(chan struct{})
		type snapshotResult struct {
			snapshot *cn35game.PlayerSnapshot
			err      error
		}
		snapCh := make(chan snapshotResult, 1)
		payCh := make(chan error, 1)
		go func() {
			<-start
			snap, err := s.Snapshot(uid)
			snapCh <- snapshotResult{snapshot: snap, err: err}
		}()
		go func() {
			<-start
			_, err := s.PayBattleAP(uid, sess.BattleID)
			payCh <- err
		}()
		close(start)
		gotSnap := <-snapCh
		if err := <-payCh; err != nil {
			t.Fatalf("concurrent PayBattleAP: %v", err)
		}
		if gotSnap.err != nil {
			t.Fatalf("concurrent Snapshot: %v", gotSnap.err)
		}
		ap := gotSnap.snapshot.User["ap"].(int64)
		anchorUnix := gotSnap.snapshot.User["int_ap_updated_at"].(int64)
		beforePayAP, beforePayAnchor := int64(4), now.Add(-5*time.Second).Unix()
		afterPayAP, afterPayAnchor := int64(3), now.Unix()
		if (ap != beforePayAP || anchorUnix != beforePayAnchor) &&
			(ap != afterPayAP || anchorUnix != afterPayAnchor) {
			t.Fatalf("Snapshot returned torn AP/anchor pair: AP=%d anchor=%d", ap, anchorUnix)
		}
	}
}
