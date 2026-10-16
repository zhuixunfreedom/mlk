// presents_infos.go cn35_user_presents / cn35_infos 读取（LEGACY-ASSETS-02）。
//
// 表由 0015 建立。本文件只读：礼物的领取写面（/user_presents/receive/）
// 不在本期范围，未注册即客户端不会调用，不伪造成功。
package cn35store

import (
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// UnreadPresents 返回某账号「未领取且未过期」的待领礼物（LEGACY-ASSETS-02）。
//
// 过滤口径：
//   - received_at = 0（未领取）
//   - expire_at = 0（不过期）或 expire_at > now
//
// 表不存在时返回空切片而非错误：旧 state 目录（未升级到 0015）应表现为
// 「无待领礼物」，不应让 Home 的 requireData 失败导致整页取消。
func (s *Store) UnreadPresents(userID string, now int64) ([]cn35game.PresentRecord, error) {
	const q = `
SELECT uuid, content_type, content_id, amount, expire_at, created_at
  FROM cn35_user_presents
 WHERE user_id = ?
   AND received_at = 0
   AND (expire_at = 0 OR expire_at > ?)
 ORDER BY created_at ASC, uuid ASC`

	rows, err := s.db.Query(q, userID, now)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cn35store: 读取待领礼物: %w", err)
	}
	defer rows.Close()

	var out []cn35game.PresentRecord
	for rows.Next() {
		var r cn35game.PresentRecord
		if err := rows.Scan(&r.UUID, &r.ContentType, &r.ContentID,
			&r.Amount, &r.ExpireAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("cn35store: 扫描礼物行: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 遍历礼物行: %w", err)
	}
	return out, nil
}

// ActiveInfos 返回时间窗内启用的公告（LEGACY-ASSETS-02）。
//
// 过滤口径：enabled=1 且 (begin_at=0 或 begin_at<=now) 且 (end_at=0 或 end_at>now)。
// 排序 weight DESC, id DESC —— 首条即客户端 most_recently_info 的口径来源。
//
// 表不存在时返回空切片而非错误（同 UnreadPresents 的理由）。
func (s *Store) ActiveInfos(now int64) ([]cn35game.InfoRecord, error) {
	const q = `
SELECT id, category, title, body, begin_at, end_at
  FROM cn35_infos
 WHERE enabled = 1
   AND (begin_at = 0 OR begin_at <= ?)
   AND (end_at = 0 OR end_at > ?)
 ORDER BY weight DESC, id DESC`

	rows, err := s.db.Query(q, now, now)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cn35store: 读取公告: %w", err)
	}
	defer rows.Close()

	var out []cn35game.InfoRecord
	for rows.Next() {
		var r cn35game.InfoRecord
		if err := rows.Scan(&r.ID, &r.Category, &r.Title, &r.Body,
			&r.BeginAt, &r.EndAt); err != nil {
			return nil, fmt.Errorf("cn35store: 扫描公告行: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 遍历公告行: %w", err)
	}
	return out, nil
}

// 编译期断言：*Store 满足两个读面接口。
var (
	_ cn35game.PresentStore = (*Store)(nil)
	_ cn35game.InfoStore    = (*Store)(nil)
)
