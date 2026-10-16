// grant.go PLAY350-08：五卡礼包的事务发放编排。
//
// 顺序（单 tx，BEGIN→先取写锁）：
//  1. 无写 UPDATE 抢占写者序列——DEFERRED 事务的第一个语句即写，
//     使并发发放串行：后者阻塞至前者提交，再以新快照读到台账。
//  2. 账号存在性校验（不存在 → ErrGrantTarget，零写入回滚）。
//  3. 幂等比对：台账已应用集==配置集 → 原样返回（Idempotent）；
//     非空且不等 → ErrGrantVersionConflict（拒绝再发/覆盖）。
//  4. 已合法拥有的模板跳过插入（不覆盖既有库存行），其余插入；
//     每卡登记 cn35_inventory_grants 台账行。
//  5. 队长修正：leader_unit_id 指向未拥有模板（含发放后仍悬空）
//     → 置为 cfg.LeaderUnitID；已指向拥有单位则不动。
//  6. 提交；任一步失败整体回滚——失败零部分发放。
package cn35store

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// GrantFiveStar 对 userID 幂等执行五卡礼包发放（grant_key 固定）。
// 返回完整结果（含五卡实例键=模板 ID）；幂等重放返回原结果。
func (s *Store) GrantFiveStar(userID string, cfg *cn35game.FiveStarConfig,
	cat *cardcatalog.Catalog) (*cn35game.GrantResult, error) {
	if err := cn35game.ValidateFiveStar(cat, cfg); err != nil {
		return nil, err
	}
	want := cfg.UnitIDs()
	res := &cn35game.GrantResult{UserID: userID, GrantKey: cn35game.FiveStarGrantKey,
		UnitIDs: want, LeaderUnitID: cfg.LeaderUnitID}
	err := s.withTx(func(tx *sql.Tx) error {
		// 1. 抢占写者序列（并发串行点）。
		if _, err := tx.Exec(`UPDATE cn35_player_profiles SET updated_at=updated_at WHERE user_id=?`, userID); err != nil {
			return fmt.Errorf("cn35store: 发放锁序失败: %w", err)
		}
		// 2. 账号存在性。
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: 账号不存在 user_id=%s", cn35game.ErrGrantTarget, userID)
		}
		// 3. 幂等比对。
		applied, err := GrantAppliedTx(tx, userID, cn35game.FiveStarGrantKey)
		if err != nil {
			return err
		}
		if len(applied) > 0 {
			if sameIDSet(applied, want) {
				res.Idempotent = true
				res.AlreadyOwned = want
				return s.finishGrantResult(tx, res)
			}
			return fmt.Errorf("%w: grant_key=%s 已应用集 %v ≠ 配置集 %v",
				cn35game.ErrGrantVersionConflict, cn35game.FiveStarGrantKey, keys(applied), want)
		}
		// 4. 逐卡：已拥有跳过插入（保留原行），否则按 initial_level 插入；登记台账。
		for _, u := range cfg.Units {
			var owned int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=? AND unit_id=?`,
				userID, u.ID).Scan(&owned); err != nil {
				return err
			}
			if owned > 0 {
				res.AlreadyOwned = append(res.AlreadyOwned, u.ID)
			} else {
				if err := InsertUnitTx(tx, userID, cn35game.UnitRecord{
					UnitID: u.ID, Level: u.InitialLevel,
				}); err != nil {
					return err
				}
				res.Inserted = append(res.Inserted, u.ID)
			}
			if err := RecordGrantTx(tx, userID, cn35game.FiveStarGrantKey, u.ID); err != nil {
				return err
			}
		}
		return s.finishGrantResult(tx, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// finishGrantResult 队长引用修正（仅当当前 leader 发放后仍悬空）。
// 幂等路径同样走这里：并发发放与重放返回一致结果。
func (s *Store) finishGrantResult(tx *sql.Tx, res *cn35game.GrantResult) error {
	var leader string
	if err := tx.QueryRow(`SELECT leader_unit_id FROM cn35_player_profiles WHERE user_id=?`,
		res.UserID).Scan(&leader); err != nil {
		return err
	}
	lid, _ := strconv.Atoi(leader)
	var owned int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=? AND unit_id=?`,
		res.UserID, lid).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		res.LeaderUnitID = lid
		return nil
	}
	if _, err := tx.Exec(`UPDATE cn35_player_profiles SET leader_unit_id=?,updated_at=? WHERE user_id=?`,
		strconv.Itoa(res.LeaderUnitID), nowUTC(), res.UserID); err != nil {
		return err
	}
	res.LeaderChanged = true
	return nil
}

// GrantStatus 只读视图：某账号对该来源键的台账应用集（供 CLI 检查）。
func (s *Store) GrantStatus(userID, grantKey string) (map[int]bool, error) {
	var out map[int]bool
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		out, err = GrantAppliedTx(tx, userID, grantKey)
		return err
	})
	return out, err
}

// EnsureDevAccount 专用开发账号初始化：固定 deviceID 游客域幂等建号，
// 返回其 user_id（重复调用同号）。deviceID 由调用方显式给出，
// 不默认任何既有账号。账号域约束（游客解析/建号种子）全复用 QuickLogin。
func (s *Store) EnsureDevAccount(deviceID string) (userID string, isNew bool, err error) {
	res, err := s.QuickLogin(deviceID)
	if err != nil {
		return "", false, err
	}
	err = s.withTx(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT user_id FROM cn35_accounts WHERE he_account_id=?`,
			res.HeAccountID).Scan(&userID)
	})
	if err != nil {
		return "", false, fmt.Errorf("cn35store: dev 账号解析失败: %w", err)
	}
	return userID, res.New, nil
}

func sameIDSet(applied map[int]bool, want []int) bool {
	if len(applied) != len(want) {
		return false
	}
	for _, id := range want {
		if !applied[id] {
			return false
		}
	}
	return true
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
