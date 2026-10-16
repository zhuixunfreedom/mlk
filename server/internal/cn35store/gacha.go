// gacha.go CN3.5 抽卡的 SQLite 持久化原语（GACHA350-03）。
//
// 事务不变式（与设计 §5 一致）：
//
//	读/建保底行 → 条件扣费（amount>=cost，RowsAffected==1）→ 逐抽结算
//	（INSERT 新卡 / UPDATE duplicated_count / 满觉醒补偿）→ 写操作记录
//	→ 逐抽记录 → 货币流水 → 提交。
//
// 任一失败整体回滚：不部分扣费、不部分发卡、不推进保底。
//
// 并发：SQLite 无 SELECT FOR UPDATE，行锁语义由连接层
// `_txlock=immediate`（database.go）+ MaxOpenConns(1) 提供；同一账号的
// 并发抽取因此被串行化。条件 UPDATE + RowsAffected 是第二道防线，
// cn35_player_balances 的 CHECK(amount>=0) 是第三道。
package cn35store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足抽卡写面契约。
var _ cn35game.GachaStore = (*Store)(nil)

// 领域类型别名：cn35game 是契约真源，存储层不另立一套形状，
// 避免两边字段漂移导致编译期断言失配。
type (
	GachaPityRow         = cn35game.GachaPity
	GachaDrawRow         = cn35game.GachaDraw
	GachaLedgerRow       = cn35game.GachaLedgerEntry
	GachaOperationInput  = cn35game.GachaSettlement
	GachaOperationRecord = cn35game.GachaOperationRecord
)

// 抽卡结算的哨兵错误。
//
// 与 cn35game 的哨兵**同值**（别名而非新错误）：领域层 handler 用
// errors.Is 判定时必须能命中，否则会把"余额不足"这类预期业务拒绝
// 误报成 500。存储层不另造一套平行错误，避免两边判定口径漂移。
var (
	// ErrGachaOperationConflict 同 (账号,操作键) 已存在但请求摘要不同。
	ErrGachaOperationConflict = cn35game.ErrGachaOperationConflict
	// ErrGachaInsufficient 余额不足，条件扣减未命中。
	ErrGachaInsufficient = cn35game.ErrGachaInsufficient
	// ErrGachaAccountMissing 账号不存在（归属校验失败）。
	ErrGachaAccountMissing = cn35game.ErrGachaOperationMissing
)

// GachaPity 读取某保底组的进度。无行时返回零值且 ok=false——
// 调用方据此在事务内懒初始化，**不**按现有持卡量反推历史。
func (s *Store) GachaPity(userID, group string) (GachaPityRow, bool, error) {
	var row GachaPityRow
	var guaranteed int
	err := s.db.QueryRow(`SELECT n4,n5,featured_guaranteed,rules_version
		FROM cn35_gacha_pity WHERE user_id=? AND pity_group=?`, userID, group).
		Scan(&row.N4, &row.N5, &guaranteed, &row.RulesVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return GachaPityRow{}, false, nil
	}
	if err != nil {
		return GachaPityRow{}, false, fmt.Errorf("cn35store: 读取保底失败: %w", err)
	}
	row.FeaturedGuaranteed = guaranteed == 1
	return row, true, nil
}

// GachaOperationByKey 读取已提交操作（幂等回放路径）。
// 返回的 record 为 nil 表示该键尚未提交。
func (s *Store) GachaOperationByKey(userID, key string) (*GachaOperationRecord, error) {
	var rec GachaOperationRecord
	err := s.db.QueryRow(`SELECT operation_id,operation_key,request_digest,pool_id,
		pity_group,rules_version,draw_count,currency,total_cost,result_json,created_at
		FROM cn35_gacha_operations WHERE user_id=? AND operation_key=?`, userID, key).
		Scan(&rec.OperationID, &rec.OperationKey, &rec.RequestDigest, &rec.PoolID,
			&rec.PityGroup, &rec.RulesVersion, &rec.DrawCount, &rec.Currency,
			&rec.TotalCost, &rec.ResultJSON, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读取抽卡操作失败: %w", err)
	}
	return &rec, nil
}

// GachaOperationByDigest 读取同参数指纹下最近一次已提交操作（重放判定
// 读面）。operation_key 含 nonce、每次结算唯一，重放判定只能按
// request_digest 定位"同一请求的最近一次结算"。返回 nil 表示无记录。
func (s *Store) GachaOperationByDigest(userID, digest string) (*GachaOperationRecord, error) {
	var rec GachaOperationRecord
	err := s.db.QueryRow(`SELECT operation_id,operation_key,request_digest,pool_id,
		pity_group,rules_version,draw_count,currency,total_cost,result_json,created_at
		FROM cn35_gacha_operations WHERE user_id=? AND request_digest=?
		ORDER BY operation_id DESC LIMIT 1`, userID, digest).
		Scan(&rec.OperationID, &rec.OperationKey, &rec.RequestDigest, &rec.PoolID,
			&rec.PityGroup, &rec.RulesVersion, &rec.DrawCount, &rec.Currency,
			&rec.TotalCost, &rec.ResultJSON, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读取抽卡操作失败: %w", err)
	}
	return &rec, nil
}

// GachaRecentOperations 读取账号最近的抽卡操作（记录页读面）。
// limit<=0 时使用默认 20，最大 100，防止读面被滥用。
func (s *Store) GachaRecentOperations(userID string, limit int) ([]GachaOperationRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT operation_id,operation_key,request_digest,pool_id,
		pity_group,rules_version,draw_count,currency,total_cost,result_json,created_at
		FROM cn35_gacha_operations WHERE user_id=?
		ORDER BY operation_id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读取抽卡记录失败: %w", err)
	}
	defer rows.Close()
	var out []GachaOperationRecord
	for rows.Next() {
		var rec GachaOperationRecord
		if err := rows.Scan(&rec.OperationID, &rec.OperationKey, &rec.RequestDigest,
			&rec.PoolID, &rec.PityGroup, &rec.RulesVersion, &rec.DrawCount,
			&rec.Currency, &rec.TotalCost, &rec.ResultJSON, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// GachaDraws 读取某操作的逐抽记录（按服务端原始顺序）。
func (s *Store) GachaDraws(userID string, operationID int64) ([]GachaDrawRow, error) {
	// 归属校验：操作必须属于该账号，阻止跨账号读取。
	var owner string
	err := s.db.QueryRow(`SELECT user_id FROM cn35_gacha_operations WHERE operation_id=?`,
		operationID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读取抽卡操作归属失败: %w", err)
	}
	if owner != userID {
		return nil, fmt.Errorf("cn35store: 操作 %d 不属于账号 %s", operationID, userID)
	}
	rows, err := s.db.Query(`SELECT unit_id,rarity_id,reason,newcomer,duplicated_delta,
		n4_after,n5_after,compensated_point FROM cn35_gacha_draws
		WHERE operation_id=? ORDER BY seq ASC`, operationID)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读取逐抽记录失败: %w", err)
	}
	defer rows.Close()
	var out []GachaDrawRow
	for rows.Next() {
		var d GachaDrawRow
		var newcomer int
		if err := rows.Scan(&d.UnitID, &d.RarityID, &d.Reason, &newcomer,
			&d.DuplicatedDelta, &d.N4After, &d.N5After, &d.CompensatedPoint); err != nil {
			return nil, err
		}
		d.Newcomer = newcomer == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

// SettleGacha 在**单个事务**内完成一次抽卡的全部持久化。
//
// 幂等语义（与迁移注释一致，勿误读）：本函数按 (账号, 操作键) 唯一约束
// 保证"同键只提交一次"。operation_key 由"同参指纹 + 结算 nonce"派生，
// 每次新结算唯一——它**不再承担重放定位**；调用方在进入本函数前应先查
// GachaOperationByDigest 按指纹判定重放（命中则直接回放 result_json，
// 不进入本函数）。本函数内的同键检查仅作并发冲突的防御兜底。
func (s *Store) SettleGacha(in GachaOperationInput) (*GachaOperationRecord, error) {
	if strings.TrimSpace(in.OperationKey) == "" {
		return nil, fmt.Errorf("cn35store: 抽卡操作键为空")
	}
	if in.DrawCount < 1 || len(in.Draws) != in.DrawCount {
		return nil, fmt.Errorf("cn35store: 抽卡次数 %d 与逐抽记录数 %d 不一致",
			in.DrawCount, len(in.Draws))
	}
	var rec GachaOperationRecord
	err := s.withTx(func(tx *sql.Tx) error {
		// 0) 归属校验：账号必须存在（外键之外的显式检查，
		//    便于返回明确错误而不是笼统的约束失败）。
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE user_id=?`,
			in.UserID).Scan(&exists); err != nil {
			return fmt.Errorf("cn35store: 抽卡账号校验失败: %w", err)
		}
		if exists != 1 {
			return ErrGachaAccountMissing
		}

		// 1) 幂等短路：同键已提交。
		var existingDigest, existingResult string
		var existingID int64
		err := tx.QueryRow(`SELECT request_digest,result_json,operation_id
			FROM cn35_gacha_operations WHERE user_id=? AND operation_key=?`,
			in.UserID, in.OperationKey).Scan(&existingDigest, &existingResult, &existingID)
		if err == nil {
			if existingDigest != in.RequestDigest {
				return ErrGachaOperationConflict
			}
			rec = GachaOperationRecord{
				OperationID: existingID, OperationKey: in.OperationKey,
				RequestDigest: existingDigest, ResultJSON: existingResult,
			}
			return nil // 回放：不扣费、不发卡、不推进保底。
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("cn35store: 抽卡幂等查询失败: %w", err)
		}

		// 2) 条件扣费：amount>=cost 且影响行数恰为 1，否则余额不足回滚。
		if in.TotalCost > 0 {
			res, err := tx.Exec(`UPDATE cn35_player_balances
				SET amount=amount-?,updated_at=?
				WHERE user_id=? AND currency=? AND amount>=?`,
				in.TotalCost, nowUTC(), in.UserID, in.Currency, in.TotalCost)
			if err != nil {
				return fmt.Errorf("cn35store: 抽卡扣费失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return ErrGachaInsufficient
			}
		}

		// 3) 写入操作记录，拿到 operation_id。
		opRes, err := tx.Exec(`INSERT INTO cn35_gacha_operations
			(user_id,operation_key,request_digest,pool_id,pity_group,rules_version,
			 draw_count,currency,unit_cost,total_cost,status,result_json,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,'committed',?,?)`,
			in.UserID, in.OperationKey, in.RequestDigest, in.PoolID, in.PityGroup,
			in.RulesVersion, in.DrawCount, in.Currency, in.UnitCost, in.TotalCost,
			in.ResultJSON, nowUTC())
		if err != nil {
			if isIDConflict(err) {
				// 并发同键：另一事务已提交，返回冲突让上层重读回放。
				return ErrGachaOperationConflict
			}
			return fmt.Errorf("cn35store: 写入抽卡操作失败: %w", err)
		}
		opID, err := opRes.LastInsertId()
		if err != nil {
			return fmt.Errorf("cn35store: 取抽卡操作 id 失败: %w", err)
		}

		// 4) 逐抽结算：新卡 INSERT，已有卡累加 duplicated_count。
		//    入库语义经 settleUnitCopyTx 与礼物领取共用（award_tx.go）。
		for i, d := range in.Draws {
			seq := i + 1
			if err := settleUnitCopyTx(tx, in.UserID, d.UnitID, d.DuplicatedDelta, d.Newcomer); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO cn35_gacha_draws
				(operation_id,seq,unit_id,rarity_id,reason,newcomer,duplicated_delta,
				 n4_after,n5_after,compensated_point,created_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				opID, seq, d.UnitID, d.RarityID, d.Reason, boolInt(d.Newcomer),
				d.DuplicatedDelta, d.N4After, d.N5After, d.CompensatedPoint,
				nowUTC()); err != nil {
				return fmt.Errorf("cn35store: 写入逐抽记录 %d 失败: %w", seq, err)
			}
		}

		// 5) 满觉醒补偿入账（与扣费同事务；creditBalanceTx 与礼物共用）。
		//    补偿素材是徽章（medal）：满觉重复卡的溢出兑换物是徽章而非金币，
		//    误写金币会让玩家拿不到徽章商店的兑换素材。
		compensation := 0
		for _, d := range in.Draws {
			compensation += d.CompensatedPoint
		}
		if compensation > 0 {
			if err := creditBalanceTx(tx, in.UserID, "medal", int64(compensation)); err != nil {
				return err
			}
		}

		// 6) 货币流水：消费 + 补偿，按 seq 唯一。
		for _, l := range in.Ledger {
			if _, err := tx.Exec(`INSERT INTO cn35_gacha_ledger
				(user_id,operation_id,seq,currency,delta,reason,balance_after,created_at)
				VALUES (?,?,?,?,?,?,?,?)`,
				in.UserID, opID, l.Seq, l.Currency, l.Delta, l.Reason,
				l.BalanceAfter, nowUTC()); err != nil {
				return fmt.Errorf("cn35store: 写入抽卡流水 seq=%d 失败: %w", l.Seq, err)
			}
		}

		// 7) 保底进度 upsert（仅参与保底的池）。
		if in.PityGroup != "" {
			if _, err := tx.Exec(`INSERT INTO cn35_gacha_pity
				(user_id,pity_group,n4,n5,featured_guaranteed,rules_version,created_at,updated_at)
				VALUES (?,?,?,?,?,?,?,?)
				ON CONFLICT(user_id,pity_group) DO UPDATE SET
					n4=excluded.n4, n5=excluded.n5,
					featured_guaranteed=excluded.featured_guaranteed,
					rules_version=excluded.rules_version,
					updated_at=excluded.updated_at`,
				in.UserID, in.PityGroup, in.Pity.N4, in.Pity.N5,
				boolInt(in.Pity.FeaturedGuaranteed), in.Pity.RulesVersion,
				nowUTC(), nowUTC()); err != nil {
				return fmt.Errorf("cn35store: 写入保底进度失败: %w", err)
			}
		}

		rec = GachaOperationRecord{
			OperationID: opID, OperationKey: in.OperationKey,
			RequestDigest: in.RequestDigest, PoolID: in.PoolID,
			PityGroup: in.PityGroup, RulesVersion: in.RulesVersion,
			DrawCount: in.DrawCount, Currency: in.Currency,
			TotalCost: in.TotalCost, ResultJSON: in.ResultJSON, CreatedAt: nowUTC(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// encodeGachaResult 把结果快照编码为可持久化 JSON。
// 使用标准编码即可——这张表只被服务端读取，不直接下发给客户端。
func encodeGachaResult(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("cn35store: 抽卡结果编码失败: %w", err)
	}
	return string(b), nil
}
