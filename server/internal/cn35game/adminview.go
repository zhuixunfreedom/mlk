// adminview.go 管理后台（admin-web）数据读面的契约类型与存储接口。
//
// 全部为既有 cn35_* 表的只读投影，供 admin35 /mercd/adminapi/* 使用；
// 不承载写操作（写面仍走 RosterAdminStore/业务事务）。账号敏感列
// （token/sk/password_hash）不出现在任何投影字段中。
package cn35game

// AdminPlayerFilter 玩家列表过滤。
type AdminPlayerFilter struct {
	UID      string // user_id 子串
	Nickname string // user_name 子串
	Page     int    // >=1
	PageSize int    // 缺省由存储层套上限
}

// AdminPlayerRow 玩家列表一行：账号 + 资料 + 货币余额投影。
type AdminPlayerRow struct {
	UserID       string `json:"user_id"`
	Nickname     string `json:"nickname"`
	AccountKind  string `json:"account_kind"` // guest / he / sdk
	Level        int64  `json:"level"`
	Exp          int64  `json:"exp"`
	AP           int64  `json:"ap"`
	BP           int64  `json:"bp"`
	Coin         int64  `json:"coin"`
	GachaPoint   int64  `json:"gacha_point"`
	TutorialDone bool   `json:"tutorial_done"`
	LastServerID string `json:"last_server_id"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// AdminUnitRow 玩家同伴库存行（cn35_player_units ⋈ cn35_unit_templates）。
type AdminUnitRow struct {
	UnitID      int    `json:"unit_id"`
	Name        string `json:"name"`    // 模板缺失时为空串
	Element     int    `json:"element"` // 1炎2水3风4光5暗
	ElementName string `json:"element_name"`
	Rare        int    `json:"rare"`
	Level       int64  `json:"level"`
	Duplicated  int64  `json:"duplicated"`
	Protected   bool   `json:"protected"`
	Newcomer    bool   `json:"newcomer"`
	MonsterUUID string `json:"monster_uuid"`
	OwnerUID    string `json:"owner_uid"`
	OwnerName   string `json:"owner_name"`
}

// AdminMonsterRow 玩家魔宠库存行。
type AdminMonsterRow struct {
	UUID      string `json:"uuid"`
	MonsterID int    `json:"monster_id"`
	UnitID    *int   `json:"unit_id"`
	Level     int64  `json:"level"`
	Protected bool   `json:"protected"`
	OwnerUID  string `json:"owner_uid"`
}

// AdminDeckSlot 编队槽位（含同伴名投影）。
type AdminDeckSlot struct {
	SlotIndex int    `json:"slot_index"`
	UnitID    *int   `json:"unit_id"`
	UnitName  string `json:"unit_name"`
	Level     int64  `json:"level"`
}

// AdminDeckRow 一队编队。
type AdminDeckRow struct {
	PartyID      string          `json:"party_id"`
	DisplayOrder int             `json:"display_order"`
	Slots        []AdminDeckSlot `json:"slots"`
}

// AdminBalanceRow 一条货币余额。
type AdminBalanceRow struct {
	Currency  string `json:"currency"`
	Amount    int64  `json:"amount"`
	UpdatedAt string `json:"updated_at"`
}

// AdminQuestProgress 剧情进度指针（cn35_quest_progress）。
type AdminQuestProgress struct {
	StageID         int64  `json:"stage_id"`
	AreaID          int64  `json:"area_id"`
	QuestID         int64  `json:"quest_id"`
	HardModeStageID int64  `json:"hard_mode_stage_id"`
	UpdatedAt       string `json:"updated_at"`
}

// AdminPlayerDetail 玩家详情聚合（raw 为脱敏后的账号+资料+旗帜原文）。
type AdminPlayerDetail struct {
	Player   AdminPlayerRow      `json:"player"`
	Units    []AdminUnitRow      `json:"units"`
	Monsters []AdminMonsterRow   `json:"monsters"`
	Decks    []AdminDeckRow      `json:"decks"`
	Balances []AdminBalanceRow   `json:"balances"`
	Quest    *AdminQuestProgress `json:"quest"`
	Raw      map[string]any      `json:"raw"`
}

// AdminCharFilter 角色（玩家持有同伴）列表过滤。
type AdminCharFilter struct {
	Name     string // 模板名子串
	UnitID   int    // >0 精确
	UserID   string // 持有者 user_id 子串
	Page     int
	PageSize int
}

// AdminItemFilter 资产/礼物合并视图过滤。Kind: ""=全部 "currency"=余额 "present"=礼物箱。
type AdminItemFilter struct {
	Kind     string
	Name     string // currency 名 / present content_type 子串
	UserID   string
	Page     int
	PageSize int
}

// AdminItemRow 资产/礼物合并视图一行。ID 为合成键：
// bal:<user_id>:<currency> 或 present:<uuid>。
type AdminItemRow struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Count     int64  `json:"count"`
	OwnerUID  string `json:"owner_uid"`
	OwnerName string `json:"owner_name"`
	Note      string `json:"note"`
	UpdatedAt string `json:"updated_at"`
}

// AdminActivityRow 公告/活动窗口（cn35_infos），status 由服务端按当前时间计算。
type AdminActivityRow struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"` // disabled / upcoming / active / ended
	BeginAt int64  `json:"begin_at"`
	EndAt   int64  `json:"end_at"`
	Weight  int64  `json:"weight"`
	BodyLen int    `json:"body_len"`
}

// AdminOverview 管理台总览计数。
type AdminOverview struct {
	Accounts int64 `json:"accounts"`
	Units    int64 `json:"units"`
	Monsters int64 `json:"monsters"`
	Presents int64 `json:"presents"`
	Infos    int64 `json:"infos"`
	GachaOps int64 `json:"gacha_ops"`
	Decks    int64 `json:"decks"`
}

// AdminViewStore 管理后台只读数据面（cn35store.Store 实现）。
// 全部方法只读；空结果返回空切片不算错误。
type AdminViewStore interface {
	AdminOverview() (*AdminOverview, error)
	AdminPlayers(f AdminPlayerFilter) ([]AdminPlayerRow, int, error)
	AdminPlayerDetail(userID string) (*AdminPlayerDetail, error)
	AdminCharacters(f AdminCharFilter) ([]AdminUnitRow, int, error)
	AdminItems(f AdminItemFilter) ([]AdminItemRow, int, error)
	AdminActivities() ([]AdminActivityRow, error)
}
