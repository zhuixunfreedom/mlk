// cn35grant.go PLAY350-08 五卡礼包开发管理入口。
//
//	mercdb cn35-grant init    [-db PATH] [-device ID]   专用测试账号初始化→输出 user_id
//	mercdb cn35-grant preview [-db PATH] [-seed JSON] [-cards DIR] [-user ID]
//	mercdb cn35-grant apply   -user ID [-db PATH] [-seed JSON] [-cards DIR]
//	mercdb cn35-grant status  -user ID [-db PATH]
//
// 语义：固定 grant_key=cn350-default-five-star-v1；同事务完成
// 库存行+台账+队长修正；同键重复执行幂等返回原结果；
// 同键不同模板集拒绝（版本冲突）。默认不触碰教程/魔宠/其他账号。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
)

// 默认专用测试账号设备 ID（init 的幂等身份；与真实设备 ID 命名空间
// 隔开——真实设备为 "meizu ..."/模拟器串，dev- 前缀明确非玩家）。
const defaultDevDevice = "cn350-dev-test-account"

func cn35Grant(args []string) error {
	fs := flag.NewFlagSet("cn35-grant", flag.ContinueOnError)
	path := fs.String("db", "server-state-cn35/mercd35.sqlite3", "SQLite file")
	seed := fs.String("seed", "assets/cn350/five_star_grant.json", "五卡种子配置")
	cards := fs.String("cards", "assets/shared/cards", "cardcatalog 目录")
	user := fs.String("user", "", "目标 user_id（apply/status 必填）")
	device := fs.String("device", defaultDevDevice, "init 用设备 ID（幂等身份键）")
	if len(args) == 0 {
		return fmt.Errorf("usage: mercdb cn35-grant init|preview|apply|status [-flags]")
	}
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch action {
	case "init":
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()
		uid, isNew, err := st.EnsureDevAccount(*device)
		if err != nil {
			return err
		}
		state := "existing"
		if isNew {
			state = "created"
		}
		fmt.Printf("dev_account user_id=%s device=%s (%s)\n", uid, *device, state)
		fmt.Printf("发放: mercdb cn35-grant apply -db %s -user %s\n", *path, uid)
		return nil

	case "preview":
		cfg, cat, err := loadGrantInputs(*seed, *cards)
		if err != nil {
			return err
		}
		fmt.Printf("grant_version=%s grant_key=%s leader=%d cards=%v\n",
			cfg.GrantVersion, cn35game.FiveStarGrantKey, cfg.LeaderUnitID, cfg.UnitIDs())
		for _, u := range cfg.Units {
			cu, _ := cat.Unit(u.ID)
			fmt.Printf("  id=%d rare=%d %s%s lv=%d role=%s\n",
				u.ID, cu.Rare, cu.Title, cu.Name, u.InitialLevel, u.Role)
		}
		if *user != "" {
			return grantStatus(*path, *user)
		}
		return nil
	case "apply":
		if *user == "" {
			return fmt.Errorf("apply 需要 -user <user_id>（先 cn35-grant init 取号）")
		}
		cfg, cat, err := loadGrantInputs(*seed, *cards)
		if err != nil {
			return err
		}
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()
		res, err := st.GrantFiveStar(*user, cfg, cat)
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return nil

	case "status":
		if *user == "" {
			return fmt.Errorf("status 需要 -user <user_id>")
		}
		return grantStatus(*path, *user)
	}
	return fmt.Errorf("usage: mercdb cn35-grant [-db PATH] [-seed JSON] [-cards DIR] [-user ID] [-device ID] init|preview|apply|status")
}

// grantStatus 只读核对某账号的五卡发放状态。
//
// 必须用 OpenReadOnly（mode=ro + query_only）：preview/status 是**只读观察**，
// 不得触发 Migrate/Open/checkpoint——否则对未升级的目标库跑一次预览就会
// 顺手把库升到最新迁移（2026-09-23 实测事故：preview 把 0007 库升到 0009）。
// 旧库缺少 cn35_inventory_grants 时直接报错，由人决定是否升级，不代做。
func grantStatus(path, userID string) error {
	st, err := cn35store.OpenReadOnly(resolveAsset(path))
	if err != nil {
		return err
	}
	defer st.Close()
	applied, err := st.GrantStatus(userID, cn35game.FiveStarGrantKey)
	if err != nil {
		return err
	}
	units, err := st.Units(userID)
	if err != nil {
		return err
	}
	owned := []int{}
	for _, u := range units {
		owned = append(owned, u.UnitID)
	}
	fmt.Printf("user_id=%s grant_key=%s applied=%v owned_units=%v (read_only=true)\n",
		userID, cn35game.FiveStarGrantKey, sortedKeys(applied), owned)
	return nil
}

func loadGrantInputs(seedPath, cardsDir string) (*cn35game.FiveStarConfig, *cardcatalog.Catalog, error) {
	cfg, err := cn35game.LoadFiveStarConfig(resolveAsset(seedPath))
	if err != nil {
		return nil, nil, err
	}
	cat, err := cardcatalog.Load(resolveAsset(cardsDir))
	if err != nil {
		return nil, nil, err
	}
	if err := cn35game.ValidateFiveStar(cat, cfg); err != nil {
		return nil, nil, err
	}
	return cfg, cat, nil
}

func openStore(path string) (*cn35store.Store, error) {
	st, err := cn35store.Open(resolveAsset(path))
	if err != nil {
		return nil, err
	}
	return st, nil
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// resolveAsset 相对路径先按 cwd 解析，不存在时回退到 server 模块根
// （mercdb 从仓库根调用时参数写作 server/assets/... 也能命中默认）。
func resolveAsset(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if _, err := os.Stat(filepath.Join("server", p)); err == nil {
		return filepath.Join("server", p)
	}
	return p
}
