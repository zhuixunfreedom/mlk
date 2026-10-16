// award_tx.go 抽卡/礼物共用的 tx 内发奖原语（2026-09-28 P0 Note：礼物
// 入库语义必须复用抽卡结算，不另写第二套规则）。
//
// 抽出位置：原 gacha.go SettleGacha 步骤 4（单位入库）与步骤 5（余额入账）。
package cn35store

import (
	"database/sql"
	"fmt"
)

// settleUnitCopyTx 单次同伴入库原语：
//   - newcomer：INSERT 新行（level=1、duplicated_count=0、newcomer=1），
//     不触碰既有实例；
//   - delta>0：已有行 duplicated_count+delta 并清 newcomer，
//     **不覆盖** level/protected/monster_uuid/编队绑定（与抽卡语义一致）。
func settleUnitCopyTx(tx *sql.Tx, userID string, unitID, delta int, newcomer bool) error {
	if newcomer {
		if _, err := tx.Exec(`INSERT INTO cn35_player_units
			(user_id,unit_id,level,duplicated_count,protected,newcomer,monster_uuid,created_at,updated_at)
			VALUES (?,?,1,0,0,1,'',?,?)`,
			userID, unitID, nowUTC(), nowUTC()); err != nil {
			return fmt.Errorf("cn35store: 新单位 %d 入库失败: %w", unitID, err)
		}
		return nil
	}
	if delta <= 0 {
		return nil
	}
	res, err := tx.Exec(`UPDATE cn35_player_units
		SET duplicated_count=duplicated_count+?,newcomer=0,updated_at=?
		WHERE user_id=? AND unit_id=?`,
		delta, nowUTC(), userID, unitID)
	if err != nil {
		return fmt.Errorf("cn35store: 重复单位 %d 累加失败: %w", unitID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("cn35store: 重复单位 %d 不存在（结算与库存不一致）", unitID)
	}
	return nil
}

// 余额入账原语复用 battle.go 的 creditBalanceTx（UPSERT 形态，行缺失即建，
// 免 UPDATE→INSERT 两步竞态）；gacha 补偿与礼物余额共用同一定义。
