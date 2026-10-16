// gacha.go CN3.5 抽卡的领域契约（GACHA350-03/05）。
//
// 与 cn35store 的关系：本文件只声明接口与错误，不依赖 sql；存储在
// cn35store/gacha.go 实现（编译期断言 var _ GachaStore = (*Store)(nil)）。
//
// 幂等边界（务必按事实理解，勿按愿望理解）：
// 客户端 /gachas/execute 的 URL 是硬编码拼接的，只带 id/count/use_scout_book，
// 不含任何客户端生成的唯一量（无 nonce、无随机、无请求时间戳进业务语义；
// APP_T 是秒级服务端时钟，见 JSONRequester.cs）。因此**在不修改客户端 DLL
// 的前提下无法做到密码级严格幂等**——这是已证的事实边界。
//
// 但"无法严格幂等"不等于"只能关闭执行面"。执行面采用**有界重放**模型：
//   - request_digest 是"同参指纹"（账号+池+次数+币种+规则版本），
//     operation_key 由"指纹 + 结算 nonce"派生、每次结算唯一。
//   - 结算前按指纹查最近一次 committed：
//     a) 距其提交在短窗口内 → 判定网络级重发，回放 result_json；
//     b) 其提交后客户端发过 on=gachas_execute 的 reload_data → 判定
//     玩家经错误路径重试（GachaIndexParts.cs:160-164：成功走
//     ShowUnitGetAnimation 不发 reload，失败才 ReloadData），回放。
//   - 其余同参请求一律新结算——合法再抽不被误伤。
//
// 残余风险（已如实标注）：内存中的 reload 信号在服务端重启后丢失，
// 极端场景（结算成功+响应丢失+重启+玩家重试）会多扣一次费；
// 方向宁可少收不可多扣的策略无法覆盖该支，见 gacha_wire.go 实现注释。
package cn35game

import "errors"

var (
	// ErrGachaOperationConflict 同 (账号,操作键) 已存在但请求摘要不同。
	ErrGachaOperationConflict = errors.New("cn35game: 抽卡操作键冲突（同键异参）")
	// ErrGachaOperationMissing 操作键不存在或不属于该账号。
	ErrGachaOperationMissing = errors.New("cn35game: 抽卡操作不存在")
	// ErrGachaPoolUnknown 请求的池不在当前规则中。
	ErrGachaPoolUnknown = errors.New("cn35game: 未知卡池")
	// ErrGachaPoolClosed 池存在但当前不可抽取（未开放/已结束）。
	ErrGachaPoolClosed = errors.New("cn35game: 卡池未开放或已结束")
	// ErrGachaCountInvalid 抽取次数非法（0/负数/超上限）。
	ErrGachaCountInvalid = errors.New("cn35game: 抽取次数非法")
	// ErrGachaInsufficient 余额不足。
	ErrGachaInsufficient = errors.New("cn35game: 余额不足")
	// ErrGachaNoAvailableUnits 池没有任何资源合格的可获得角色。
	ErrGachaNoAvailableUnits = errors.New("cn35game: 卡池没有可获得的角色")
	// ErrGachaUnitTemplateUnknown 中奖模板不在只读目录中。
	ErrGachaUnitTemplateUnknown = errors.New("cn35game: 中奖角色模板不存在")
)

// GachaPity 是保底进度的领域形态。
type GachaPity struct {
	N4                 int
	N5                 int
	FeaturedGuaranteed bool
	RulesVersion       string
}

// GachaDraw 是一次抽取的结算结果（领域形态）。
type GachaDraw struct {
	UnitID           int
	RarityID         int
	Reason           string
	Newcomer         bool
	DuplicatedDelta  int
	N4After          int
	N5After          int
	CompensatedPoint int
}

// GachaLedgerEntry 是一条货币流水。
type GachaLedgerEntry struct {
	Seq          int
	Currency     string
	Delta        int
	Reason       string
	BalanceAfter int
}

// GachaSettlement 是一次抽卡结算的完整输入。
type GachaSettlement struct {
	UserID        string
	OperationKey  string
	RequestDigest string
	PoolID        string
	PityGroup     string
	RulesVersion  string
	DrawCount     int
	Currency      string
	UnitCost      int
	TotalCost     int
	ResultJSON    string
	Pity          GachaPity
	Draws         []GachaDraw
	Ledger        []GachaLedgerEntry
}

// GachaOperationRecord 是已提交操作的读回形态。
type GachaOperationRecord struct {
	OperationID   int64
	OperationKey  string
	RequestDigest string
	PoolID        string
	PityGroup     string
	RulesVersion  string
	DrawCount     int
	Currency      string
	TotalCost     int
	ResultJSON    string
	CreatedAt     string
}

// GachaStore 抽卡持久化写面/读面。
//
// SettleGacha 必须在**单个事务**内完成：归属与幂等检查 → 条件扣费 →
// 库存结算（新卡 INSERT／重复卡累加 duplicated_count／满觉醒补偿）→
// 逐抽记录 → 货币流水 → 保底进度 → 提交。任一失败整体回滚。
type GachaStore interface {
	// GachaPity 读取保底进度；ok=false 表示尚无记录（须懒初始化，不得
	// 依据现有持卡量反推历史抽数）。
	GachaPity(userID, group string) (GachaPity, bool, error)
	// GachaOperationByKey 读取指定操作键的已提交操作；nil 表示未提交。
	GachaOperationByKey(userID, key string) (*GachaOperationRecord, error)
	// GachaOperationByDigest 读取同参指纹下最近一次已提交操作（重放判定
	// 读面）；nil 表示无记录。
	GachaOperationByDigest(userID, digest string) (*GachaOperationRecord, error)
	// GachaRecentOperations 账号最近的抽卡记录。
	GachaRecentOperations(userID string, limit int) ([]GachaOperationRecord, error)
	// GachaDraws 某操作的逐抽记录（按服务端原始顺序，含跨账号拒绝）。
	GachaDraws(userID string, operationID int64) ([]GachaDraw, error)
	// SettleGacha 单事务结算。
	SettleGacha(in GachaSettlement) (*GachaOperationRecord, error)
}
