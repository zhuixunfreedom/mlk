// quest.go cn35 剧情关卡账号进度的 SQLite 读面（STORY350-03）。
//
// 表归 0006 迁移（cn35_quest_progress/cn35_quest_clears）。本文件只实现
// 读路径与建号种子写入：进度推进与通关计数的事务写归 STORY350-07
// （战斗结算同事务），届时复用 WithTx + 追加 *Tx 原语。
//
// 读语义：无进度行 → 初始值 (1,1,1,0)（quest0 内嵌实证：stage/area/quest
// 初始均 1，hard_mode 未开放为 0）；"无行"与"从未推进"语义等价，不作为
// 不变量损坏报错——0006 已为迁移时点全部既有账号回填，createAccount
// 又在建号事务内插行，正常路径不会出现缺行。
package cn35store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足 03 消费的剧情进度读面。
var _ cn35game.QuestStore = (*Store)(nil)

// questProgressRow 读 cn35_quest_progress 单行；无行返回 nil,nil。
func (s *Store) questProgressRow(userID string) (*cn35game.QuestProgress, error) {
	var p cn35game.QuestProgress
	err := s.db.QueryRow(`SELECT stage_id,area_id,quest_id,hard_mode_stage_id
		FROM cn35_quest_progress WHERE user_id=?`, userID).
		Scan(&p.StageID, &p.AreaID, &p.QuestID, &p.HardModeStageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// QuestProgress 实现 cn35game.QuestStore：进度指针 + 各关卡通关计数。
// 无进度行 → 初始进度（见包注释）。clears 恒非 nil。
func (s *Store) QuestProgress(userID string) (*cn35game.QuestProgress, error) {
	p, err := s.questProgressRow(userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = &cn35game.QuestProgress{StageID: 1, AreaID: 1, QuestID: 1, HardModeStageID: 0}
	}
	p.Clears = map[int]map[string]int{}
	rows, err := s.db.Query(`SELECT quest_id,difficulty,clear_count
		FROM cn35_quest_clears WHERE user_id=? ORDER BY quest_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var qid, cnt int
		var diff string
		if err = rows.Scan(&qid, &diff, &cnt); err != nil {
			return nil, err
		}
		if p.Clears[qid] == nil {
			p.Clears[qid] = map[string]int{}
		}
		p.Clears[qid][diff] = cnt
	}
	return p, rows.Err()
}

// questProgressUserFields 把进度指针映成 data.user 的四个字段
// （preset_data 注入用）。无行 → 初始值。
func (s *Store) questProgressUserFields(userID string) (map[string]any, error) {
	p, err := s.questProgressRow(userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = &cn35game.QuestProgress{StageID: 1, AreaID: 1, QuestID: 1, HardModeStageID: 0}
	}
	return map[string]any{
		"stage_id":           p.StageID,
		"area_id":            p.AreaID,
		"quest_id":           p.QuestID,
		"hard_mode_stage_id": p.HardModeStageID,
	}, nil
}

// insertQuestProgressSeedTx 建号事务内插入初始进度行——与 0006 回填
// 同一组种子值 (1,1,1,0)，保证"每个账号恒有进度行"不变量。
func insertQuestProgressSeedTx(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cn35_quest_progress
		(user_id,stage_id,area_id,quest_id,hard_mode_stage_id,created_at,updated_at)
		VALUES (?,1,1,1,0,?,?)`, userID, nowUTC(), nowUTC())
	return err
}
