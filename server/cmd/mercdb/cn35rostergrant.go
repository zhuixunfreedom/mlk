package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
)

// tester01 是当前已验收的测试账号；通用批次对任何已存在账号开放，
// -user 必填且必须解析到真实账号（见 verifyStageRosterTarget）。
const stageRosterTargetUserID = "681abf0d7485"

type stageRosterPreviewCandidate struct {
	Kind                string         `json:"kind"`
	ID                  int            `json:"id"`
	Name                string         `json:"name"`
	Rare                int            `json:"rare"`
	InventoryStatus     string         `json:"inventory_status"`
	ExistingCount       int            `json:"existing_count"`
	ExistingLevels      []int          `json:"existing_levels"`
	GrantedLevel        int            `json:"granted_level"`
	BaseValues          map[string]any `json:"base_values"`
	MissingFields       []string       `json:"missing_fields"`
	ResourceEvidence    string         `json:"resource_evidence"`
	RuntimeLoadVerified bool           `json:"runtime_load_verified"`
}

type stageRosterPreviewLedger struct {
	GrantKey    string `json:"grant_key"`
	AppliedIDs  []int  `json:"applied_ids"`
	ExpectedIDs []int  `json:"expected_ids"`
	Complete    bool   `json:"complete"`
	Empty       bool   `json:"empty"`
	Matches     bool   `json:"matches"`
}

type stageRosterPreview struct {
	GeneratedAt                  string                        `json:"generated_at"`
	DatabasePath                 string                        `json:"database_path"`
	ReadOnly                     bool                          `json:"read_only"`
	MigrationsAppliedByTool      bool                          `json:"migrations_applied_by_tool"`
	Account                      map[string]any                `json:"account"`
	GrantVersion                 string                        `json:"grant_version"`
	ContentSHA256                string                        `json:"content_sha256"`
	ValueScope                   string                        `json:"value_scope"`
	UnitLedger                   stageRosterPreviewLedger      `json:"unit_ledger"`
	MonsterLedger                stageRosterPreviewLedger      `json:"monster_ledger"`
	Candidates                   []stageRosterPreviewCandidate `json:"candidates"`
	AlreadyOwnedUnitIDs          []int                         `json:"already_owned_unit_ids"`
	MissingUnitIDs               []int                         `json:"missing_unit_ids"`
	MissingMonsterIDs            []int                         `json:"missing_monster_ids"`
	ExistingMonsterCount         int                           `json:"existing_monster_count"`
	WillInsertMonster            bool                          `json:"will_insert_monster"`
	ProposedInsertUnitIDs        []int                         `json:"proposed_insert_unit_ids"`
	ProposedInsertMonsterIDs     []int                         `json:"proposed_insert_monster_ids"`
	UnitLedgerIDsOnFirstApply    []int                         `json:"unit_ledger_ids_on_first_apply"`
	MonsterLedgerIDsOnFirstApply []int                         `json:"monster_ledger_ids_on_first_apply"`
	Decision                     string                        `json:"decision"`
	Warnings                     map[string]any                `json:"warnings"`
}

func cn35RosterGrant(args []string) error {
	fs := flag.NewFlagSet("cn35-roster-grant", flag.ContinueOnError)
	dbPath := fs.String("db", "", "existing SQLite database (required; preview opens mode=ro)")
	configPath := fs.String("config", "assets/cn350/stage_roster_grant.json", "fixed-version stage grant config")
	cardsDir := fs.String("cards", "assets/shared/cards", "versioned static card catalog")
	userID := fs.String("user", "", "目标账号 user_id（须已存在，如 tester01 "+stageRosterTargetUserID+"）")
	if len(args) == 0 {
		return fmt.Errorf("usage: mercdb cn35-roster-grant digest|preview|apply|status [-db PATH] [-config JSON] [-cards DIR] [-user ID]")
	}
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if action == "digest" {
		cfg, err := cn35game.LoadStageRosterGrantConfig(resolveAsset(*configPath))
		if err != nil {
			return err
		}
		sum, err := cn35game.StageRosterConfigSHA256(cfg)
		if err != nil {
			return err
		}
		fmt.Println(sum)
		return nil
	}
	if *dbPath == "" {
		return fmt.Errorf("cn35-roster-grant %s requires an explicit -db path", action)
	}
	if *userID == "" {
		return fmt.Errorf("cn35-roster-grant %s requires -user <existing user_id>", action)
	}
	cfg, cat, err := loadStageRosterInputs(*configPath, *cardsDir)
	if err != nil {
		return err
	}
	switch action {
	case "preview", "status":
		st, err := cn35store.OpenReadOnly(resolveAsset(*dbPath))
		if err != nil {
			return err
		}
		defer st.Close()
		out, err := buildStageRosterPreview(st, resolveAsset(*dbPath), *userID, cfg, cat)
		if err != nil {
			return err
		}
		return printJSON(out)
	case "apply":
		if _, err := os.Stat(resolveAsset(*dbPath)); err != nil {
			return fmt.Errorf("apply refuses a missing DB path: %w", err)
		}
		st, err := cn35store.OpenExistingForGrant(resolveAsset(*dbPath))
		if err != nil {
			return err
		}
		defer st.Close()
		if err := verifyStageRosterTarget(st.DB(), *userID); err != nil {
			return err
		}
		res, err := st.GrantStageRoster(*userID, cfg, cat)
		if err != nil {
			return err
		}
		return printJSON(res)
	default:
		return fmt.Errorf("unknown action %q; use preview|apply|status", action)
	}
}

func loadStageRosterInputs(configPath, cardsDir string) (*cn35game.StageRosterGrantConfig, *cardcatalog.Catalog, error) {
	cfg, err := cn35game.LoadStageRosterGrantConfig(resolveAsset(configPath))
	if err != nil {
		return nil, nil, err
	}
	cat, err := cardcatalog.Load(resolveAsset(cardsDir))
	if err != nil {
		return nil, nil, err
	}
	if err := cn35game.ValidateStageRosterGrant(cat, cfg); err != nil {
		return nil, nil, err
	}
	return cfg, cat, nil
}

func buildStageRosterPreview(st *cn35store.Store, dbPath, userID string, cfg *cn35game.StageRosterGrantConfig, cat *cardcatalog.Catalog) (*stageRosterPreview, error) {
	tx, err := st.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var userName string
	var tutorial int
	if err := tx.QueryRow(`SELECT user_name,tutorial_finish FROM cn35_accounts WHERE user_id=?`, userID).Scan(&userName, &tutorial); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("cn35-roster-grant: target account not found")
		}
		return nil, err
	}
	unitOwned := map[int]int{}
	if len(cfg.UnitIDs) > 0 {
		marks := makeSQLMarks(len(cfg.UnitIDs))
		args := append([]any{userID}, intsToAny(cfg.UnitIDs)...)
		rows, err := tx.Query(`SELECT unit_id,level FROM cn35_player_units WHERE user_id=? AND unit_id IN (`+marks+`) ORDER BY unit_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, level int
			if err := rows.Scan(&id, &level); err != nil {
				rows.Close()
				return nil, err
			}
			unitOwned[id] = level
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	monsterLevels := map[int][]int{}
	for _, id := range cfg.MonsterIDs {
		rows, err := tx.Query(`SELECT level FROM cn35_player_monsters WHERE user_id=? AND monster_id=? ORDER BY uuid`, userID, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var level int
			if err := rows.Scan(&level); err != nil {
				rows.Close()
				return nil, err
			}
			monsterLevels[id] = append(monsterLevels[id], level)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	unitLedger, err := previewLedger(tx, userID, cfg.UnitGrantKey, cfg.UnitIDs)
	if err != nil {
		return nil, err
	}
	monsterLedger, err := previewLedger(tx, userID, cfg.MonsterGrantKey, cfg.MonsterIDs)
	if err != nil {
		return nil, err
	}
	missingUnits := make([]int, 0)
	alreadyOwned := make([]int, 0)
	for _, id := range cfg.UnitIDs {
		if _, ok := unitOwned[id]; ok {
			alreadyOwned = append(alreadyOwned, id)
		} else {
			missingUnits = append(missingUnits, id)
		}
	}
	var existingMonsterCount int
	for _, id := range cfg.MonsterIDs {
		existingMonsterCount += len(monsterLevels[id])
	}
	rows := make([]stageRosterPreviewCandidate, 0, len(cfg.UnitIDs)+len(cfg.MonsterIDs))
	unitHitsGaps := make([]int, 0)
	for _, id := range cfg.UnitIDs {
		unit, err := cat.Unit(id)
		if err != nil {
			return nil, err
		}
		levels := []int{}
		status := "missing"
		if level, ok := unitOwned[id]; ok {
			levels = append(levels, level)
			status = "owned; preserve existing row"
		}
		missing := []string{}
		if unit.Hits == nil {
			missing = append(missing, "hits")
			unitHitsGaps = append(unitHitsGaps, id)
		}
		rows = append(rows, stageRosterPreviewCandidate{
			Kind: "unit", ID: id, Name: unit.Title + unit.Name, Rare: unit.Rare,
			InventoryStatus: status, ExistingCount: len(levels), ExistingLevels: levels,
			GrantedLevel:  cfg.InitialLevel,
			BaseValues:    map[string]any{"life": unit.Life, "atk": unit.Atk, "hits": unit.Hits},
			MissingFields: missing, ResourceEvidence: "static MIN350-03 stage candidate; not runtime proof", RuntimeLoadVerified: false,
		})
	}
	petZeroFields := make([]string, 0)
	for _, id := range cfg.MonsterIDs {
		monster, err := cat.Monster(id)
		if err != nil {
			return nil, err
		}
		levels := append([]int(nil), monsterLevels[id]...)
		status := "missing; would add"
		if len(levels) > 0 {
			status = "owned; preserve existing instance(s)"
		}
		values := map[string]any{
			"life": monster.Life, "atk": monster.Atk, "hits": monster.Hits,
			"fire": monster.Rates.Fire, "aqua": monster.Rates.Aqua, "wind": monster.Rates.Wind,
			"light": monster.Rates.Light, "dark": monster.Rates.Dark,
		}
		for field, value := range map[string]*int{"life": monster.Life, "atk": monster.Atk, "hits": monster.Hits} {
			if value != nil && *value == 0 {
				petZeroFields = append(petZeroFields, field)
			}
		}
		for field, value := range map[string]*float64{
			"fire": monster.Rates.Fire, "aqua": monster.Rates.Aqua, "wind": monster.Rates.Wind,
			"light": monster.Rates.Light, "dark": monster.Rates.Dark,
		} {
			if value != nil && *value == 0 {
				petZeroFields = append(petZeroFields, field)
			}
		}
		if len(levels) == 0 {
			status = "missing"
		}
		rows = append(rows, stageRosterPreviewCandidate{
			Kind: "monster", ID: id, Name: monster.Name, Rare: monster.Rare,
			InventoryStatus: status, ExistingCount: len(levels), ExistingLevels: levels,
			GrantedLevel: cfg.InitialLevel, BaseValues: values,
			MissingFields:    append([]string(nil), monster.Missing...),
			ResourceEvidence: "static MIN350-03 stage candidate; not runtime proof", RuntimeLoadVerified: false,
		})
	}
	sort.Ints(unitHitsGaps)
	sort.Strings(petZeroFields)
	decision := "first-application"
	if !unitLedger.Empty || !monsterLedger.Empty {
		if unitLedger.Matches && monsterLedger.Matches {
			decision = "already-applied"
			if len(missingUnits) != 0 || existingMonsterCount == 0 {
				decision = "ledger-inventory-drift; apply fails closed"
			}
		} else {
			decision = "partial-or-mismatched-ledger; apply fails closed"
		}
	}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	missingMonsters := make([]int, 0)
	for _, id := range cfg.MonsterIDs {
		if len(monsterLevels[id]) == 0 {
			missingMonsters = append(missingMonsters, id)
		}
	}
	proposedUnits, proposedMonsters := []int{}, []int{}
	unitLedgerWrites, monsterLedgerWrites := []int{}, []int{}
	if decision == "first-application" {
		proposedUnits = append(proposedUnits, missingUnits...)
		proposedMonsters = append(proposedMonsters, missingMonsters...)
		unitLedgerWrites = append(unitLedgerWrites, cfg.UnitIDs...)
		monsterLedgerWrites = append(monsterLedgerWrites, cfg.MonsterIDs...)
	}
	return &stageRosterPreview{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), DatabasePath: absolute,
		ReadOnly: true, MigrationsAppliedByTool: false,
		Account:      map[string]any{"user_id": userID, "user_name": userName, "tutorial_finish": tutorial == 1, "identity_verified": true},
		GrantVersion: cfg.GrantVersion, ContentSHA256: cfg.ContentSHA256, ValueScope: cfg.ValueScope,
		UnitLedger: unitLedger, MonsterLedger: monsterLedger, Candidates: rows,
		AlreadyOwnedUnitIDs: alreadyOwned, MissingUnitIDs: missingUnits,
		ExistingMonsterCount: existingMonsterCount, WillInsertMonster: existingMonsterCount == 0 && decision == "first-application",
		MissingMonsterIDs: missingMonsters, ProposedInsertUnitIDs: proposedUnits, ProposedInsertMonsterIDs: proposedMonsters,
		UnitLedgerIDsOnFirstApply: unitLedgerWrites, MonsterLedgerIDsOnFirstApply: monsterLedgerWrites,
		Decision: decision,
		Warnings: map[string]any{
			"unit_hits_missing_ids":                   unitHitsGaps,
			"monster_base_zero_fields":                petZeroFields,
			"monster_39_values_retained_from_catalog": true,
			"full_scope_status":                       "231 companions + 157 rare-4/5 pets and max-level remain open",
		},
	}, nil
}

func verifyStageRosterTarget(db *sql.DB, userID string) error {
	var userName string
	if err := db.QueryRow(`SELECT user_name FROM cn35_accounts WHERE user_id=?`, userID).Scan(&userName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("cn35-roster-grant: target account not found")
		}
		return err
	}
	return nil
}

func previewLedger(tx *sql.Tx, userID, key string, expected []int) (stageRosterPreviewLedger, error) {
	ids, err := readPreviewLedger(tx, userID, key)
	if err != nil {
		return stageRosterPreviewLedger{}, err
	}
	ledger := stageRosterPreviewLedger{
		GrantKey: key, AppliedIDs: ids, ExpectedIDs: append([]int(nil), expected...),
		Empty: len(ids) == 0,
	}
	ledger.Complete = len(ids) == len(expected)
	ledger.Matches = ledger.Complete
	if ledger.Matches {
		for i := range ids {
			if ids[i] != expected[i] {
				ledger.Matches = false
				break
			}
		}
	}
	return ledger, nil
}

func readPreviewLedger(tx *sql.Tx, userID, key string) ([]int, error) {
	rows, err := tx.Query(`SELECT unit_id FROM cn35_inventory_grants WHERE user_id=? AND grant_key=? ORDER BY unit_id`, userID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func makeSQLMarks(n int) string {
	marks := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			marks = append(marks, ',')
		}
		marks = append(marks, '?')
	}
	return string(marks)
}

func intsToAny(ids []int) []any {
	values := make([]any, len(ids))
	for i, id := range ids {
		values[i] = id
	}
	return values
}

func printJSON(value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(raw))
	return err
}
