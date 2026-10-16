// monster_ops.go P0 写面（2026-09-28 Note）：魔宠装备/卸下/保护位持久化。
//
// 双向引用不变式（同一事务内维护，0005 已有两列）：
//
//	cn35_player_monsters.unit_id   ↔ cn35_player_units.monster_uuid
//
// 任何时刻一对一：装到目标同伴时，若该魔宠已挂在其他同伴上先清原宿主
// monster_uuid；若目标同伴已有其他魔宠先把那只魔宠 unit_id 置 NULL。
// 「换装即替换」是服务端重建语义——客户端确认文案（COMMON_INFO_OVERWITE_CONF）
// 仅提示覆盖，原服逐位行为未证。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足魔宠写面契约。
var _ cn35game.MonsterOpsStore = (*Store)(nil)

// monsterHostTx 取魔宠当前宿主（unit_id NULL=未装备）；不存在归一为 ErrMonsterNotOwned。
func monsterHostTx(tx *sql.Tx, userID, uuid string) (sql.NullInt64, error) {
	var cur sql.NullInt64
	err := tx.QueryRow(`SELECT unit_id FROM cn35_player_monsters
		WHERE user_id=? AND uuid=?`, userID, uuid).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return cur, cn35game.ErrMonsterNotOwned
	}
	if err != nil {
		return cur, fmt.Errorf("cn35store: 魔宠归属校验失败: %w", err)
	}
	return cur, nil
}

// EquipMonster /user_monsters/on：uuid 与 unit_id 都必须属本账号。
// 同事务内：清魔宠原宿主 → 卸目标同伴原魔宠 → 写两侧引用。
func (s *Store) EquipMonster(userID, uuid string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := ownUnitTx(tx, userID, unitID); err != nil {
			return err
		}
		cur, err := monsterHostTx(tx, userID, uuid)
		if err != nil {
			return err
		}
		// 已装在其他同伴 → 清原宿主指向（同 unit 上是幂等换装，不需要清）。
		if cur.Valid && int(cur.Int64) != unitID {
			res, err := tx.Exec(`UPDATE cn35_player_units
				SET monster_uuid='',updated_at=?
				WHERE user_id=? AND unit_id=? AND monster_uuid=?`,
				nowUTC(), userID, int(cur.Int64), uuid)
			if err != nil {
				return fmt.Errorf("cn35store: 清除原宿主装备引用失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("cn35store: 魔宠 %s 声称装在 unit %d 但宿主引用不一致", uuid, cur.Int64)
			}
		}
		// 目标同伴已有其他魔宠 → 那只魔宠卸下（双向清）。
		var curUUID string
		if err := tx.QueryRow(`SELECT monster_uuid FROM cn35_player_units
			WHERE user_id=? AND unit_id=?`, userID, unitID).Scan(&curUUID); err != nil {
			return fmt.Errorf("cn35store: 读取目标同伴装备位失败: %w", err)
		}
		if curUUID != "" && curUUID != uuid {
			res, err := tx.Exec(`UPDATE cn35_player_monsters
				SET unit_id=NULL,updated_at=?
				WHERE user_id=? AND uuid=? AND unit_id=?`,
				nowUTC(), userID, curUUID, unitID)
			if err != nil {
				return fmt.Errorf("cn35store: 卸下目标同伴原魔宠失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("cn35store: 同伴 %d 声称装着魔宠 %s 但魔宠引用不一致", unitID, curUUID)
			}
		}
		res, err := tx.Exec(`UPDATE cn35_player_monsters
			SET unit_id=?,updated_at=? WHERE user_id=? AND uuid=?`,
			unitID, nowUTC(), userID, uuid)
		if err != nil {
			return fmt.Errorf("cn35store: 写入魔宠宿主失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrMonsterNotOwned
		}
		res, err = tx.Exec(`UPDATE cn35_player_units
			SET monster_uuid=?,updated_at=? WHERE user_id=? AND unit_id=?`,
			uuid, nowUTC(), userID, unitID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入同伴装备位失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrUnitNotOwned
		}
		return nil
	})
}

// UnequipMonster /user_monsters/off：unitID<0 表示客户端未指定宿主
// （MonsterButtonsListComponent.cs:382 形态），按当前宿主卸下；
// unitID>=0 时必须与实际宿主一致（错报宿主明确拒绝）。
func (s *Store) UnequipMonster(userID, uuid string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		cur, err := monsterHostTx(tx, userID, uuid)
		if err != nil {
			return err
		}
		if !cur.Valid {
			return cn35game.ErrMonsterNotEquipped
		}
		if unitID >= 0 && int(cur.Int64) != unitID {
			return cn35game.ErrMonsterHostMismatch
		}
		host := int(cur.Int64)
		res, err := tx.Exec(`UPDATE cn35_player_monsters
			SET unit_id=NULL,updated_at=? WHERE user_id=? AND uuid=? AND unit_id=?`,
			nowUTC(), userID, uuid, host)
		if err != nil {
			return fmt.Errorf("cn35store: 卸下魔宠失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: 魔宠 %s 宿主并发漂移", uuid)
		}
		res, err = tx.Exec(`UPDATE cn35_player_units
			SET monster_uuid='',updated_at=? WHERE user_id=? AND unit_id=? AND monster_uuid=?`,
			nowUTC(), userID, host, uuid)
		if err != nil {
			return fmt.Errorf("cn35store: 清除同伴装备位失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: 魔宠 %s 宿主引用不一致（unit %d 未回指）", uuid, host)
		}
		return nil
	})
}

// SetMonsterProtected /user_monsters/protect：逐 uuid 写 protected，
// 任一 uuid 不属本账号即整体回滚（不报部分成功）。
func (s *Store) SetMonsterProtected(userID string, uuids []string, protected bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		for _, uuid := range uuids {
			res, err := tx.Exec(`UPDATE cn35_player_monsters
				SET protected=?,updated_at=? WHERE user_id=? AND uuid=?`,
				boolInt(protected), nowUTC(), userID, uuid)
			if err != nil {
				return fmt.Errorf("cn35store: 写入魔宠保护位失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrMonsterNotOwned
			}
		}
		return nil
	})
}
