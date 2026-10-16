// balance_grant.go 开发期受控余额发放（GACHA350 真机验收用）。
//
// 背景：客户端抽卡按钮以缓存余额做前置门槛（user.gacha_point /
// user_coin.amount），测试账号余额不足时按钮置灰、execute 请求根本
// 不发出，服务端拒绝路径无法验收。本子命令经 cn35_player_balances
// 正规行集写入，preset_data/reload_data 随即原样下发——与生产发放的
// 读取路径完全一致。
//
// 边界：仅 mercdb 离线管理工具调用；不进任何 handler/路由；白名单
// 货币 + 账号存在校验 + 单事务 upsert，失败整体回滚。
package cn35store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// BalanceGrantResult 发放前后余额对照（CLI/审计输出用）。
type BalanceGrantResult struct {
	UserID string           `json:"user_id"`
	Before map[string]int64 `json:"before"`
	After  map[string]int64 `json:"after"`
}

// grantableCurrencies 白名单：只放行已知货币，杜绝任意键写库。
var grantableCurrencies = map[string]bool{
	"coin":        true,
	"gacha_point": true,
}

// GrantBalances 将指定账号的给定货币覆盖为固定值（幂等：重复执行结果相同）。
// 单事务：账号校验 → 读前值 → upsert → 读回校验，任一步失败整体回滚。
func (s *Store) GrantBalances(userID string, grants map[string]int64) (*BalanceGrantResult, error) {
	if userID == "" {
		return nil, fmt.Errorf("cn35store: userID 为空")
	}
	if len(grants) == 0 {
		return nil, fmt.Errorf("cn35store: 未指定发放货币")
	}
	for cur, amt := range grants {
		if !grantableCurrencies[cur] {
			return nil, fmt.Errorf("cn35store: 未知货币 %q（白名单 coin/gacha_point）", cur)
		}
		if amt < 0 {
			return nil, fmt.Errorf("cn35store: 负余额 %q=%d", cur, amt)
		}
	}
	res := &BalanceGrantResult{UserID: userID,
		Before: map[string]int64{}, After: map[string]int64{}}
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrNoAccount
		}
		read := func(dst map[string]int64) error {
			rows, err := tx.QueryContext(ctx,
				`SELECT currency,amount FROM cn35_player_balances WHERE user_id=?`, userID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cur string
				var amt int64
				if err = rows.Scan(&cur, &amt); err != nil {
					return err
				}
				dst[cur] = amt
			}
			return rows.Err()
		}
		if err := read(res.Before); err != nil {
			return err
		}
		curs := make([]string, 0, len(grants))
		for cur := range grants {
			curs = append(curs, cur)
		}
		sort.Strings(curs)
		for _, cur := range curs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_player_balances
				(user_id,currency,amount,updated_at) VALUES (?,?,?,?)
				ON CONFLICT(user_id,currency) DO UPDATE
				SET amount=excluded.amount,updated_at=excluded.updated_at`,
				userID, cur, grants[cur], nowUTC()); err != nil {
				return err
			}
		}
		return read(res.After)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
