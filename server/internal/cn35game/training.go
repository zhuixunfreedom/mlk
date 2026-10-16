// training.go STAT350-05A：正常等级训练（/user_units/training）的领域契约。
//
// 状态边界（C0.1，plans/cn35-stat-system-2026-09-23/README.md）：
//   - training **只**修改 cn35_player_units.level；
//   - 不隐式修改 duplicated_count（觉醒，05C）或 upgrade_level（潜能，05B）；
//   - 合法区间 [1, current_max_level(rare, dup)]，current_max_level 由 statmodel 口径给出。
//
// 经济口径：成本 = expTable[after-1] - expTable[before-1]（客户端常量，
// 见 exp_table.go），从 gacha_point 扣减。扣费策略本身是服务端的重建决策，
// 不是原服逐位事实；客户端 UnitLevelupModal 也用 gacha_point 显示需求。
package cn35game

import "errors"

var (
	// ErrTrainingUnitNotFound 会话账号未持有该 unit_id（含跨账号请求）。
	ErrTrainingUnitNotFound = errors.New("cn35game: 训练目标单位不在本账号库存")
	// ErrTrainingStale 请求携带的 before 与当前等级不符（并发/重放）——明确拒绝。
	ErrTrainingStale = errors.New("cn35game: 训练请求已过期（before 与当前等级不符）")
	// ErrTrainingOutOfRange after 不在 (before, current_max_level] 内，或经验表越界。
	ErrTrainingOutOfRange = errors.New("cn35game: 训练目标等级越界")
	// ErrTrainingPoint gacha_point 余额不足。
	ErrTrainingPoint = errors.New("cn35game: gacha_point 不足")
)

// UnitTrainingStore 正常等级训练写面（STAT350-05A）。
//
// 实现必须在**单个事务**内完成：归属与存在校验 → 陈旧校验（level==before）
// → 区间校验（after<=maxLevel）→ 余额条件扣减 → 更新 level。任一失败整体
// 回滚，不产生部分写入。返回扣费后的 gacha_point 余额。
type UnitTrainingStore interface {
	TrainUnit(userID string, unitID, before, after, maxLevel int) (int64, error)
}
