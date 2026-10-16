// unit_ops.go P0 写面（2026-09-28 Note）：同伴队长/助战/保护位持久化。
//
// 事务约定（与 gacha.go 相同）：所有校验与写入在单个 withTx 内完成；
// 归属判定一律以 (user_id, unit_id) 库存行存在为准，跨账号请求与未持有
// 归一为 cn35game.ErrUnitNotOwned——不区分"单位不存在"与"他人持有"。
//
// leader/helper 两字段独立写入（客户端取证）：UserUnitDetail.cs:346-360
// 对 leader_unit_id 与 helper_unit_id 各自独立比较渲染，无互斥证据——
// 同一 unit_id 可同时占据两槽位。helper 空位编码为 ”（0016 允许、
// 空则不下发该键）；leader 空位不存在于正常流程，0003 的 CHECK 要求
// leader_unit_id 非空。
package cn35store

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足同伴写面契约。
var _ cn35game.UnitOpsStore = (*Store)(nil)

// ownUnitTx 校验会话账号持有该 unit_id 模板行（防跨账号写他人库存）。
func ownUnitTx(tx *sql.Tx, userID string, unitID int) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_units
		WHERE user_id=? AND unit_id=?`, userID, unitID).Scan(&n); err != nil {
		return fmt.Errorf("cn35store: 单位归属校验失败: %w", err)
	}
	if n != 1 {
		return cn35game.ErrUnitNotOwned
	}
	return nil
}

// SetLeaderUnitID 写 cn35_player_profiles.leader_unit_id（与 helper 独立，
// 不清对面槽位）。
func (s *Store) SetLeaderUnitID(userID string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := ownUnitTx(tx, userID, unitID); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE cn35_player_profiles
			SET leader_unit_id=?,updated_at=?
			WHERE user_id=?`, strconv.Itoa(unitID), nowUTC(), userID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入 leader_unit_id 失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: leader_unit_id 写入未命中账号 user_id=%s", userID)
		}
		return nil
	})
}

// SetHelperUnitID 写 cn35_player_profiles.helper_unit_id（0016 增列，
// 与 leader 独立，不清对面槽位）。
func (s *Store) SetHelperUnitID(userID string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := ownUnitTx(tx, userID, unitID); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE cn35_player_profiles
			SET helper_unit_id=?,updated_at=?
			WHERE user_id=?`, strconv.Itoa(unitID), nowUTC(), userID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入 helper_unit_id 失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: helper_unit_id 写入未命中账号 user_id=%s", userID)
		}
		return nil
	})
}

// SetUnitProtected 写 cn35_player_units.protected（归属条件在 UPDATE 内：
// 未持有即 0 行命中 → ErrUnitNotOwned）。
func (s *Store) SetUnitProtected(userID string, unitID int, protected bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE cn35_player_units
			SET protected=?,updated_at=? WHERE user_id=? AND unit_id=?`,
			boolInt(protected), nowUTC(), userID, unitID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入单位保护位失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrUnitNotOwned
		}
		return nil
	})
}
