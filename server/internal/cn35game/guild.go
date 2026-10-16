// guild.go GUILD350-02..08：公会组织与公会战领域契约。
//
// 设计真源：plans/cn350-guild-war-2026-10-02/README.md +
// notes/proposed/2026-10-02-cn35-guild-war-revival.md +
// docs/协议对接/cn350/公会战-客户端契约取证.md（客户端事实边界）。
//
// 数值集中在本文件 GuildRules（后续数值 agent 下发的唯一入口）；
// 字段含义与客户端消费证据见取证文档 §4/§5。
package cn35game

import (
	"errors"
	"time"
)

// ---- 错误 ----

var (
	ErrGuildNotFound    = errors.New("cn35game: 公会不存在")
	ErrGuildJoined      = errors.New("cn35game: 已在公会中")
	ErrGuildFull        = errors.New("cn35game: 公会已满员")
	ErrGuildNoMember    = errors.New("cn35game: 未加入公会")
	ErrGuildNoPerm      = errors.New("cn35game: 公会权限不足")
	ErrGuildName        = errors.New("cn35game: 公会名非法（1-8 字符）")
	ErrGuildSubNotFound = errors.New("cn35game: 申请/邀请记录不存在")
	ErrGuildSubDecided  = errors.New("cn35game: 申请已处理")
	ErrGuildAsset       = errors.New("cn35game: 公会资材不足")
	ErrGuildInvest      = errors.New("cn35game: 投资额非法")
	ErrGuildDeckSlot    = errors.New("cn35game: 防卫槽位非法")
	ErrGuildMonster     = errors.New("cn35game: 魔宠未持有或已在防卫卡组")
	ErrGWNoSession      = errors.New("cn35game: 当前不在会战场次")
	ErrGWBP             = errors.New("cn35game: BP 不足")
	ErrGWAttackNotFound = errors.New("cn35game: 会战战斗会话不存在或归属其他账号")
	ErrGWAttackState    = errors.New("cn35game: 会战战斗会话状态不允许该操作")
	ErrGWDifficulty     = errors.New("cn35game: 会战难度非法")
	ErrGWTarget         = errors.New("cn35game: 会战目标非法（不在同组/为己方）")
	ErrGWNoDefense      = errors.New("cn35game: 目标公会未设置防卫")
	ErrGuildEquipMax    = errors.New("cn35game: 设施已满级")
	ErrGuildOp          = errors.New("cn35game: 操作幂等键冲突")
)

// ---- 数值集中区（数值 agent 改这里） ----

// GuildField 一条会战时段（客户端 fields 文档行）。
type GuildField struct {
	BeginHM string `json:"begin_hm"`
	EndHM   string `json:"end_hm"`
	BgName  string `json:"bg_name"`
	// 客户端兜底值即下列常量（GuildTop.cs:126-128、GuildHomeIcon.cs:158-160）。
	PrayBP    int `json:"pray_bp"`
	PrayGP    int `json:"pray_gp"`
	HelpBP    int `json:"help_bp"`
	HelpGP    int `json:"help_gp"`
	AttackBP1 int `json:"attack_bp1"`
	AttackBP2 int `json:"attack_bp2"`
	AttackBP3 int `json:"attack_bp3"`
}

// GuildRules 公会域可调数值（纯常量集合；持久化不经此结构）。
var GuildRules = struct {
	// Fields 会战时段表（以 Asia/Shanghai 本地 HH:MM:SS 判定）。
	Fields []GuildField
	// MaxMembers 公会人数上限（与客户端 users_map.Count<20 判定同值）。
	MaxMembers int
	// GroupSize 单场会战匹配公会数上限。
	GroupSize int
	// Colors 组内公会显示色（battle_condition.guilds[].color，客户端
	// ColoredName 按该值拼 [color]name[-]）。
	Colors []string
	// InvestCostPerPoint 设施投资换算（value 资材 → point）。
	InvestCostPerPoint int64
	// EquipMaxLevel 设施等级上限；EquipPointPerLevel 每级累计 point。
	EquipMaxLevel      int
	EquipPointPerLevel int64
	// AttackGPWin/Lose 胜/负场公会 GP 入账（占位数值）。
	AttackGPWin  int64
	AttackGPLose int64
	// DifficultyHpBP/AtBP 难度面板千分比（normal/hard/very_hard）。
	DifficultyHpBP map[string]int
	DifficultyAtBP map[string]int
	// 防卫魔宠面板：hp = DefenseHPBase*level*DefenseHPPerLevel 加成系数。
	DefenseHPBase     int
	DefenseATBase     int
	DefenseHPPerLevel float64
	// RewardGuildAsset 胜场公会资材奖励（占位）。
	RewardGuildAsset int
	// DifficultyPlunderRate 各难度胜场掠夺比例：对被击败目标的当场
	// session GP 按比例转移给胜者（基数为对方当前 gp，下限 0）。
	// 口径（需求方 2026-10-09）：普通 0.75%、困难 1.5%、极难 2%。
	DifficultyPlunderRate map[string]float64
	// DifficultyWaveIntervalSec 防守魔宠（大怪）相邻出场间隔秒，
	// 写进 boss 文档 interval_sec（客户端 WatchBattleBossAppearTable
	// 消费，Quest.cs:1526）。口径：普通 120s、困难 80s、极难 40s。
	DifficultyWaveIntervalSec map[string]int
	// DifficultyTrashTotal 单场小怪出场条数。极难 155-160 为需求方
	// 实机观测口径；普通/困难为占位值，数值 agent 下发真值。
	DifficultyTrashTotal map[string]int
	// DifficultyIntervalLabel/RateLabel consts.battle_difficulty
	// 展示文案（BattleQuestSelectParts 只读串："{interval}\nGP {rate}%"）。
	DifficultyIntervalLabel map[string]string
	DifficultyRateLabel     map[string]string
	// BattleBossTotal 单场投放的强化防卫魔宠（大怪）总数上限；
	// boss_appear_table 每 1-2 只一波连续出场，全部击杀即 Clear
	// （Quest.cs OnBattleBossDead→Clear）。
	BattleBossTotal int
	// TrashMonsterIDs 小怪模板池（cardcatalog id，循环出场）。
	TrashMonsterIDs []int
	// 以下效果系复建规则，非已证国服原值；依据见 2026-10-03 Note。
	// HelpSP/HelpGP 口径 2026-10-09：一次支援入场 SP +400、GP +1000。
	// PrayHPBonus/PrayATBonus 口径同上：每次祈祷防御魔宠体力/攻击
	// 各 +2%。
	HelpSP                   int
	PrayHPBonus, PrayATBonus float64
	EquipSPPerLevel          int
	EquipUnitBonusPerLevel   float64
}{
	// bg_name 须为客户端已打包素材：common/battle-field_{morning,noon,
	// evening,night}（decoded/assets/unity/common/，实机验证
	// common/guild_battle_bg 报 Unknown asset）。
	Fields: []GuildField{
		{BeginHM: "12:00:00", EndHM: "13:00:00", BgName: "common/battle-field_noon",
			PrayBP: 15, PrayGP: 400, HelpBP: 25, HelpGP: 1000,
			AttackBP1: 30, AttackBP2: 50, AttackBP3: 70},
		{BeginHM: "20:00:00", EndHM: "21:00:00", BgName: "common/battle-field_night",
			PrayBP: 15, PrayGP: 400, HelpBP: 25, HelpGP: 1000,
			AttackBP1: 30, AttackBP2: 50, AttackBP3: 70},
	},
	MaxMembers:                20,
	GroupSize:                 4,
	Colors:                    []string{"ff4848", "4863ff", "48a24f", "a848b0"},
	InvestCostPerPoint:        1,
	EquipMaxLevel:             10,
	EquipPointPerLevel:        100,
	AttackGPWin:               100,
	AttackGPLose:              20,
	DifficultyHpBP:            map[string]int{"normal": 1000, "hard": 1500, "very_hard": 2000},
	DifficultyAtBP:            map[string]int{"normal": 1000, "hard": 1300, "very_hard": 1700},
	DefenseHPBase:             500,
	DefenseATBase:             100,
	DefenseHPPerLevel:         0.05,
	RewardGuildAsset:          10,
	DifficultyPlunderRate:     map[string]float64{"normal": 0.0075, "hard": 0.015, "very_hard": 0.02},
	DifficultyWaveIntervalSec: map[string]int{"normal": 120, "hard": 80, "very_hard": 40},
	DifficultyTrashTotal:      map[string]int{"normal": 80, "hard": 120, "very_hard": 158},
	DifficultyIntervalLabel:   map[string]string{"normal": "120", "hard": "80", "very_hard": "40"},
	DifficultyRateLabel:       map[string]string{"normal": "0.75", "hard": "1.5", "very_hard": "2"},
	BattleBossTotal:           10,
	TrashMonsterIDs:           []int{159, 226, 46, 305, 22, 113, 18, 10},
	HelpSP:                    400,
	PrayHPBonus:               0.02, PrayATBonus: 0.02,
	EquipSPPerLevel:        25,
	EquipUnitBonusPerLevel: 0.0025,
}

// GuildFieldIndex 当前时间所在场次下标；不在窗口内返回 -1。
// now 须为 Asia/Shanghai 本地时间（store 侧换算）。
func GuildFieldIndex(now time.Time) int {
	hm := now.Format("15:04:05")
	for i, f := range GuildRules.Fields {
		if f.BeginHM <= hm && hm < f.EndHM {
			return i
		}
	}
	return -1
}

// AttackBPCost 按选中目标数取 BP 消耗（1/2/3+ → attack_bp1/2/3）。
func (f GuildField) AttackBPCost(targets int) int {
	switch {
	case targets <= 1:
		return f.AttackBP1
	case targets == 2:
		return f.AttackBP2
	default:
		return f.AttackBP3
	}
}

// ---- 领域类型 ----

// Guild 公会行投影。
type Guild struct {
	ID                string
	Name              string
	MasterName        string
	PlayTypeID        string
	ApprovalTypeID    string
	RecruitText       string
	Memo              string
	Shifts            [7]bool
	BattleSettingText string
	Rating            int64
	RankID            string
	Point             int64 // 本日会战累计 GP
	MemberCount       int
}

// GuildMember 成员行投影。
type GuildMember struct {
	UserID       string
	Name         string
	Role         int // 1 成员 2 副长 3 会长
	Level        int64
	LeaderUnitID string
	JoinedAt     string
}

// GuildEquipment 设施行投影。
type GuildEquipment struct {
	TypeID string
	Level  int
	Point  int64
}

// GuildSubscription 申请/邀请行投影。
type GuildSubscription struct {
	SubID    string
	GuildID  string
	UserID   string
	UserName string
	Level    int64
	Kind     string // apply / invite
	Status   string // pending/approved/rejected/cancelled
}

// GuildView 公会读面聚合（/guilds/、/guilds/show 共用）。
type GuildView struct {
	Guild         *Guild // nil = 未入会/不存在
	MyRole        int    // 查询者在公会中的职位（0=非成员）
	Members       []GuildMember
	Equipments    map[string]*GuildEquipment
	Subscriptions []GuildSubscription // 待审批 apply（仅 MyGuild 视图）
}

// GuildUpdateInput /guilds/update 参数。
type GuildUpdateInput struct {
	PlayTypeID     *string
	ApprovalTypeID *string
	RecruitText    *string
	Memo           *string
}

// GWDefenseSlot 防卫卡组一槽。
type GWDefenseSlot struct {
	Slot        int
	MonsterUUID string
	MonsterID   int
	Level       int
	Batch       int
}

// GWSession 会战场次。
type GWSession struct {
	SessionID  string
	DateKey    string
	FieldIndex int
	GroupKey   string
	Status     string // open/closed/settled
}

// GWParticipant 场次内公会投影。
type GWParticipant struct {
	GuildID     string
	Name        string
	Color       string
	RankID      string
	GP          int64
	RankInGroup int
	Joined      bool // 查询者公会标记
}

// GWView 会战读面聚合（battle_condition 数据源）。
type GWView struct {
	Session              *GWSession // nil = 不在场次
	FieldIndex           int
	Actable              bool
	Participants         []GWParticipant
	Combo                int
	Power                int
	SeedPowerup          float64
	HelpCount, PrayCount int
}

// GWAttack 会战战斗会话。
type GWAttack struct {
	BattleID        string
	SessionID       string
	UserID          string
	GuildID         string
	TargetGuildID   string
	Difficulty      string
	Status          string
	BPCost          int
	DefenseSnapshot []GWDefenseMonster // 开战时冻结的目标防卫
	PartyJSON       string
	TargetGuildIDs  []string
	Effects         GWEffects
	RequestJSON     string
}

// GWEffects 开战冻结的己方设施及 SP。零值兼容 0028 旧攻击。
type GWEffects struct {
	Version                       int `json:"version"`
	InitialSP                     int `json:"initial_sp"`
	UnitHP, UnitAT, UnitToughness float64
}

// GWDefenseMonster 防卫快照单条。
type GWDefenseMonster struct {
	OwnerUserID string  `json:"owner"`
	Slot        int     `json:"slot"`
	MonsterUUID string  `json:"uuid"`
	MonsterID   int     `json:"monster_id"`
	Level       int     `json:"level"`
	GuildID     string  `json:"guild_id,omitempty"`
	HPBonus     float64 `json:"hp_bonus,omitempty"`
	ATBonus     float64 `json:"at_bonus,omitempty"`
}

// GWSettleResult 会战结算回执。
type GWSettleResult struct {
	Attack    *GWAttack
	Already   bool
	Win       bool
	GPDelta   int64 // 固定胜场 GP
	Plundered int64 // 从被击败目标当场 GP 掠夺的额度
	Rewards   []BattleReward
}

// ---- GuildStore 读写面（cn35store.Store 实现） ----

type GuildStore interface {
	// 读面
	MyGuild(userID string) (*GuildView, error)
	GuildShow(guildID string) (*GuildView, error)
	SearchGuilds(keyword, playTypeID, approvalTypeID string) ([]Guild, error)
	DefenseOf(userID string) ([]GWDefenseSlot, error)
	DefenseSnapshot(guildID string) ([]GWDefenseMonster, error)
	// 组织写面（opKey 幂等）
	CreateGuild(userID, name, playTypeID, approvalTypeID, recruitText, opKey string) (*Guild, error)
	JoinGuild(userID, guildID, opKey string) (autoJoined bool, err error)
	DecideSubscription(actorID, subID string, approve bool, opKey string) error
	RejectAllSubscriptions(actorID, opKey string) error
	Dropout(actorID, targetUserID, opKey string) error
	ChangeRole(actorID, targetUserID string, role int, opKey string) error
	UpdateGuild(actorID string, in GuildUpdateInput, opKey string) error
	SetShifts(actorID string, shifts [7]bool, opKey string) error
	Invest(userID, typeID string, value int64, opKey string) (*GuildView, error)
	SetDefense(userID string, slot int, monsterUUID, opKey string) error
	RemoveDefense(userID, monsterUUID, opKey string) error
	// 会战
	GWState(userID string, now time.Time) (*GWView, error)
	GWHelpPray(userID, action, opKey string, now time.Time) (gp int64, bpCost int, err error)
	// GWAttackOpen 开战：事务内校验时窗/同组目标/BP 并冻结防卫快照；
	// 返回参战单位与装备魔宠（resolvePartyTx 同事务）。
	GWAttackOpen(userID, targetGuildID string, extraTargets []string, difficulty string,
		ordinal, unitLimit int, opKey string, now time.Time) (
		*GWAttack, []UnitRecord, []MonsterRecord, error)
	GWAttackPay(userID, battleID string) (*GWAttack, error)
	GWAttackSettle(userID, battleID string, win bool, reportTime int64) (*GWSettleResult, error)
	GWAttackSession(userID, battleID string) (*GWAttack, error)
	GWRankings() ([]Guild, error)
}
