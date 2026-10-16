package apidoc

import "strings"

// routeDetail 是旧协议的显式兼容契约；不扫描 note 猜测参数。
// 新增 JSON 接口应优先使用 Huma 类型化 Input/Output。
type routeDetail struct {
	Query               []param
	Form                []param
	Fixture             string // serve_*.json 资产文件名（相对 AssetBase）
	OctetStream         bool   // 二进制帧端点（/account、/cn35/codec-echo）
	Doc                 string // 契约/证据引用文本
	ResponseContentType string
	Encrypted           bool
}

type param struct{ Name, Desc string }

func detailFor(path string) routeDetail {
	d, ok := routeDetails[path]
	if !ok {
		d, ok = routeDetails[strings.TrimSuffix(path, "/")]
	}
	if !ok {
		d = routeDetails[path+"/"]
	}
	if d.OctetStream {
		d.ResponseContentType = "application/octet-stream"
	}
	if path == "/mercd/admin" {
		d.ResponseContentType = "text/html"
	}
	if strings.HasPrefix(path, "/mercd/admin/") || path == "/mercd/cn35/admin/page-editor" {
		d.ResponseContentType = "text/html"
	}
	return d
}

var routeDetails = map[string]routeDetail{
	"/mercd/cn35/admin/page-editor/api/page":      {Query: []param{{Name: "name", Desc: "页面名称"}}},
	"/mercd/cn35/admin/page-editor/api/versions":  {Query: []param{{Name: "limit", Desc: "归档列表条数"}}},
	"/mercd/cn35/admin/page-editor/api/version":   {Query: []param{{Name: "id", Desc: "归档版本 id"}}},
	"/mercd/cn35/admin/page-editor/api/ref-image": {Query: []param{{Name: "name", Desc: "参考图文件名"}}, ResponseContentType: "application/octet-stream"},
	"/areas":  {Query: []param{{Name: "stage_id", Desc: "主线区域读取的 stage id"}}},
	"/quests": {Query: []param{{Name: "area_id", Desc: "区域关卡读取的 area id"}}},
	"/quests/current_quest_additional_option": {Query: []param{{Name: "quest_id", Desc: "关卡 id"}}},
	"/user_units/set_leader":                  {Query: []param{{Name: "id", Desc: "同伴模板 id"}}},
	"/user_units/set_helper":                  {Query: []param{{Name: "id", Desc: "同伴模板 id"}}},
	"/user_units/protect":                     {Query: []param{{Name: "unit_id", Desc: "同伴模板 id"}, {Name: "protected", Desc: "保护标记"}}},
	"/user_monsters/off":                      {Query: []param{{Name: "uuid", Desc: "魔宠实例 id"}, {Name: "unit_id", Desc: "当前宿主同伴 id"}}},
	"/user_monsters/protect":                  {Query: []param{{Name: "uuids", Desc: "魔宠实例 id 列表；按现有 handler 解析"}}},
	"/mercd/cn35/admin/audit":                 {Query: []param{{Name: "limit", Desc: "返回条数"}}},
	"/mercd/cn35/admin/unit/template":         {Query: []param{{Name: "id", Desc: "模板 id"}}},
	"/mercd/cn35/admin/version/members":       {Query: []param{{Name: "version", Desc: "配置版本"}}},
	"/mercd/cn35/admin/versions":              {Query: []param{{Name: "limit", Desc: "返回条数"}}},
	// /mercd/adminapi/* 管理后台数据接口（web authkey 鉴权，恒 fail-closed）。
	"/mercd/adminapi/ping": {Doc: "鉴权自检；响应为类型化 pingResponse"},
	"/mercd/adminapi/overview": {
		Doc: "账号/同伴/魔宠/礼物/公告/抽卡操作/编队计数聚合",
	},
	"/mercd/adminapi/players": {
		Query: []param{
			{Name: "uid", Desc: "user_id 子串"},
			{Name: "nickname", Desc: "user_name 子串"},
			{Name: "page", Desc: "页码（>=1，缺省 1）"},
			{Name: "page_size", Desc: "每页条数（缺省 50，上限 200）"},
		},
	},
	"/mercd/adminapi/player": {
		Query: []param{{Name: "user_id", Desc: "玩家 user_id（必填）"}},
		Doc:   "账号行已脱敏（token/sk/password_hash 不下发）",
	},
	"/mercd/adminapi/characters": {
		Query: []param{
			{Name: "name", Desc: "模板名子串"},
			{Name: "unit_id", Desc: "同伴模板 id 精确匹配"},
			{Name: "user_id", Desc: "持有者 user_id（精确）"},
			{Name: "page", Desc: "页码（>=1，缺省 1）"},
			{Name: "page_size", Desc: "每页条数（缺省 50，上限 200）"},
		},
	},
	"/mercd/adminapi/items": {
		Query: []param{
			{Name: "kind", Desc: "currency=货币余额 / present=礼物箱（缺省全部）"},
			{Name: "name", Desc: "currency 名或 content_type 子串"},
			{Name: "user_id", Desc: "持有者 user_id 子串"},
			{Name: "page", Desc: "页码（>=1，缺省 1）"},
			{Name: "page_size", Desc: "每页条数（缺省 50，上限 200）"},
		},
		Doc: "货币余额与礼物箱的合并视图；行 id 为合成键 bal:<uid>:<cur> / present:<uuid>",
	},
	"/mercd/adminapi/activities": {
		Query: []param{
			{Name: "status", Desc: "disabled|upcoming|active|ended（缺省全部）"},
			{Name: "page", Desc: "页码（>=1，缺省 1）"},
			{Name: "page_size", Desc: "每页条数（缺省 50，上限 200）"},
		},
	},
	"/mercd/adminapi/api-logs": {
		Query: []param{
			{Name: "path", Desc: "path 子串过滤"},
			{Name: "status", Desc: "HTTP 状态码精确匹配"},
			{Name: "page", Desc: "页码（>=1，缺省 1）"},
			{Name: "page_size", Desc: "每页条数（缺省 50）"},
		},
		Doc: "真源为 state 目录 requests.ndjson；尾部窗口（8MiB）扫描，window_truncated 标记截断",
	},
	"/asset_info_android4_v2_short.json.encode": {ResponseContentType: "application/octet-stream"},
	"/toto_image_4/unity/":                      {ResponseContentType: "application/octet-stream"},
	"/account": {
		OctetStream: true,
		Query: []param{
			{Name: "uid", Desc: "固定 666666（客户端启动链硬编码）"},
		},
		Doc: "docs/协议对接/cn350/contracts/00-transport.md",
	},
	"/cn35/codec-echo": {
		OctetStream: true,
		Doc:         "编解码自检：回包每个 method 的 retCode=200",
	},
	"/asset_info_android.json.encode": {
		Fixture: "serve_asset_info_android.json",
		Query:   []param{{Name: "version", Desc: "客户端附带的时间戳参数，服务端忽略"}},
	},
	"/app_conf_android.json.encode": {
		Fixture:             "serve_app_conf_android.json",
		ResponseContentType: "application/octet-stream",
		Query:               []param{{Name: "version", Desc: "客户端附带的时间戳参数，服务端忽略"}},
	},
	"/users/preset_data.json": {
		Fixture:   "serve_preset_data.json",
		Encrypted: true,
		Doc:       "data.user/data.user_coin 由当前账号数据库快照注入；会话未绑定/无快照时返回错误",
	},
	"/users/update": {
		Form: []param{
			{Name: "user_name", Desc: "Story1 提交的昵称"},
			{Name: "tutorial_finish", Desc: "教程完成标记（true/1/yes/on 为真）"},
		},
	},
	"/users/messages": {
		Query: []param{
			{Name: "e", Desc: "AES 加密的 messageQuery（含 last_read_at/temp_user_id 等），见 decodeQuery"},
		},
		Doc: "契约卡 d173191a:docs/协议对接/契约卡/users-messages.md",
	},
	"/users/show": {
		Query: []param{
			{Name: "invitation_code", Desc: "邀请码查询分支"},
		},
		Doc: "契约卡 d173191a:docs/协议对接/契约卡/users-show.md",
	},
	"/users/reload_data": {
		Query: []param{{Name: "on", Desc: "缓存刷新目标（如 gachas_execute）"}},
	},
	"/users/help": {
		Query: []param{{Name: "help_id", Desc: "救援消息 id"}},
	},
	"/quests/execute/": {
		Query: []param{
			{Name: "mode", Desc: "quest 为主线图模式"},
			{Name: "id", Desc: "quest id"},
			{Name: "page_message", Desc: "PageMessage.data 全部键透传为 query"},
		},
		Doc: "QuestExecuteDocument.cs:157；形状 oracle 为包内 Config/quest0",
	},
	"/stories/read/": {
		Query: []param{{Name: "id", Desc: "story id"}},
		Doc:   "StoryReadDocument.cs:47-60；命中原样下发 story_<id>.json 顶层 story/asset_names/actress_xys（无 data 信封），未命中 404；见 notes/implemented/2026-09-28-cn35-story-reader.md",
	},
	"/chapters/": {
		Doc: "Chapters 页 requireData.chapters；行内 chapter_select_option 深合并下发 Stories 页 requireData/data_key；见 notes/implemented/2026-09-28-cn35-story-reader.md",
	},
	"/stories/list": {
		Query: []param{{Name: "chapter_id", Desc: "章节 id → stories_<id> 缓存键"}},
		Doc:   "Stories 页列表（全部开放，order_num 升序）；见 notes/implemented/2026-09-28-cn35-story-reader.md",
	},
	"/quests/clear": {
		Query: []param{{Name: "time", Desc: "战斗用时（秒），SWFQuestClear 附带"}},
		Doc:   "result_url 目标，按会话幂等",
	},
	"/quests/fail": {
		Query: []param{{Name: "time", Desc: "战斗用时（秒），SWFQuestLose 附带"}},
		Doc:   "fail_url 目标",
	},
	"/quests/log_battle": {
		Form: []param{{Name: "battle_record", Desc: "JSON 字符串战报，服务端仅记账"}},
	},
	"/quests/use_ap": {
		Doc: "ap_use_url 目标，status==success 门禁",
	},
	"/battles/guild_quest_execute.json": {
		Query: []param{{Name: "mode", Desc: "公会战为非 quest 值"}},
	},
	"/user_presents/receive/": {
		Query: []param{{Name: "uuid", Desc: "单领指定礼物；缺省为全领"}},
	},
	"/user_items/use/": {
		Query: []param{
			{Name: "count", Desc: "使用数量"},
			{Name: "boost", Desc: "强化项标记"},
			{Name: "unit_id", Desc: "作用于的 unit"},
		},
	},
	"/user_monsters/on": {
		Query: []param{{Name: "uuid", Desc: "monster uuid"}, {Name: "unit_id", Desc: "挂载目标"}},
	},
	"/user_monsters/powerup": {
		Query: []param{
			{Name: "uuid", Desc: "monster uuid"},
			{Name: "powerup_item_id", Desc: "强化道具"},
			{Name: "count", Desc: "数量"},
		},
	},
	"/user_monsters/sell": {
		Query: []param{{Name: "uuids[]", Desc: "重复参数形式逐个出售"}},
	},
	"/user_monsters/share": {
		Query: []param{{Name: "num", Desc: "分享计数"}, {Name: "uuid", Desc: "monster uuid"}},
	},
	"/user_skills/on": {
		Query: []param{{Name: "uuid", Desc: "skill uuid"}, {Name: "unit_id", Desc: "装备目标"}},
	},
	"/user_skills/off": {
		Query: []param{
			{Name: "uuid", Desc: "单卸"},
			{Name: "all", Desc: "true 时按 unit_id 全卸"},
			{Name: "unit_id", Desc: "全卸目标"},
		},
	},
	"/user_skills/powerup": {
		Query: []param{
			{Name: "uuid", Desc: "skill uuid"},
			{Name: "powerup_item_id", Desc: "强化道具"},
			{Name: "count", Desc: "数量"},
		},
	},
	"/user_skills/sell": {
		Query: []param{{Name: "uuids[]", Desc: "重复参数形式逐个出售"}},
	},
	"/user_units/sell": {
		Query: []param{{Name: "ids[]", Desc: "重复参数形式逐个出售"}},
	},
	"/task/update_task_status": {
		Query: []param{
			{Name: "task_action", Desc: "任务动作标识（取证字段）"},
			{Name: "amount", Desc: "进度增量（取证字段）"},
			{Name: "extend", Desc: "扩展字段（取证字段）"},
		},
	},
	"/guilds/update": {
		Query: []param{
			{Name: "play_type_id", Desc: "游玩方针"},
			{Name: "approval_type_id", Desc: "审批方式"},
		},
		Form: []param{
			{Name: "recruit_text", Desc: "招募文"},
			{Name: "memo", Desc: "公会备忘"},
		},
	},
	"/guilds/set_shifts": {
		Form: []param{{Name: "0..6", Desc: "七个班次位，值 True/False（首字母大写）"}},
	},
	"/guilds/search.json": {
		Doc: "回 data.temp_search_indexes 空列表",
	},
}
