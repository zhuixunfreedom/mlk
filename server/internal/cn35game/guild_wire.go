// guild_wire.go GUILD350-02..09：公会/公会战 wire 面。
//
// 字段真源：docs/协议对接/cn350/公会战-客户端契约取证.md。
// 数值统一读 cn35game.GuildRules；无国服证据的键一律带〔推断〕注释。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// guildRoleNames 公会职位（client Role 枚举：1 User/2 SubMaster/3 Master）。
const (
	GuildRoleMember    = 1
	GuildRoleSubMaster = 2
	GuildRoleMaster    = 3
)

// guildModal 校验失败响应（复用剧情弹窗协议）。
func guildModal(title, body string) map[string]any { return questModal(title, body) }

// ---- 文档组装 ----

// guildMemberDoc 成员行文档（GuildMemberListRow.cs 消费键）。
func guildMemberDoc(m GuildMember) map[string]any {
	return map[string]any{
		"id":                     m.UserID,
		"name":                   m.Name,
		"guild_id":               "", // 行内不携带本公会 id（客户端用 user.guild_id）
		"guild_role":             map[string]any{"id": m.Role},
		"level":                  m.Level,
		"name_and_role":          fmt.Sprintf("%s Lv.%d", m.Name, m.Level),
		"user_status":            "",
		"square_icon_asset_name": fmt.Sprintf("unit/unit_%s", orEmpty(m.LeaderUnitID, "0")),
		"user_detail_message":    "",
		"updated_at_i":           0,
		// GuildMembers 行 last_login 位（niceTime）：暂无登录表，以入会时间代。
		"logged_at": m.JoinedAt,
	}
}

func orEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// equipmentDoc guild.equipments.<type_id>（GuildEquipmentListRows.cs 消费）。
// 未证键（effect/description/point 标签文案）按已证形状占位。
func equipmentDoc(typeID string, e *GuildEquipment) map[string]any {
	level, point := 0, int64(0)
	if e != nil {
		level, point = e.Level, e.Point
	}
	until := GuildRules.EquipPointPerLevel*(int64(level)+1) - point
	if level >= GuildRules.EquipMaxLevel {
		until = 0
	}
	return map[string]any{
		"asset_name":  fmt.Sprintf("guild_equipment/%s", typeID),
		"name":        typeID,
		"description": "",
		"effect":      "",
		"point":       point,
		"current": map[string]any{
			"level": strconv.Itoa(level),
			"until": until,
			"end":   level >= GuildRules.EquipMaxLevel,
		},
	}
}

var guildEquipmentTypeIDs = []string{"hp", "sp", "taghness", "at"}

// guildDoc data.guild 文档（取证 §2）。
func guildDoc(view *GuildView) map[string]any {
	g := view.Guild
	usersMap := map[string]any{}
	for _, m := range view.Members {
		usersMap[m.UserID] = guildMemberDoc(m)
	}
	eq := map[string]any{}
	for _, tid := range guildEquipmentTypeIDs {
		eq[tid] = equipmentDoc(tid, view.Equipments[tid])
	}
	return map[string]any{
		"id":                  g.ID,
		"name":                g.Name,
		"master_name":         g.MasterName,
		"users_map":           usersMap,
		"rating":              g.Rating,
		"rank_id":             g.RankID,
		"guild_ranking_point": g.Point,
		"guild_ranking_rank":  "-",
		"equipments":          eq,
	}
}

// searchIndexDoc /guilds/search_index 与 show 的 search_index_temp。
// auto_approval=免审直进（暂定：approval_type_id ∈ {"", "1"}）。
func searchIndexDoc(g *Guild) map[string]any {
	auto := g.ApprovalTypeID == "" || g.ApprovalTypeID == "1"
	shifts := map[string]any{}
	for i := 0; i < 7; i++ {
		shifts[strconv.Itoa(i)] = g.Shifts[i]
	}
	return map[string]any{
		"play_type":           map[string]any{"name": g.PlayTypeID},
		"approval_type":       map[string]any{"name": g.ApprovalTypeID},
		"auto_approval":       auto,
		"battle_setting_text": g.BattleSettingText,
		"shifts":              shifts,
		"recruit_text":        g.RecruitText,
	}
}

// guildTempDoc 出边信息页用公会简档（guild_temp / 搜索结果行）。
func guildTempDoc(g *Guild) map[string]any {
	auto := g.ApprovalTypeID == "" || g.ApprovalTypeID == "1"
	return map[string]any{
		"id": g.ID, "name": g.Name, "master_name": g.MasterName,
		"rating": g.Rating, "rank_id": g.RankID,
		"guild_ranking_point": g.Point, "guild_ranking_rank": "-",
		"users_map":    map[string]any{}, // 成员数经 member_count 下发行
		"member_count": g.MemberCount,
		// GuildSearchIndexListPartial 行键（实机验证：缺则行渲染空白、加入死链）。
		"guild_id":               g.ID,
		"master_user_name":       g.MasterName,
		"users_count":            g.MemberCount,
		"play_type_name":         g.PlayTypeID,
		"approval_type_name":     g.ApprovalTypeID,
		"auto_approval":          auto,
		"confirm_text":           "加入「" + g.Name + "」公会吗？",
		"join_approve_url":       "/guilds/join?id=" + g.ID,
		"square_icon_asset_name": "",
		"user_detail_message":    "",
		"description_conf":       map[string]any{},
	}
}

// battleConditionDoc data.battle_condition（取证 §3）。
func battleConditionDoc(v *GWView) map[string]any {
	if v == nil || v.Session == nil {
		return map[string]any{}
	}
	guilds := []any{}
	for _, p := range v.Participants {
		guilds = append(guilds, map[string]any{
			"id": p.GuildID, "name": p.Name, "rank_id": p.RankID,
			"ranking": p.RankInGroup, "point": p.GP,
			"color": p.Color, "joined": p.Joined,
		})
	}
	return map[string]any{
		"guilds": guilds,
		"condition": map[string]any{
			"combo":             v.Combo,
			"power":             v.Power,
			"seed_powerup_rate": v.SeedPowerup,
		},
	}
}

// fieldsDocs fields 文档列表（每行挂当日状态：actable/ranking_enable）。
// pray_num/help_num = 本场次全公会祈祷/应援累计次数（当前时窗行下发，
// 服务端台账 cn35_gw_participants.pray_count/help_count）。
// bg_name 仅当前可战时段下发：客户端 CurrentFieldDocument 非时窗回退
// fields[0]，isBattleOpened=bg_name非空+battle_condition非空——全行下发
// 会让休战幕布与战场地图同显（实机已现重叠）。
func fieldsDocs(now time.Time, actable bool, prayNum, helpNum int) []any {
	cur := GuildFieldIndex(now.In(guildWireLoc()))
	out := []any{}
	for i, f := range GuildRules.Fields {
		bg := ""
		pn, hn := 0, 0
		if actable && i == cur {
			bg = f.BgName
			pn, hn = prayNum, helpNum
		}
		out = append(out, map[string]any{
			"begin_hm": f.BeginHM, "end_hm": f.EndHM, "bg_name": bg,
			"actable":        actable && i == cur,
			"ranking_enable": true,
			"pray_bp":        f.PrayBP, "pray_gp": f.PrayGP,
			"help_bp": f.HelpBP, "help_gp": f.HelpGP,
			"attack_bp1": f.AttackBP1, "attack_bp2": f.AttackBP2,
			"attack_bp3": f.AttackBP3,
			"pray_num":   pn, "help_num": hn,
		})
	}
	return out
}

// guildWireLoc 会战判定时区（与 store guildLoc 同值；wire 独立持有避免跨包）。
func guildWireLoc() *time.Location { return time.FixedZone("Asia/Shanghai", 8*3600) }

// battleWeaponRateDoc 武器相性（全 1.0 占位，数值批下发）。
func battleWeaponRateDoc() map[string]any {
	return map[string]any{
		"slash_rate": 1.0, "rush_rate": 1.0, "hit_rate": 1.0,
		"arrow_rate": 1.0, "magic_rate": 1.0, "gun_rate": 1.0,
		"heal_rate": 1.0,
	}
}

// playTypeOptions / approvalTypeOptions 建会/搜索选项（占位枚举）。
func playTypeOptionsDoc() []any {
	return []any{
		map[string]any{"id": "1", "name": "休闲", "value": "1"},
		map[string]any{"id": "2", "name": "普通", "value": "2"},
		map[string]any{"id": "3", "name": "认真", "value": "3"},
	}
}

func approvalTypeOptionsDoc() []any {
	return []any{
		map[string]any{"id": "1", "name": "自由加入", "value": "1"},
		map[string]any{"id": "2", "name": "需要审批", "value": "2"},
	}
}

// ---- 读面 handlers ----

// guildsIndexHandler GET /guilds/：主读面（data.guild + battle_condition +
// 全套辅助键，见取证 §7）。
func guildsIndexHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := guildWireNow(dep)
		gw, err := dep.Guild.GWState(req.UserID, now)
		if err != nil {
			return nil, err
		}
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{
			"user": snap.User,
		}
		if view.Guild != nil {
			data["guild"] = guildDoc(view)
			data["guild_role"] = map[string]any{"id": view.MyRole}
			data["search_index"] = searchIndexDoc(view.Guild)
			data["my_guild_memo"] = view.Guild.Memo
		} else {
			data["guild"] = map[string]any{}
			data["guild_role"] = map[string]any{"id": 0}
			data["my_guild_memo"] = ""
		}
		// GuildMembers 页 ListParts.data_key=guild_members（app_conf 10829 行）：
		// 成员列表行经独立顶层键下发，与 guild.users_map 同源同序。
		members := []any{}
		for _, m := range view.Members {
			members = append(members, guildMemberDoc(m))
		}
		data["guild_members"] = members
		data["battle_condition"] = battleConditionDoc(gw)
		prayNum, helpNum := 0, 0
		if gw != nil {
			prayNum, helpNum = gw.PrayCount, gw.HelpCount
		}
		data["fields"] = fieldsDocs(now, gw != nil && gw.Actable, prayNum, helpNum)
		data["battle_weapon_rate"] = battleWeaponRateDoc()
		data["my_shared_monsters"] = map[string]any{} // 协力战排除（取证 §9）
		data["guild_member_option"] = map[string]any{}
		data["play_type_select_option"] = playTypeOptionsDoc()
		data["approval_type_select_option"] = approvalTypeOptionsDoc()
		et := []any{}
		for _, tid := range guildEquipmentTypeIDs {
			et = append(et, map[string]any{"id": tid})
		}
		data["guild_equipment_types"] = et
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_index",
			"guild": view.Guild != nil,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildDeckHandler GET /guilds/deck：防卫卡组读面。
func guildDeckHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		slots, err := dep.Guild.DefenseOf(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := guildDefenseDeckDoc(dep, slots)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_deck",
			"slots": len(slots),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_deck": deck, "guild_deck_data": guildDeckDataDoc()},
		})
	})
}

// guildDeckDataDoc guild_deck_data 旁路文档：powerup_rate[5] 槽位倍率与
// last_edit_name 为客户端 GuildDeckListParts 必读键，缺失抛
// ArgumentOutOfRangeException 致编队页空白。倍率与槽位加成待数值下发，
// 占位全 1.0。
func guildDeckDataDoc() map[string]any {
	return map[string]any{
		"powerup_rate":   []any{1.0, 1.0, 1.0, 1.0, 1.0},
		"last_edit_name": "",
	}
}

// guildDefenseSlotDoc 防卫槽位行：展示字段同 guild_monsters 魔宠行（名称/
// 属性/数值/贴图），否则已设防槽显示属性 0、无图标。
func guildDefenseSlotDoc(dep Deps, s GWDefenseSlot) (map[string]any, error) {
	m, err := dep.Catalog.Monster(s.MonsterID)
	if err != nil {
		return nil, fmt.Errorf("防卫魔宠模板缺失 %d: %w", s.MonsterID, err)
	}
	doc, err := buildMonsterDoc(MonsterRecord{
		UUID: s.MonsterUUID, MonsterID: s.MonsterID, Level: s.Level,
	}, m)
	if err != nil {
		return nil, err
	}
	doc["batch"] = s.Batch
	return doc, nil
}

// guildDefenseDeckDoc 防卫卡组文档（5 槽 map）。
func guildDefenseDeckDoc(dep Deps, slots []GWDefenseSlot) (map[string]any, error) {
	deck := map[string]any{}
	for _, s := range slots {
		doc, err := guildDefenseSlotDoc(dep, s)
		if err != nil {
			return nil, err
		}
		deck[strconv.Itoa(s.Slot)] = doc
	}
	return deck, nil
}

// guildMonstersHandler GET /guilds/monsters：可选防卫魔宠列表（账号魔宠
// 全量，客户端按 guild_deck 已选排除；batch 占位 0）。
func guildMonstersHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		records, err := dep.Inventory.Monsters(req.UserID)
		if err != nil {
			return nil, err
		}
		list := []any{}
		for _, r := range records {
			m, merr := dep.Catalog.Monster(r.MonsterID)
			if merr != nil {
				return nil, merr
			}
			doc, derr := buildMonsterDoc(r, m)
			if derr != nil {
				return nil, derr
			}
			doc["batch"] = 0
			list = append(list, doc)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_monsters",
			"count": len(list),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_monsters": list},
		})
	})
}

// guildSearchIndexHandler GET /guilds/search_index。
func guildSearchIndexHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{}
		if view.Guild != nil {
			data["search_index"] = searchIndexDoc(view.Guild)
		} else {
			data["search_index"] = map[string]any{}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_search_index",
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildSubscriptionsHandler GET /guilds/subscriptions：待审批申请行
// （行文档带 join_approve_url〔推断 approve_subscription〕+ reject_url）。
func guildSubscriptionsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		rows := []any{}
		for _, sub := range view.Subscriptions {
			rows = append(rows, map[string]any{
				"id":               sub.UserID,
				"sub_id":           sub.SubID,
				"name":             sub.UserName,
				"level":            sub.Level,
				"guild_id":         sub.GuildID,
				"guild_role":       map[string]any{"id": GuildRoleMember},
				"join_approve_url": "/guilds/approve_subscription?sub_id=" + sub.SubID,
				"reject_url":       "/guilds/reject_subscription?sub_id=" + sub.SubID,
			})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_subscriptions",
			"pending": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_subscriptions": rows},
		})
	})
}

// waitingUsersHandler GET /waiting_users：本人发出的申请/受邀列表。
func waitingUsersHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		// 出边：本人 pending apply（申请中）+ 收到的 invite。
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		_ = view
		rows := []any{}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "waiting_users",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_subscriptions": rows},
		})
	})
}

// waitingRecruitHandler GET /waiting_users/recruit：招募检索（挂起的
// waiting 用户）。本批以申请-审批为唯一入会链，返回本人公会简档空态
// + temp_search_indexes=[]（不登记 waiting 池）。
func waitingRecruitHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "waiting_users_recruit",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"temp_search_indexes": []any{}},
		})
	})
}

// guildRankingsHandler GET /guild_rankings。
func guildRankingsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		guilds, err := dep.Guild.GWRankings()
		if err != nil {
			return nil, err
		}
		rows := []any{}
		for i := range guilds {
			g := &guilds[i]
			rows = append(rows, map[string]any{
				"id": g.ID, "name": g.Name, "rank_id": g.RankID,
				"ranking": int(g.Rating), "point": g.Point,
				"member_count": g.MemberCount,
			})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_rankings",
			"count": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_rankings": rows},
		})
	})
}

// ---- 写面 handlers ----

// guildCreateHandler POST /guilds/create.json。
func guildCreateHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		f := req.Form
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_create",
		}
		g, err := dep.Guild.CreateGuild(req.UserID,
			f.Get("name"), f.Get("play_type_id"), f.Get("approval_type_id"),
			f.Get("recruit_text"), f.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		data := map[string]any{"user": snap.User}
		if view, verr := dep.Guild.MyGuild(req.UserID); verr == nil && view.Guild != nil {
			data["guild"] = guildDoc(view)
			data["guild_role"] = map[string]any{"id": view.MyRole}
		}
		data["guild_id"] = g.ID
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildSearchHandler POST /guilds/search.json（?recommend=true 同口）。
func guildSearchHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		f := req.Form
		guilds, err := dep.Guild.SearchGuilds(
			f.Get("keyword"), f.Get("play_type_id"), f.Get("approval_type_id"))
		if err != nil {
			return nil, err
		}
		rows := []any{}
		for i := range guilds {
			rows = append(rows, guildTempDoc(&guilds[i]))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_search",
			"count": len(rows), "recommend": req.Query.Get("recommend"),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"search_index_temp":   map[string]any{},
				"guilds":              rows,
				"temp_search_indexes": rows,
			},
		})
	})
}

// guildShowHandler GET /guilds/show?with_search_index=true&id=
// → search_index_temp + guild_temp（UserPage/GuildDescription 消费）。
func guildShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		gid := req.Query.Get("id")
		view, err := dep.Guild.GuildShow(gid)
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		// guild_temp 详情页入会按钮读 join_approve_url —— 用搜索结果行同一简档。
		data := map[string]any{
			"guild_temp":        guildTempDoc(view.Guild),
			"guild":             guildDoc(view),
			"search_index_temp": searchIndexDoc(view.Guild),
		}
		// 出边页 useTemp 行：本人对该公会的入会/受邀按钮 URL。
		if _, _, merr := guildMemberRole(dep, req.UserID); merr == nil {
			// noop: memberRole 仅内部
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_show", "guild_id": gid,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildMemberRole 内部小助手（占位：wire 不做跨账号成员查询）。
func guildMemberRole(_ Deps, _ string) (string, int, error) { return "", 0, nil }

// guildJoinHandler GET /guilds/join?id=（出边页入会/申请）。
func guildJoinHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		gid := req.Query.Get("id")
		auto, err := dep.Guild.JoinGuild(req.UserID, gid, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_join",
			"guild_id": gid, "auto": auto,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"user": snap.User, "joined": auto},
		})
	})
}

// guildDecideSubHandler 审批/拒绝单条申请。
func guildDecideSubHandler(dep Deps, approve bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		subID := req.Query.Get("sub_id")
		err := dep.Guild.DecideSubscription(req.UserID, subID, approve, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "sub_id": subID, "approve": approve,
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildRejectAllHandler POST /guilds/reject_all_subscription。
func guildRejectAllHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		if err := dep.Guild.RejectAllSubscriptions(req.UserID, req.Form.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildDropoutHandler GET /guilds/dropout（自退/踢人同口）。
func guildDropoutHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		target := req.Query.Get("target_user_id")
		err := dep.Guild.Dropout(req.UserID, target, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_dropout",
			"target": target,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"user": snap.User},
		})
	})
}

// guildChangeRoleHandler 会长/副长任免。
func guildChangeRoleHandler(dep Deps, role int) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		target := req.Query.Get("target_user_id")
		err := dep.Guild.ChangeRole(req.UserID, target, role, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildUpdateHandler /guilds/update（GET ?play_type_id= / POST body）。
func guildUpdateHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		var in GuildUpdateInput
		if v := req.Query.Get("play_type_id"); v != "" {
			in.PlayTypeID = &v
		}
		if v := req.Query.Get("approval_type_id"); v != "" {
			in.ApprovalTypeID = &v
		}
		// 键存在即视为提交——空串是清空文案/留言的合法新值。
		if _, ok := req.Form["recruit_text"]; ok {
			v := req.Form.Get("recruit_text")
			in.RecruitText = &v
		}
		if _, ok := req.Form["memo"]; ok {
			v := req.Form.Get("memo")
			in.Memo = &v
		}
		if err := dep.Guild.UpdateGuild(req.UserID, in, req.Form.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_update",
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildSetShiftsHandler POST /guilds/set_shifts {0..6: bool}。
func guildSetShiftsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		var shifts [7]bool
		for i := 0; i < 7; i++ {
			v := req.Form.Get(strconv.Itoa(i))
			shifts[i] = v == "true" || v == "True" || v == "1"
		}
		if err := dep.Guild.SetShifts(req.UserID, shifts, req.Form.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildInvestHandler GET /guilds/invest?equipment_type_id=&value=。
func guildInvestHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		typeID := req.Query.Get("equipment_type_id")
		value, _ := strconv.ParseInt(req.Query.Get("value"), 10, 64)
		view, err := dep.Guild.Invest(req.UserID, typeID, value, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		data := map[string]any{"user": snap.User}
		if view != nil && view.Guild != nil {
			data["guild"] = guildDoc(view)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_invest",
			"type_id": typeID, "value": value,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildSetDefenseHandler GET /guilds/set_deck?party_index=&uuid=。
func guildSetDefenseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		slot, _ := strconv.Atoi(req.Query.Get("party_index"))
		uuid := req.Query.Get("uuid")
		if err := dep.Guild.SetDefense(req.UserID, slot, uuid, req.Query.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		slots, err := dep.Guild.DefenseOf(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := guildDefenseDeckDoc(dep, slots)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_deck": deck, "guild_deck_data": guildDeckDataDoc()},
		})
	})
}

// guildRemoveDefenseHandler GET /guilds/remove_from_deck?uuid=。
func guildRemoveDefenseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := req.Query.Get("uuid")
		if err := dep.Guild.RemoveDefense(req.UserID, uuid, req.Query.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		slots, err := dep.Guild.DefenseOf(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := guildDefenseDeckDoc(dep, slots)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_deck": deck, "guild_deck_data": guildDeckDataDoc()},
		})
	})
}

// guildPushNotifyHandler /guilds/send_push_notify：推送通知占位（副长以上，
// 无推送通道时回成功——客户端仅做 ResponseFilter 确认）。
func guildPushNotifyHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_send_push_notify",
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildErrModal 错误 → 客户端弹窗映射（200+modal，不裸 4xx）。
func guildErrModal(err error) map[string]any {
	switch {
	case errors.Is(err, ErrGuildName):
		return guildModal("无法创建", "公会名须为 1-8 个字符")
	case errors.Is(err, ErrGuildJoined):
		return guildModal("无法入会", "你已加入公会")
	case errors.Is(err, ErrGuildFull):
		return guildModal("无法入会", "公会已满员")
	case errors.Is(err, ErrGuildNotFound):
		return guildModal("无法操作", "公会不存在")
	case errors.Is(err, ErrGuildNoMember):
		return guildModal("无法操作", "你未加入公会")
	case errors.Is(err, ErrGuildNoPerm):
		return guildModal("无法操作", "公会权限不足")
	case errors.Is(err, ErrGuildSubNotFound):
		return guildModal("无法操作", "申请记录不存在")
	case errors.Is(err, ErrGuildSubDecided):
		return guildModal("无法操作", "该申请已处理")
	case errors.Is(err, ErrGuildInvest):
		return guildModal("无法投资", "投资额非法")
	case errors.Is(err, ErrGuildAsset):
		return guildModal("无法投资", "公会资材不足")
	case errors.Is(err, ErrGuildDeckSlot):
		return guildModal("无法设防", "槽位非法")
	case errors.Is(err, ErrGuildMonster):
		return guildModal("无法设防", "魔宠未持有或已在防卫卡组")
	case errors.Is(err, ErrGWNoSession):
		return guildModal("无法操作", "当前不在会战场次")
	case errors.Is(err, ErrGWBP):
		return guildModal("无法出战", "BP 不足")
	case errors.Is(err, ErrGWAttackNotFound):
		return guildModal("无法战斗", "战斗会话不存在")
	case errors.Is(err, ErrGWAttackState):
		return guildModal("无法战斗", "战斗会话状态不允许")
	case errors.Is(err, ErrGWDifficulty):
		return guildModal("无法战斗", "难度非法")
	case errors.Is(err, ErrGWTarget):
		return guildModal("无法战斗", "目标非法（不在同组或为己方）")
	default:
		return guildModal("无法操作", err.Error())
	}
}

// guildWireNow 注入时钟占位：wire 统一取 dep.GuildClock（未装配→time.Now）。
func guildWireNow(dep Deps) time.Time {
	if dep.GuildClock != nil {
		return dep.GuildClock()
	}
	return time.Now()
}
