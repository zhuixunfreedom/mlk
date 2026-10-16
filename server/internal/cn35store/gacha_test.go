// gacha_test.go GACHA350-03 抽卡持久化与原子结算测试。
//
// 覆盖计划要求的退出条件：失败注入无部分扣费/发卡，重复与并发不多结算，
// 升级保留既有资产，事务外序列化失败不能重新结算。
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

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// gachaFixture 建库、建号并返回 (store, userID, 初始 gacha_point)。
func gachaFixture(t *testing.T) (*Store, string, int) {
	t.Helper()
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "gacha-fixture")
	// 建号种子只有 302 gacha_point，不足以覆盖多抽测试；这里显式充值，
	// 让"扣费金额"断言有确定基线（需要稀缺余额的测试自行设更小值）。
	setBalance(t, s, uid, "gacha_point", 100000)
	setBalance(t, s, uid, "coin", 100000)
	var point int
	if err := s.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&point); err != nil {
		t.Fatalf("读取初始 gacha_point 失败: %v", err)
	}
	return s, uid, point
}

// setBalance 直接设置余额（测试夹具，绕过业务写面）。
func setBalance(t *testing.T, s *Store, uid, currency string, amount int) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_balances
		(user_id,currency,amount,updated_at) VALUES (?,?,?,'2026-01-01T00:00:00Z')
		ON CONFLICT(user_id,currency) DO UPDATE SET amount=excluded.amount`, uid, currency, amount); err != nil {
		t.Fatalf("设置余额 %s 失败: %v", currency, err)
	}
}

// balance 读取余额。
func balance(t *testing.T, s *Store, uid, currency string) int {
	t.Helper()
	var amount int
	if err := s.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency=?`, uid, currency).Scan(&amount); err != nil {
		t.Fatalf("读取余额 %s 失败: %v", currency, err)
	}
	return amount
}

// drawCount 统计库存行数。
func drawCount(t *testing.T, s *Store, uid string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_player_units WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计库存失败: %v", err)
	}
	return n
}

// baseSettlement 构造一次基础结算输入。
func baseSettlement(uid, key string, cost int, draws ...GachaDrawRow) GachaOperationInput {
	return GachaOperationInput{
		UserID: uid, OperationKey: key, RequestDigest: "digest-" + key,
		PoolID: "3", PityGroup: "", RulesVersion: "test-v1",
		DrawCount: len(draws), Currency: "gacha_point",
		UnitCost: cost, TotalCost: cost * len(draws),
		ResultJSON: `{"temp_gacha_result":[]}`,
		Draws:      draws,
		Ledger: []GachaLedgerRow{
			{Seq: 0, Currency: "gacha_point", Delta: -(cost * len(draws)),
				Reason: "gacha_consume", BalanceAfter: 0},
		},
	}
}

// TestGachaSettleHappyPath 新卡入库 + 扣费 + 逐抽记录 + 流水，一次提交。
func TestGachaSettleHappyPath(t *testing.T) {
	s, uid, point := gachaFixture(t)
	in := baseSettlement(uid, "op-1", 200,
		GachaDrawRow{UnitID: 101, RarityID: 20, Reason: "no_pity_pool", Newcomer: true},
		GachaDrawRow{UnitID: 102, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	rec, err := s.SettleGacha(in)
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	if rec.OperationID == 0 {
		t.Fatal("未返回 operation_id")
	}
	if got := balance(t, s, uid, "gacha_point"); got != point-400 {
		t.Fatalf("扣费后余额=%d want %d", got, point-400)
	}
	if got := drawCount(t, s, uid); got != 2 {
		t.Fatalf("库存行数=%d want 2", got)
	}
	draws, err := s.GachaDraws(uid, rec.OperationID)
	if err != nil {
		t.Fatalf("读取逐抽记录失败: %v", err)
	}
	if len(draws) != 2 {
		t.Fatalf("逐抽记录数=%d want 2", len(draws))
	}
	if draws[0].UnitID != 101 || draws[1].UnitID != 102 {
		t.Fatalf("逐抽顺序错误: %+v", draws)
	}
	var ledger int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_ledger WHERE user_id=?`, uid).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 1 {
		t.Fatalf("流水条数=%d want 1", ledger)
	}
}

// TestGachaSettleReplaySameKey 同键同摘要重放原结果，不再扣费不再发卡。
func TestGachaSettleReplaySameKey(t *testing.T) {
	s, uid, point := gachaFixture(t)
	in := baseSettlement(uid, "op-replay", 200,
		GachaDrawRow{UnitID: 201, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	first, err := s.SettleGacha(in)
	if err != nil {
		t.Fatalf("首次结算失败: %v", err)
	}
	second, err := s.SettleGacha(in)
	if err != nil {
		t.Fatalf("重放失败: %v", err)
	}
	if first.OperationID != second.OperationID {
		t.Fatalf("重放返回了新操作 id: %d vs %d", first.OperationID, second.OperationID)
	}
	if second.ResultJSON != first.ResultJSON {
		t.Fatal("重放未返回原结果快照")
	}
	if got := balance(t, s, uid, "gacha_point"); got != point-200 {
		t.Fatalf("重放重复扣费: 余额=%d want %d", got, point-200)
	}
	if got := drawCount(t, s, uid); got != 1 {
		t.Fatalf("重放重复发卡: 库存=%d want 1", got)
	}
}

// TestGachaSettleSameKeyDifferentDigest 同键异参必须拒绝。
func TestGachaSettleSameKeyDifferentDigest(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	in := baseSettlement(uid, "op-conflict", 200,
		GachaDrawRow{UnitID: 301, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	if _, err := s.SettleGacha(in); err != nil {
		t.Fatalf("首次结算失败: %v", err)
	}
	in.RequestDigest = "digest-changed"
	_, err := s.SettleGacha(in)
	if !errors.Is(err, cn35game.ErrGachaOperationConflict) {
		t.Fatalf("同键异参未拒绝: err=%v", err)
	}
}

// TestGachaSettleInsufficientRollsBack 余额不足整体回滚：
// 不扣费、不发卡、不留操作记录与流水。
func TestGachaSettleInsufficientRollsBack(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	setBalance(t, s, uid, "gacha_point", 100) // 只够 0 次 200，请求 1 次
	in := baseSettlement(uid, "op-poor", 200,
		GachaDrawRow{UnitID: 401, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	_, err := s.SettleGacha(in)
	if !errors.Is(err, cn35game.ErrGachaInsufficient) {
		t.Fatalf("余额不足未返回 ErrGachaInsufficient: %v", err)
	}
	if got := balance(t, s, uid, "gacha_point"); got != 100 {
		t.Fatalf("失败后余额被改动: %d want 100", got)
	}
	if got := drawCount(t, s, uid); got != 0 {
		t.Fatalf("失败后仍发了卡: 库存=%d want 0", got)
	}
	var ops, ledger int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_operations WHERE user_id=?`, uid).Scan(&ops)
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_ledger WHERE user_id=?`, uid).Scan(&ledger)
	if ops != 0 || ledger != 0 {
		t.Fatalf("失败后留下记录: ops=%d ledger=%d", ops, ledger)
	}
}

// TestGachaSettleUnknownAccountRejected 账号不存在时拒绝且无副作用。
func TestGachaSettleUnknownAccountRejected(t *testing.T) {
	s, _, _ := gachaFixture(t)
	in := baseSettlement("not-exist-user", "op-ghost", 200,
		GachaDrawRow{UnitID: 501, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	_, err := s.SettleGacha(in)
	if !errors.Is(err, ErrGachaAccountMissing) {
		t.Fatalf("未知账号未拒绝: %v", err)
	}
}

// TestGachaSettleDuplicateDrawAccumulates 同模板多次只累加觉醒，
// 且**不覆盖** level/protected。
func TestGachaSettleDuplicateDrawAccumulates(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	// 预置一张已有卡：等级 7、受保护。
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_units
		(user_id,unit_id,level,duplicated_count,protected,newcomer,monster_uuid,created_at,updated_at)
		VALUES (?,?,7,2,1,0,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, uid, 601); err != nil {
		t.Fatal(err)
	}
	in := baseSettlement(uid, "op-dup", 200,
		GachaDrawRow{UnitID: 601, RarityID: 40, Reason: "no_pity_pool", Newcomer: false, DuplicatedDelta: 1},
		GachaDrawRow{UnitID: 601, RarityID: 40, Reason: "no_pity_pool", Newcomer: false, DuplicatedDelta: 1},
	)
	if _, err := s.SettleGacha(in); err != nil {
		t.Fatalf("重复卡结算失败: %v", err)
	}
	var level, dup, prot, newcomer int
	if err := s.DB().QueryRow(`SELECT level,duplicated_count,protected,newcomer
		FROM cn35_player_units WHERE user_id=? AND unit_id=?`, uid, 601).
		Scan(&level, &dup, &prot, &newcomer); err != nil {
		t.Fatal(err)
	}
	if dup != 4 {
		t.Fatalf("觉醒计数=%d want 4（2+1+1）", dup)
	}
	if level != 7 {
		t.Fatalf("等级被覆盖: %d want 7", level)
	}
	if prot != 1 {
		t.Fatalf("保护状态被覆盖: %d want 1", prot)
	}
	if newcomer != 0 {
		t.Fatalf("newcomer 未被清除: %d", newcomer)
	}
	// 同模板只应有一行（PK 保证）。
	var rows int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_player_units WHERE user_id=? AND unit_id=?`, uid, 601).Scan(&rows)
	if rows != 1 {
		t.Fatalf("同模板行数=%d want 1", rows)
	}
}

// TestGachaSettleCompensationCredited 满觉醒补偿入账与扣费同事务。
func TestGachaSettleCompensationCredited(t *testing.T) {
	s, uid, point := gachaFixture(t)
	in := baseSettlement(uid, "op-comp", 200,
		GachaDrawRow{UnitID: 701, RarityID: 40, Reason: "no_pity_pool",
			Newcomer: false, DuplicatedDelta: 0, CompensatedPoint: 200},
	)
	in.Ledger = append(in.Ledger, GachaLedgerRow{
		Seq: 1, Currency: "medal", Delta: 200,
		Reason: "gacha_duplicate_compensation", BalanceAfter: 0,
	})
	setBalance(t, s, uid, "medal", 0)
	if _, err := s.SettleGacha(in); err != nil {
		t.Fatalf("补偿结算失败: %v", err)
	}
	// 补偿素材是徽章：medal +200，金币只被扣费 -200。
	if got := balance(t, s, uid, "medal"); got != 200 {
		t.Fatalf("徽章补偿未入账: medal=%d want 200", got)
	}
	if got := balance(t, s, uid, "gacha_point"); got != point-200 {
		t.Fatalf("金币余额异常: %d want %d", got, point-200)
	}
	var n int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_ledger WHERE user_id=?`, uid).Scan(&n)
	if n != 2 {
		t.Fatalf("流水条数=%d want 2", n)
	}
}

// TestGachaPityPersistedAndRead 保底进度落库并可按组读回。
func TestGachaPityPersistedAndRead(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	in := baseSettlement(uid, "op-pity", 200,
		GachaDrawRow{UnitID: 801, RarityID: 40, Reason: "sr_pity", Newcomer: true, N4After: 0, N5After: 7},
	)
	in.PoolID = "2"
	in.PityGroup = "cn350-diamond-regular"
	in.Currency = "coin"
	in.UnitCost = 5
	in.TotalCost = 5
	in.Pity = GachaPityRow{N4: 0, N5: 7, FeaturedGuaranteed: true, RulesVersion: "cn350-gacha-rules-v1"}
	in.Ledger = []GachaLedgerRow{{Seq: 0, Currency: "coin", Delta: -5, Reason: "gacha_consume", BalanceAfter: 0}}
	setBalance(t, s, uid, "coin", 50)

	if _, err := s.SettleGacha(in); err != nil {
		t.Fatalf("保底结算失败: %v", err)
	}
	row, ok, err := s.GachaPity(uid, "cn350-diamond-regular")
	if err != nil {
		t.Fatalf("读取保底失败: %v", err)
	}
	if !ok {
		t.Fatal("保底行未落库")
	}
	if row.N5 != 7 || row.N4 != 0 || !row.FeaturedGuaranteed {
		t.Fatalf("保底内容错误: %+v", row)
	}
	if row.RulesVersion != "cn350-gacha-rules-v1" {
		t.Fatalf("规则版本=%q", row.RulesVersion)
	}

	// 第二次结算应更新同一行而不是新增。
	in2 := baseSettlement(uid, "op-pity-2", 200,
		GachaDrawRow{UnitID: 802, RarityID: 40, Reason: "normal", Newcomer: true, N4After: 0, N5After: 0},
	)
	in2.PoolID = "2"
	in2.PityGroup = "cn350-diamond-regular"
	in2.Currency = "coin"
	in2.UnitCost = 5
	in2.TotalCost = 5
	in2.Pity = GachaPityRow{N4: 0, N5: 0, FeaturedGuaranteed: false, RulesVersion: "cn350-gacha-rules-v1"}
	in2.Ledger = []GachaLedgerRow{{Seq: 0, Currency: "coin", Delta: -5, Reason: "gacha_consume", BalanceAfter: 0}}
	if _, err := s.SettleGacha(in2); err != nil {
		t.Fatalf("第二次保底结算失败: %v", err)
	}
	var rows int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_pity WHERE user_id=?`, uid).Scan(&rows)
	if rows != 1 {
		t.Fatalf("保底行数=%d want 1（应按组 upsert）", rows)
	}
	row2, _, _ := s.GachaPity(uid, "cn350-diamond-regular")
	if row2.N5 != 0 || row2.FeaturedGuaranteed {
		t.Fatalf("保底未更新: %+v", row2)
	}
}

// TestGachaPityMissingReturnsZero 无保底行时返回 ok=false，
// 不得按现有持卡量反推历史。
func TestGachaPityMissingReturnsZero(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	row, ok, err := s.GachaPity(uid, "cn350-diamond-regular")
	if err != nil {
		t.Fatalf("读取保底失败: %v", err)
	}
	if ok {
		t.Fatal("未抽过卡的账号不应有保底行")
	}
	if row.N4 != 0 || row.N5 != 0 {
		t.Fatalf("零值保底被污染: %+v", row)
	}
}

// TestGachaDrawsRejectCrossAccount 跨账号读取逐抽记录必须拒绝。
func TestGachaDrawsRejectCrossAccount(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	in := baseSettlement(uid, "op-iso", 200,
		GachaDrawRow{UnitID: 901, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	rec, err := s.SettleGacha(in)
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	other := seedAccount(t, s, "gacha-other-1")
	if _, err := s.GachaDraws(other, rec.OperationID); err == nil {
		t.Fatal("跨账号读取逐抽记录未被拒绝")
	}
}

// TestGachaOperationIsolatedPerAccount 不同账号可用相同操作键互不影响。
func TestGachaOperationIsolatedPerAccount(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	other := seedAccount(t, s, "gacha-other-2")
	in1 := baseSettlement(uid, "same-key", 200,
		GachaDrawRow{UnitID: 1001, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	in2 := baseSettlement(other, "same-key", 200,
		GachaDrawRow{UnitID: 1002, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	r1, err := s.SettleGacha(in1)
	if err != nil {
		t.Fatalf("账号1结算失败: %v", err)
	}
	r2, err := s.SettleGacha(in2)
	if err != nil {
		t.Fatalf("账号2结算失败: %v", err)
	}
	if r1.OperationID == r2.OperationID {
		t.Fatal("不同账号的同名操作键被合并")
	}
	// 账号隔离：各持有一张卡。
	if got := drawCount(t, s, uid); got != 1 {
		t.Fatalf("账号1库存=%d want 1", got)
	}
	if got := drawCount(t, s, other); got != 1 {
		t.Fatalf("账号2库存=%d want 1", got)
	}
}

// TestGachaRecentOperationsOrdering 记录读面按时间倒序且遵守上限。
func TestGachaRecentOperationsOrdering(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	for i := 0; i < 3; i++ {
		in := baseSettlement(uid, "op-list-"+string(rune('a'+i)), 200,
			GachaDrawRow{UnitID: 1100 + i, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
		)
		if _, err := s.SettleGacha(in); err != nil {
			t.Fatalf("第%d次结算失败: %v", i, err)
		}
	}
	recs, err := s.GachaRecentOperations(uid, 0)
	if err != nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("记录数=%d want 3", len(recs))
	}
	if recs[0].OperationKey != "op-list-c" {
		t.Fatalf("首条记录=%q want op-list-c（应倒序）", recs[0].OperationKey)
	}
	// 上限钳制。
	recs2, err := s.GachaRecentOperations(uid, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs2) != 2 {
		t.Fatalf("limit=2 返回 %d 条", len(recs2))
	}
}

// TestGachaSettleRejectsInconsistentCount 次数与逐抽记录不一致必须拒绝。
func TestGachaSettleRejectsInconsistentCount(t *testing.T) {
	s, uid, _ := gachaFixture(t)
	in := baseSettlement(uid, "op-badcount", 200,
		GachaDrawRow{UnitID: 1201, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
	)
	in.DrawCount = 5 // 与 Draws 长度 1 不符
	if _, err := s.SettleGacha(in); err == nil {
		t.Fatal("次数与逐抽记录不一致未被拒绝")
	}
	in.DrawCount = 0
	if _, err := s.SettleGacha(in); err == nil {
		t.Fatal("次数为 0 未被拒绝")
	}
}

// TestGachaUpgradeFrom0011PreservesAssets 升级路径：从 0011 库升级到 0012
// 必须保留既有账号、余额与库存，且新表为空（不回填、不反推历史）。
func TestGachaUpgradeFrom0011PreservesAssets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite3")

	// 1) 手工回放 0001—0011，写入 SHA256 台账。
	old := openStoreUpTo(t, path, 11)
	uid := seedAccount(t, old, "gacha-upgrade")
	if _, err := old.DB().Exec(`INSERT INTO cn35_player_units
		(user_id,unit_id,level,duplicated_count,protected,newcomer,monster_uuid,created_at,updated_at)
		VALUES (?,?,5,3,1,0,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, uid, 1301); err != nil {
		t.Fatal(err)
	}
	if _, err := old.DB().Exec(`UPDATE cn35_player_balances SET amount=1234
		WHERE user_id=? AND currency='gacha_point'`, uid); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	// 2) 用当前二进制 Open（内部 Migrate 追加 0012）。
	s, err := Open(path)
	if err != nil {
		t.Fatalf("升级 Open 失败: %v", err)
	}
	defer s.Close()
	assertLatestSchema(t, s)

	// 3) 既有资产必须原样保留。
	var level, dup, prot int
	if err := s.DB().QueryRow(`SELECT level,duplicated_count,protected FROM cn35_player_units
		WHERE user_id=? AND unit_id=?`, uid, 1301).Scan(&level, &dup, &prot); err != nil {
		t.Fatalf("升级后读取库存失败: %v", err)
	}
	if level != 5 || dup != 3 || prot != 1 {
		t.Fatalf("升级改动了既有库存: level=%d dup=%d prot=%d", level, dup, prot)
	}
	if got := balance(t, s, uid, "gacha_point"); got != 1234 {
		t.Fatalf("升级改动了余额: %d want 1234", got)
	}

	// 4) 新表为空，且保底为"无记录"而不是按持卡量反推。
	var pity, ops int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_pity WHERE user_id=?`, uid).Scan(&pity)
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_operations WHERE user_id=?`, uid).Scan(&ops)
	if pity != 0 || ops != 0 {
		t.Fatalf("升级后新表非空: pity=%d ops=%d（不得按现有持卡量反推历史）", pity, ops)
	}
	if _, ok, err := s.GachaPity(uid, "cn350-diamond-regular"); err != nil || ok {
		t.Fatalf("升级后保底应为空: ok=%v err=%v", ok, err)
	}
}

// TestGachaConcurrentSettleSerialized 同账号并发结算同键：
// 只应有恰好一次真实扣费，其余走幂等回放或冲突拒绝。
func TestGachaConcurrentSettleSerialized(t *testing.T) {
	s, uid, point := gachaFixture(t)
	const workers = 4
	type result struct {
		rec *GachaOperationRecord
		err error
	}
	ch := make(chan result, workers)
	for i := 0; i < workers; i++ {
		go func() {
			in := baseSettlement(uid, "op-concurrent", 200,
				GachaDrawRow{UnitID: 1401, RarityID: 30, Reason: "no_pity_pool", Newcomer: true},
			)
			rec, err := s.SettleGacha(in)
			ch <- result{rec, err}
		}()
	}
	var ids []int64
	failures := 0
	for i := 0; i < workers; i++ {
		r := <-ch
		if r.err != nil {
			// 同键并发会有一方在 INSERT 处命中唯一约束，属预期的冲突拒绝。
			if errors.Is(r.err, cn35game.ErrGachaOperationConflict) {
				failures++
				continue
			}
			t.Fatalf("并发结算出现非预期错误: %v", r.err)
		}
		ids = append(ids, r.rec.OperationID)
	}
	// 恰好一次真实提交。
	if got := balance(t, s, uid, "gacha_point"); got != point-200 {
		t.Fatalf("并发导致重复扣费: 余额=%d want %d", got, point-200)
	}
	if got := drawCount(t, s, uid); got != 1 {
		t.Fatalf("并发导致重复发卡: 库存=%d want 1", got)
	}
	var ops int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_operations WHERE user_id=?`, uid).Scan(&ops)
	if ops != 1 {
		t.Fatalf("操作记录数=%d want 1（并发只提交一次）", ops)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("并发返回了多个 operation id: %v", ids)
		}
	}
	t.Logf("并发 %d 次：%d 次回放成功、%d 次冲突拒绝（均无副作用）", workers, len(ids), failures)
}

// openStoreUpTo 打开一个只迁移到指定编号的库（升级测试用）。
// 沿用 TestInventoryUpgradeFrom0004 的模式：真实 SQLite + 真实迁移文件 +
// 真实 SHA256 台账，不造内存桩。
func openStoreUpTo(t *testing.T, path string, upto int) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库失败: %v", err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("开启旧库事务失败: %v", err)
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatalf("建 schema_migrations 失败: %v", err)
	}
	names := []string{
		"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql",
		"0004_cn35_guest_device.sql", "0005_cn35_inventory.sql",
		"0006_cn35_quest_progress.sql", "0007_cn35_quest_battle.sql",
		"0008_cn35_account_decks.sql", "0009_cn35_monster_size.sql",
		"0010_cn35_messages.sql", "0011_cn35_messages_seed.sql",
	}
	for i, name := range names {
		if i >= upto {
			break
		}
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatalf("读取迁移 %s 失败: %v", name, err)
		}
		if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("执行迁移 %s 失败: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			name, sum); err != nil {
			t.Fatalf("写入台账 %s 失败: %v", name, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("提交旧库事务失败: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭旧库失败: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("升级 Open 失败: %v", err)
	}
	return s
}
