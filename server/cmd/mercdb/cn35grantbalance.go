// cn35grantbalance.go GACHA350 真机验收用开发期余额发放。
//
//	mercdb cn35-grant-balance [-db PATH] -user <userID> [-coin N] [-gacha-point N]
//
// 语义：经 cn35_player_balances 正规行集把指定货币覆盖为固定值——
// 单事务（账号校验→前值→upsert→读回）、货币白名单、非负校验、
// 幂等。输出 before/after JSON 供验收留档。仅开发管理入口，不接
// 路由、不进 handler。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
)

func cn35GrantBalance(args []string) error {
	fs := flag.NewFlagSet("cn35-grant-balance", flag.ContinueOnError)
	path := fs.String("db", "server-state-cn35/mercd35.sqlite3", "SQLite file")
	user := fs.String("user", "", "目标 user_id（cn35_accounts.user_id）")
	coin := fs.Int64("coin", -1, "coin 目标值（缺省不动）")
	gp := fs.Int64("gacha-point", -1, "gacha_point 目标值（缺省不动）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	grants := map[string]int64{}
	if *coin >= 0 {
		grants["coin"] = *coin
	}
	if *gp >= 0 {
		grants["gacha_point"] = *gp
	}
	if *user == "" || len(grants) == 0 {
		return fmt.Errorf(
			"usage: mercdb cn35-grant-balance [-db PATH] -user <userID> [-coin N] [-gacha-point N]")
	}
	st, err := openStore(*path)
	if err != nil {
		return err
	}
	defer st.Close()
	res, err := st.GrantBalances(*user, grants)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化: %w", err)
	}
	fmt.Println(string(out))
	return nil
}
