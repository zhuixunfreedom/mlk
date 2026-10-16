// guild_battle_wire.go GUILD350-06/07：会战应援/祈祷/出战战斗面。
//
// 客户端路径（客户端事实）：
//   - GET /battles/help、/battles/pray（响应读 animation_message）
//   - GET /battles/guild_quest_execute.json?mode=battle&guild_id=&guild_id_N=&difficulty_id=&party_id=
//   - execute 文档内 ap_use_url/result_url/lose_url 由服务端下发（本会战链
//     用 /battles/ 前缀，与剧情 /quests/ 链隔离）。
package cn35game

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// gwHelpPrayHandler /battles/help|/battles/pray。
func gwHelpPrayHandler(dep Deps, action string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		gp, cost, err := dep.Guild.GWHelpPray(req.UserID, action,
			req.Query.Get("op_key"), guildWireNow(dep))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		gw, err := dep.Guild.GWState(req.UserID, guildWireNow(dep))
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_" + action,
			"gp": gp, "bp_cost": cost,
		}
		msg := fmt.Sprintf("GP +%d", gp)
		return cn35wire.MarshalJSON(map[string]any{
			"status":            "success",
			"animation_message": msg,
			"data": map[string]any{
				"user":             snap.User,
				"battle_condition": battleConditionDoc(gw),
				"guild_war": map[string]any{
					"action": action, "gp_delta": gp, "bp_cost": cost,
				},
			},
		})
	})
}

// gwExecuteHandler GET /battles/guild_quest_execute.json：mode=battle 的
// 会战出战建档。响应文档与剧情 execute 同形（QuestExecuteDocument 统一解析），
// 敌方怪组 = 目标公会防卫卡组快照。
func gwExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_quest_execute",
			"mode": q.Get("mode"), "guild_id": q.Get("guild_id"),
			"difficulty_id": q.Get("difficulty_id"), "party_id": q.Get("party_id"),
		}
		if mode := q.Get("mode"); mode != "battle" {
			return cn35wire.MarshalJSON(guildModal("无法战斗", "不支持的战斗模式"))
		}
		diff := q.Get("difficulty_id")
		if diff == "" {
			diff = "normal"
		}
		// 目标集合：主攻 guild_id + guild_id_1..N 附加（服务端逐个验证同组）。
		targetGuildID := q.Get("guild_id")
		var extraTargets []string
		for i := 1; ; i++ {
			v := q.Get("guild_id_" + strconv.Itoa(i))
			if v == "" {
				break
			}
			extraTargets = append(extraTargets, v)
		}
		ordinal, err := ParsePartyOrdinal(q.Get("party_id"))
		if err != nil {
			return cn35wire.MarshalJSON(guildModal("无法战斗", "编队编号无效"))
		}
		if dep.Catalog == nil {
			return nil, fmt.Errorf("cn35: 公会战缺 Catalog")
		}
		atk, units, seeds, err := dep.Guild.GWAttackOpen(req.UserID, targetGuildID,
			extraTargets, diff, ordinal, 5, q.Get("op_key"), guildWireNow(dep))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		doc, err := buildGWExecuteDoc(dep, atk, units, seeds, snap)
		if err != nil {
			return nil, err
		}
		c.Decoded["battle_id"] = atk.BattleID
		return cn35wire.MarshalJSON(doc)
	})
}

// gwDifficultyBattleBP 难度面板千分比（normal/hard/very_hard），
// 供防卫魔宠与小怪共用缩放。
func gwDifficultyBattleBP(difficulty string) (hpBP, atBP int) {
	hpBP = GuildRules.DifficultyHpBP[difficulty]
	atBP = GuildRules.DifficultyAtBP[difficulty]
	if hpBP == 0 {
		hpBP = 1000
	}
	if atBP == 0 {
		atBP = 1000
	}
	return hpBP, atBP
}

// gwBossAppearList 防卫快照 → boss_appear_table 波次 1 的大怪文档列。
// 客户端 Quest.cs WatchBattleBossAppearTable：boss 按 interval_sec
// 逐只出场；OnBattleBossDead 里 spawned boss 全灭且文档耗尽 → Clear()，
// 即「大魔宠击杀完即算完成战斗」的下发面。
//
// interval_sec 语义 = 该只出场后距下一只的间隔秒：组内第二只给 0
// 实现「每一波 1-2 只」，组间给难度间隔（普通 120/困难 80/极难 40s）。
func gwBossAppearList(atk *GWAttack, dep Deps) ([]any, error) {
	hpBP, atBP := gwDifficultyBattleBP(atk.Difficulty)
	interval := GuildRules.DifficultyWaveIntervalSec[atk.Difficulty]
	if interval <= 0 {
		interval = 120
	}
	total := GuildRules.BattleBossTotal
	if total <= 0 {
		total = len(atk.DefenseSnapshot)
	}
	// 多目标出击时按目标公会轮转取材：总量上限截断也保证每个
	// 已收费目标都有防卫怪出场（顺序截断会把后排目标整场吃掉，
	// 但其 GP 在结算时仍被掠夺）。
	byGuild := map[string][]GWDefenseMonster{}
	guildOrder := []string{}
	for _, d := range atk.DefenseSnapshot {
		if _, ok := byGuild[d.GuildID]; !ok {
			guildOrder = append(guildOrder, d.GuildID)
		}
		byGuild[d.GuildID] = append(byGuild[d.GuildID], d)
	}
	picked := []GWDefenseMonster{}
	for len(picked) < total && len(guildOrder) > 0 {
		next := guildOrder[:0]
		for _, g := range guildOrder {
			if len(byGuild[g]) == 0 {
				continue
			}
			picked = append(picked, byGuild[g][0])
			byGuild[g] = byGuild[g][1:]
			if len(byGuild[g]) > 0 {
				next = append(next, g)
			}
			if len(picked) >= total {
				break
			}
		}
		guildOrder = next
	}
	out := []any{}
	for i, d := range picked {
		m, err := dep.Catalog.Monster(d.MonsterID)
		if err != nil {
			return nil, fmt.Errorf("防卫魔宠模板缺失 %d: %w", d.MonsterID, err)
		}
		hp := int(float64(GuildRules.DefenseHPBase) *
			(1 + GuildRules.DefenseHPPerLevel*float64(d.Level-1)) *
			float64(hpBP) / 1000.0 * (1 + d.HPBonus))
		at := int(float64(GuildRules.DefenseATBase) *
			float64(atBP) / 1000.0 * (1 + d.ATBonus))
		gap := interval
		if i%2 == 0 {
			gap = 0 // 组内第二只紧随，组间才间隔
		}
		out = append(out, map[string]any{
			"id": d.MonsterID, "scale": 1.0, "powerup_rate": 2.5,
			"gauge": true, "interval_sec": gap,
			"params": storyMonsterParams(m, hp, at),
		})
	}
	return out, nil
}

// gwTrashAppearTable 小怪 appear_table（波次 1）：DifficultyTrashTotal
// 条数按难度间隔分批出场（每组 ≈ total/批数），与 boss 波次时间轴对齐。
// 单波时间轴：ms 键为该怪出场毫秒（客户端 WatchAppearTable 消费），
// 击杀不推进波次（wave_clear_pt 缺省 0），胜利只由 boss 表驱动。
func gwTrashAppearTable(atk *GWAttack, dep Deps, bossGroups int) (map[string]any, error) {
	total := GuildRules.DifficultyTrashTotal[atk.Difficulty]
	if total <= 0 || len(GuildRules.TrashMonsterIDs) == 0 {
		return map[string]any{}, nil
	}
	hpBP, atBP := gwDifficultyBattleBP(atk.Difficulty)
	interval := GuildRules.DifficultyWaveIntervalSec[atk.Difficulty]
	if interval <= 0 {
		interval = 120
	}
	if bossGroups <= 0 {
		bossGroups = 1
	}
	wm := map[string]any{}
	per := total / bossGroups
	rem := total % bossGroups
	idx := 0
	for g := 0; g < bossGroups; g++ {
		count := per
		if g == bossGroups-1 {
			count += rem
		}
		for j := 0; j < count; j++ {
			mid := GuildRules.TrashMonsterIDs[idx%len(GuildRules.TrashMonsterIDs)]
			idx++
			m, err := dep.Catalog.Monster(mid)
			if err != nil {
				return nil, fmt.Errorf("小怪模板缺失 %d: %w", mid, err)
			}
			params := storyMonsterParams(m, 0, 0)
			scaleParamKeys(params, []string{"hp", "life"}, hpBP)
			scaleParamKeys(params, []string{"at", "atk", "seed_at"}, atBP)
			ms := g*interval*1000 + 500 + j*200
			wm[strconv.Itoa(ms)] = map[string]any{
				"id": mid, "scale": 1.0, "powerup_rate": 2.5,
				"gauge": false, "params": params,
			}
		}
	}
	return wm, nil
}

// gwMonsterDocs 收集 boss + 小怪引用的魔宠模板文档（monsters 键）。
func gwMonsterDocs(ids []int, dep Deps) (map[string]any, error) {
	monsters := map[string]any{}
	for _, mid := range ids {
		key := strconv.Itoa(mid)
		if _, ok := monsters[key]; ok {
			continue
		}
		m, err := dep.Catalog.Monster(mid)
		if err != nil {
			return nil, fmt.Errorf("魔宠模板缺失 %d: %w", mid, err)
		}
		monsters[key] = BattleMonsterDoc(m)
	}
	return monsters, nil
}

// buildGWExecuteDoc 会战 execute 文档（剧情 execute 键集同形下发）。
func buildGWExecuteDoc(dep Deps, atk *GWAttack, units []UnitRecord,
	seeds []MonsterRecord, snap *PlayerSnapshot) (map[string]any, error) {
	cat, err := loadQuestCatalog(dep.AssetsDir)
	if err != nil {
		return nil, fmt.Errorf("会战关卡目录不可用: %w", err)
	}
	// 防卫魔宠 → boss_appear_table（大怪，击杀完即 Clear）；
	// 小怪 → appear_table 单波时间轴（ms 出场，无总时长限制）。
	bossList, err := gwBossAppearList(atk, dep)
	if err != nil {
		return nil, err
	}
	bossGroups := (len(bossList) + 1) / 2
	trashWave, err := gwTrashAppearTable(atk, dep, bossGroups)
	if err != nil {
		return nil, err
	}
	appearTable := map[string]any{"1": trashWave}
	bossAppearTable := map[string]any{"1": bossList}
	monsterIDs := append([]int{}, MonsterIDsInAppearTable(appearTable)...)
	for _, d := range atk.DefenseSnapshot {
		monsterIDs = append(monsterIDs, d.MonsterID)
	}
	monsters, err := gwMonsterDocs(monsterIDs, dep)
	if err != nil {
		return nil, err
	}
	// 参战单位文档。
	skillsByUnit, err := skillsByUnitFor(dep, atk.UserID)
	if err != nil {
		return nil, err
	}
	unitDocs := make([]any, 0, len(units))
	for _, r := range units {
		cu, err := dep.Catalog.Unit(r.UnitID)
		if err != nil {
			return nil, err
		}
		udoc, err := buildUnitDoc(r, cu)
		if err != nil {
			return nil, err
		}
		applyUnitSkills(udoc, cu.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		if atk.Effects.Version > 0 {
			udoc["hp"] = int(float64(udoc["hp"].(int)) * atk.Effects.UnitHP)
			udoc["at"] = int(float64(udoc["at"].(int)) * atk.Effects.UnitAT)
			if toughness, ok := udoc["taghness"].(float64); ok {
				udoc["taghness"] = toughness * atk.Effects.UnitToughness
			}
		}
		unitDocs = append(unitDocs, udoc)
	}
	// asset_names：公共段 + 单位三件套 + 敌怪三件套 + 参战魔宠贴图 + 背景。
	assets := append([]string{}, cat.Defaults.AssetNamesCommon...)
	for _, r := range units {
		assets = append(assets,
			fmt.Sprintf("unit/unit_%d", r.UnitID),
			fmt.Sprintf("unit/unit_btn_%d", r.UnitID),
			fmt.Sprintf("unit/unit_large_%d", r.UnitID))
	}
	declared := map[string]bool{}
	for _, a := range assets {
		declared[a] = true
	}
	for _, ud := range unitDocs {
		udoc := ud.(map[string]any)
		for _, k := range []string{"attack_effect", "target_effect"} {
			e, _ := udoc[k].(string)
			if e != "" && !declared[e] {
				declared[e] = true
				assets = append(assets, e)
			}
		}
	}
	declaredMonster := map[int]bool{}
	for _, mid := range monsterIDs {
		if declaredMonster[mid] {
			continue
		}
		declaredMonster[mid] = true
		assets = append(assets,
			fmt.Sprintf("monster/monster_%d", mid),
			fmt.Sprintf("monster/monster_btn_%d", mid),
			fmt.Sprintf("monster/monster_large_ns_%d", mid))
	}
	for _, m := range seeds {
		if !declaredMonster[m.MonsterID] {
			declaredMonster[m.MonsterID] = true
			assets = append(assets,
				fmt.Sprintf("monster/monster_btn_%d", m.MonsterID),
				fmt.Sprintf("monster/monster_large_ns_%d", m.MonsterID))
		}
	}
	assets = append(assets, "background/background1_1", "sound/B-4")
	// 单波：length 足够大让终点旗不可及，战斗只由 boss 清空
	// （OnBattleBossDead→Clear）或我方全灭终结；无 time_limit =
	// 无战斗总时长限制（客户端 Has() 保护，缺失即不限时）。
	helpTable := map[string]any{"1": map[string]any{}}
	timezone := map[string]any{"1": 1}
	waveConf := map[string]any{"1": map[string]any{"length": 100000}}
	bid := atk.BattleID
	initialSP := 500
	if atk.Effects.Version > 0 {
		initialSP = atk.Effects.InitialSP
	}
	questDoc := map[string]any{
		"id": 0, "name": "公会战", "mode": "battle",
		"use_ap":           atk.BPCost,
		"difficulty_id":    atk.Difficulty,
		"difficulty_name":  atk.Difficulty,
		"background_id":    1,
		"initial_power":    initialSP,
		"unit_count_limit": 5,
		"seed_atack_rate":  1.0,
		"unit_help_rate":   1.0,
		"continuable":      false,
		"wave_conf":        waveConf,
		"background":       cat.Defaults.Background,
		"powerup_conf":     cat.Defaults.PowerupConf,
	}
	return map[string]any{
		"user":              snap.User,
		"quest":             questDoc,
		"units":             unitDocs,
		"monsters":          monsters,
		"main_character":    cat.Defaults.MainCharacter,
		"appear_table":      appearTable,
		"boss_appear_table": bossAppearTable,
		"help_table":        helpTable,
		"timezone_table":    timezone,
		"asset_names":       assets,
		"bgm_name":          "sound/B-4",
		"exp":               0,
		"clear_count":       0,
		"ap_use_url":        "/battles/use_ap?bid=" + bid,
		"result_url":        "/battles/result?bid=" + bid,
		"lose_url":          "/battles/lose?bid=" + bid,
		"next_page_message": "name=GuildTop&base=Common/Pages/GuildTop",
		"battle_log":        false,
		"data":              map[string]any{"user": snap.User},
	}, nil
}

// gwUseAPHandler /battles/use_ap（BP 已开战扣；prepared→bp_paid 幂等）。
func gwUseAPHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_use_ap", "bid": bid,
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		atk, err := dep.Guild.GWAttackPay(req.UserID, bid)
		switch {
		case errors.Is(err, ErrGWAttackNotFound), errors.Is(err, ErrGWAttackState):
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		c.Decoded["status"] = atk.Status
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   map[string]any{"user": snap.User},
		})
	})
}

// gwResultHandler /battles/result：胜利结算（GP/奖励入账 + 幂等重放）。
func gwResultHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_result",
			"bid": bid, "time": req.Query.Get("time"), "mc": req.Query.Get("mc"),
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		res, err := dep.Guild.GWAttackSettle(req.UserID, bid, true, 0)
		switch {
		case errors.Is(err, ErrGWAttackNotFound), errors.Is(err, ErrGWAttackState):
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		if res.Already {
			return cn35wire.MarshalJSON(statusDoc("already_received"))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status":            "success",
			"data":              map[string]any{"user": snap.User},
			"next_page_message": "name=GuildTop&base=Common/Pages/GuildTop",
		})
	})
}

// gwLoseHandler /battles/lose：败北上报（幂等）。
func gwLoseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_lose", "bid": bid,
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		_, err := dep.Guild.GWAttackSettle(req.UserID, bid, false, 0)
		switch {
		case errors.Is(err, ErrGWAttackNotFound), errors.Is(err, ErrGWAttackState):
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		return cn35wire.MarshalJSON(statusDoc("success"))
	})
}

// registerGuildRoutes 装配 dep.Guild 时注册；未装配走 requireData 空态。
func registerGuildRoutes(register func(method, path, note string, handler httpapi.Handler), dep Deps) {
	reg := func(method, path, note string, h httpapi.Handler) {
		register(method, path, note, h)
		if method == http.MethodGet {
			// 与既有读面同约定（version_info/presents/infos 等）：客户端
			// requireData 以 POST 加密体访问读面，双注册 GET+POST。
			register(http.MethodPost, path, note+" POST 形态", h)
		}
	}
	// 读面
	reg(http.MethodGet, "/guilds/", "公会主读面（GUILD350-02）", guildsIndexHandler(dep))
	reg(http.MethodGet, "/guilds/deck", "防卫卡组读面", guildDeckHandler(dep))
	reg(http.MethodGet, "/guilds/monsters", "可选防卫魔宠", guildMonstersHandler(dep))
	reg(http.MethodGet, "/guilds/search_index", "公会检索配置", guildSearchIndexHandler(dep))
	reg(http.MethodGet, "/guilds/subscriptions", "入会申请列表", guildSubscriptionsHandler(dep))
	reg(http.MethodGet, "/guilds/show", "公会信息出边", guildShowHandler(dep))
	reg(http.MethodGet, "/guilds/search.json", "公会搜索", guildSearchHandler(dep))
	reg(http.MethodGet, "/guild_rankings", "会战排名", guildRankingsHandler(dep))
	reg(http.MethodGet, "/waiting_users", "本人申请/受邀", waitingUsersHandler(dep))
	reg(http.MethodGet, "/waiting_users/recruit", "招募检索", waitingRecruitHandler(dep))
	// 写面
	reg(http.MethodPost, "/guilds/create.json", "创建公会", guildCreateHandler(dep))
	reg(http.MethodGet, "/guilds/join", "入会/申请", guildJoinHandler(dep))
	reg(http.MethodGet, "/guilds/approve_subscription", "批准申请", guildDecideSubHandler(dep, true))
	reg(http.MethodGet, "/guilds/reject_subscription", "拒绝申请", guildDecideSubHandler(dep, false))
	reg(http.MethodPost, "/guilds/reject_all_subscription", "拒全部申请", guildRejectAllHandler(dep))
	reg(http.MethodGet, "/guilds/dropout", "退会/踢人", guildDropoutHandler(dep))
	reg(http.MethodGet, "/guilds/change_master", "禅让会长", guildChangeRoleHandler(dep, GuildRoleMaster))
	reg(http.MethodGet, "/guilds/change_sub_master", "任命副长", guildChangeRoleHandler(dep, GuildRoleSubMaster))
	reg(http.MethodGet, "/guilds/fire_sub_master", "解除副长", guildChangeRoleHandler(dep, GuildRoleMember))
	reg(http.MethodGet, "/guilds/update", "公会设置变更", guildUpdateHandler(dep))
	reg(http.MethodPost, "/guilds/update", "公会设置变更 POST", guildUpdateHandler(dep))
	reg(http.MethodPost, "/guilds/set_shifts", "曜日参战设定", guildSetShiftsHandler(dep))
	reg(http.MethodGet, "/guilds/invest", "设施投资", guildInvestHandler(dep))
	reg(http.MethodGet, "/guilds/set_deck", "防卫卡组设置", guildSetDefenseHandler(dep))
	reg(http.MethodGet, "/guilds/remove_from_deck", "防卫卡组移除", guildRemoveDefenseHandler(dep))
	reg(http.MethodGet, "/guilds/send_push_notify", "推送通知占位", guildPushNotifyHandler(dep))
	// 会战战斗链
	reg(http.MethodGet, "/battles/help", "会战应援", gwHelpPrayHandler(dep, "help"))
	reg(http.MethodGet, "/battles/pray", "会战祈祷", gwHelpPrayHandler(dep, "pray"))
	reg(http.MethodGet, "/battles/guild_quest_execute.json", "会战出战建档", gwExecuteHandler(dep))
	reg(http.MethodGet, "/battles/use_ap", "会战 BP 付点（幂等）", gwUseAPHandler(dep))
	reg(http.MethodGet, "/battles/result", "会战胜利结算", gwResultHandler(dep))
	reg(http.MethodGet, "/battles/lose", "会战败北上报", gwLoseHandler(dep))
}
