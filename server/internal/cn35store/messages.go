// messages.go cn35_messages 内容池读取（CN35-UI-RESTORE）。
//
// 表由 0010 建立、0011 播种（1000 条重建讨论条目）。本文件只读。
//
// 随机抽样在 SQL 侧完成（ORDER BY RANDOM()），避免把整池拉进内存。
// 池子规模小（1000 行）且 /users/messages 是 10s 轮询，成本可忽略。
package cn35store

import (
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// RandomMessages 从启用条目中随机抽取至多 limit 条（CN35-UI-RESTORE）。
//
// 仅返回客户端会显示在 footer 的三个类别；其余类别即使被手工置为 enabled
// 也不会返回（读面与客户端 flag2 语义对齐，避免下发无效数据）。
//
// 表不存在时返回空切片而非错误：旧 state 目录（未升级到 0010）或只读打开
// 历史库时应表现为「无消息」，不应让轮询端点 500。
func (s *Store) RandomMessages(limit int) ([]cn35game.MessageRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	const q = `
SELECT id, category, body, author_name
  FROM cn35_messages
 WHERE enabled = 1
   AND category IN ('user_comment', 'guild_comment', 'community_comment')
 ORDER BY RANDOM()
 LIMIT ?`

	rows, err := s.db.Query(q, limit)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cn35store: 抽取消息内容池: %w", err)
	}
	defer rows.Close()

	var out []cn35game.MessageRecord
	for rows.Next() {
		var r cn35game.MessageRecord
		if err := rows.Scan(&r.ID, &r.Category, &r.Body, &r.Author); err != nil {
			return nil, fmt.Errorf("cn35store: 扫描消息行: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 遍历消息行: %w", err)
	}
	return out, nil
}

// isMissingTable 判断错误是否为「表不存在」。
// 只读打开未升级的历史库时 SELECT 会失败，读面据此降级为空池。
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

// 编译期断言：*Store 满足 cn35game.MessageStore。
var _ cn35game.MessageStore = (*Store)(nil)
