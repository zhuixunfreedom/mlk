package cn35store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var ErrStageRosterInventoryDrift = errors.New("cn35store: stage roster 台账与实际库存不一致")

// StageRosterGrantResult 仅描述这次固定版本 20+1 阶段发放；不代表完整名册或满级。
type StageRosterGrantResult struct {
	UserID                   string   `json:"user_id"`
	GrantVersion             string   `json:"grant_version"`
	ContentSHA256            string   `json:"content_sha256"`
	UnitGrantKey             string   `json:"unit_grant_key"`
	MonsterGrantKey          string   `json:"monster_grant_key"`
	UnitIDs                  []int    `json:"unit_ids"`
	MonsterIDs               []int    `json:"monster_ids"`
	InsertedUnitIDs          []int    `json:"inserted_unit_ids"`
	AlreadyOwnedUnitIDs      []int    `json:"already_owned_unit_ids"`
	InsertedMonsterUUIDs     []string `json:"inserted_monster_uuids"`
	AlreadyOwnedMonsterCount int      `json:"already_owned_monster_count"`
	Idempotent               bool     `json:"idempotent"`
}

// GrantStageRoster 对显式 userID 幂等补齐配置锁定的阶段候选。
// 单事务包括新增库存与 unit/monster 两类来源台账；不调整既有行。
func (s *Store) GrantStageRoster(userID string, cfg *cn35game.StageRosterGrantConfig, cat *cardcatalog.Catalog) (*StageRosterGrantResult, error) {
	return s.grantStageRosterWithUUID(userID, cfg, cat, newStageMonsterUUID)
}

func (s *Store) grantStageRosterWithUUID(userID string, cfg *cn35game.StageRosterGrantConfig, cat *cardcatalog.Catalog, newUUID func() (string, error)) (*StageRosterGrantResult, error) {
	if err := cn35game.ValidateStageRosterGrant(cat, cfg); err != nil {
		return nil, err
	}
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id 为空", cn35game.ErrGrantTarget)
	}
	res := &StageRosterGrantResult{
		UserID: userID, GrantVersion: cfg.GrantVersion, ContentSHA256: cfg.ContentSHA256,
		UnitGrantKey: cfg.UnitGrantKey, MonsterGrantKey: cfg.MonsterGrantKey,
		UnitIDs: cfg.CompanionIDs(), MonsterIDs: cfg.PetIDs(),
		InsertedUnitIDs: []int{}, AlreadyOwnedUnitIDs: []int{}, InsertedMonsterUUIDs: []string{},
	}
	err := s.withTx(func(tx *sql.Tx) error {
		return grantStageRosterTx(tx, userID, cfg, newUUID, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func grantStageRosterTx(tx *sql.Tx, userID string, cfg *cn35game.StageRosterGrantConfig, newUUID func() (string, error), res *StageRosterGrantResult) error {
	// This first write serializes concurrent grants before either ledger is read.
	if _, err := tx.Exec(`UPDATE cn35_player_profiles SET updated_at=updated_at WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("cn35store: stage roster 获取事务写序失败: %w", err)
	}
	var accountCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&accountCount); err != nil {
		return err
	}
	if accountCount == 0 {
		return fmt.Errorf("%w: 账号不存在 user_id=%s", cn35game.ErrGrantTarget, userID)
	}

	unitApplied, err := grantIDsForKeyTx(tx, userID, cfg.UnitGrantKey)
	if err != nil {
		return err
	}
	monsterApplied, err := grantIDsForKeyTx(tx, userID, cfg.MonsterGrantKey)
	if err != nil {
		return err
	}
	unitComplete := sameIDSet(unitApplied, cfg.UnitIDs)
	monsterComplete := sameIDSet(monsterApplied, cfg.MonsterIDs)
	if len(unitApplied) > 0 || len(monsterApplied) > 0 {
		if !unitComplete || !monsterComplete {
			return fmt.Errorf("%w: 固定版本两类台账必须同时完整 (unit=%v monster=%v)",
				cn35game.ErrGrantVersionConflict, keys(unitApplied), keys(monsterApplied))
		}
		// A complete ledger with missing inventory indicates out-of-band deletion.
		// Fail closed; a replay never silently reconstructs externally changed state.
		if err := assertStageRosterInventoryPresent(tx, userID, cfg, res); err != nil {
			return err
		}
		res.Idempotent = true
		return nil
	}

	for _, id := range cfg.UnitIDs {
		var owned int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=? AND unit_id=?`, userID, id).Scan(&owned); err != nil {
			return err
		}
		if owned > 0 {
			res.AlreadyOwnedUnitIDs = append(res.AlreadyOwnedUnitIDs, id)
		} else {
			if err := InsertUnitTx(tx, userID, cn35game.UnitRecord{UnitID: id, Level: cfg.InitialLevel}); err != nil {
				return err
			}
			res.InsertedUnitIDs = append(res.InsertedUnitIDs, id)
		}
		// 0005 names this physical column unit_id; the source key explicitly
		// scopes these rows to companion template IDs only.
		if err := RecordGrantTx(tx, userID, cfg.UnitGrantKey, id); err != nil {
			return err
		}
	}
	for _, id := range cfg.MonsterIDs {
		var owned int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_monsters WHERE user_id=? AND monster_id=?`, userID, id).Scan(&owned); err != nil {
			return err
		}
		if owned > 0 {
			res.AlreadyOwnedMonsterCount += owned
		} else {
			uuid, err := newUUID()
			if err != nil {
				return fmt.Errorf("cn35store: 为魔宠 %d 生成账号实例键失败: %w", id, err)
			}
			if uuid == "" {
				return fmt.Errorf("cn35store: 魔宠 %d 实例 UUID 为空", id)
			}
			if err := InsertMonsterTx(tx, userID, cn35game.MonsterRecord{UUID: uuid, MonsterID: id, Level: cfg.InitialLevel}); err != nil {
				return err
			}
			res.InsertedMonsterUUIDs = append(res.InsertedMonsterUUIDs, uuid)
		}
		// The monster-specific key gives the legacy unit_id value an unambiguous
		// logical meaning (monster_id); it is never shared with companion rows.
		if err := RecordGrantTx(tx, userID, cfg.MonsterGrantKey, id); err != nil {
			return err
		}
	}
	return nil
}

func grantIDsForKeyTx(tx *sql.Tx, userID, grantKey string) (map[int]bool, error) {
	rows, err := tx.Query(`SELECT unit_id FROM cn35_inventory_grants WHERE user_id=? AND grant_key=?`, userID, grantKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func assertStageRosterInventoryPresent(tx *sql.Tx, userID string, cfg *cn35game.StageRosterGrantConfig, res *StageRosterGrantResult) error {
	for _, id := range cfg.UnitIDs {
		var level int
		err := tx.QueryRow(`SELECT level FROM cn35_player_units WHERE user_id=? AND unit_id=?`, userID, id).Scan(&level)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: 已登记 unit %d 但库存行缺失", ErrStageRosterInventoryDrift, id)
		}
		if err != nil {
			return err
		}
		res.AlreadyOwnedUnitIDs = append(res.AlreadyOwnedUnitIDs, id)
	}
	for _, id := range cfg.MonsterIDs {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_monsters WHERE user_id=? AND monster_id=?`, userID, id).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("%w: 已登记 monster %d 但库存实例缺失", ErrStageRosterInventoryDrift, id)
		}
		res.AlreadyOwnedMonsterCount += count
	}
	return nil
}

func newStageMonsterUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "min350-" + hex.EncodeToString(b[:]), nil
}
