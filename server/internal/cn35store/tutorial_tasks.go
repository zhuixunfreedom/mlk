// tutorial_tasks.go CN3.5 新手任务（TutorialTask 页）读面与领取写面。
//
// 任务目录定义在 cn35game/tutorial_tasks.go（自研目录，原服配置未取证）；
// 进度值不在客户端推进，全部由既有台账推导：
//
//	idx0 win_quest        通关总次数（cn35_quest_clears.clears 合计）
//	idx1 has_monster      持有魔宠数（cn35_player_monsters）
//	idx2 master_level_up  玩家等级（cn35_player_profiles.level）
//	idx3 finish_quest     通关总次数（同 idx0，amount 更高）
//	idx4 first_gacha      扭蛋次数（cn35_gacha_draws）
//	idx5 op_power_up      持有素材种数（cn35_player_items quantity>0）
//
// 进度单调不减（计数/等级不会下降），领取幂等由
// cn35_tutorial_task_claims 主键兜底。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// TutorialTaskProgress 返回与目录索引对齐的进度值与已领取集合。
func (s *Store) TutorialTaskProgress(userID string) (progress []int64, claimed map[int]bool, err error) {
	progress = make([]int64, 6)
	row := s.db.QueryRow(`SELECT
		(SELECT COALESCE(SUM(clear_count),0) FROM cn35_quest_clears WHERE user_id=?),
		(SELECT COUNT(*) FROM cn35_player_monsters WHERE user_id=?),
		(SELECT level FROM cn35_player_profiles WHERE user_id=?),
		(SELECT COUNT(*) FROM cn35_gacha_operations WHERE user_id=?),
		(SELECT COUNT(*) FROM cn35_player_items WHERE user_id=? AND quantity>0)`,
		userID, userID, userID, userID, userID)
	var clears, monsters, level, draws, items int64
	if err = row.Scan(&clears, &monsters, &level, &draws, &items); err != nil {
		return nil, nil, err
	}
	progress[0] = clears
	progress[1] = monsters
	progress[2] = level
	progress[3] = clears
	progress[4] = draws
	progress[5] = items
	claimed = map[int]bool{}
	rows, err := s.db.Query(`SELECT task_index FROM cn35_tutorial_task_claims WHERE user_id=?`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var idx int
		if err = rows.Scan(&idx); err != nil {
			return nil, nil, err
		}
		claimed[idx] = true
	}
	return progress, claimed, rows.Err()
}

// ClaimTutorialTaskReward 同事务发奖 + 记领取标记（0019）。
// rewardKind 仅取 balance（发余额 currency=key）/ item（发物品 item_key=key）；
// 已领取 → cn35game.ErrTutorialTaskClaimed。
func (s *Store) ClaimTutorialTaskReward(userID string, taskIndex int, rewardKind, rewardKey string, rewardCount int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM cn35_tutorial_task_claims
			WHERE user_id=? AND task_index=?`, userID, taskIndex).Scan(&exists)
		if err == nil {
			return cn35game.ErrTutorialTaskClaimed
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		switch rewardKind {
		case "balance":
			err = creditBalanceTx(tx, userID, rewardKey, rewardCount)
		case "item":
			err = creditItemTx(tx, userID, rewardKey, int(rewardCount))
		default:
			err = fmt.Errorf("cn35store: 未知任务奖励类型 %q", rewardKind)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO cn35_tutorial_task_claims (user_id,task_index,claimed_at)
			VALUES (?,?,?)`, userID, taskIndex, nowUTC())
		return err
	})
}
