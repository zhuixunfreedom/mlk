package cn35store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/database"
)

// OpenReadOnly opens an existing SQLite database with URI mode=ro and connection-local
// query_only. It never creates a file, changes journal mode, or applies migrations.
func OpenReadOnly(path string) (*Store, error) {
	absolute, err := existingSQLitePath(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	walInfo, walErr := os.Stat(absolute + "-wal")
	_, shmErr := os.Stat(absolute + "-shm")
	walExists := walErr == nil
	shmExists := shmErr == nil
	walHasData := walExists && walInfo.Size() > 0
	if walErr != nil && !os.IsNotExist(walErr) {
		return nil, walErr
	}
	if shmErr != nil && !os.IsNotExist(shmErr) {
		return nil, shmErr
	}
	if walHasData && !shmExists {
		return nil, fmt.Errorf("cn35store: non-empty WAL without shared-memory index; use a consistent backup or restore the sidecar")
	}
	// An offline SQLite backup can retain journal_mode=WAL without sidecars.
	// immutable=1 reads only the main file and avoids creating -wal/-shm files;
	// it is safe here only when no non-empty WAL exists.
	if !walHasData && !shmExists {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err = db.Exec(`PRAGMA query_only=ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cn35store: enable query_only: %w", err)
	}
	var queryOnly int
	if err = db.QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil || queryOnly != 1 {
		_ = db.Close()
		return nil, fmt.Errorf("cn35store: SQLite query_only 未启用: value=%d err=%v", queryOnly, err)
	}
	return &Store{db: db, clock: time.Now}, nil
}

// OpenExistingForGrant opens an existing writable SQLite database without running
// Migrate. The caller must use a verified, offline backup and explicitly coordinate
// any pending schema upgrade before it can apply a stage grant.
func OpenExistingForGrant(path string) (*Store, error) {
	absolute, err := existingSQLitePath(path)
	if err != nil {
		return nil, err
	}
	db, err := database.Open(absolute)
	if err != nil {
		return nil, err
	}
	closeWithError := func(cause error) (*Store, error) {
		_ = db.Close()
		return nil, cause
	}
	migrations, err := database.Status(context.Background(), db)
	if err != nil {
		return closeWithError(err)
	}
	// This grant needs the already-deployed account/profile/inventory baseline
	// plus the monster Size columns (0009, STAT350-04) — grants write monster
	// rows and must not leave Size unknown-by-schema. 0008 (decks) is still an
	// independent migration and is not required by this operation.
	required := map[string]bool{
		"0001_accounts.sql":            false,
		"0002_player_core.sql":         false,
		"0003_cn35.sql":                false,
		"0004_cn35_guest_device.sql":   false,
		"0005_cn35_inventory.sql":      false,
		"0006_cn35_quest_progress.sql": false,
		"0007_cn35_quest_battle.sql":   false,
		"0009_cn35_monster_size.sql":   false,
	}
	for _, migration := range migrations {
		if _, ok := required[migration.Name]; ok {
			required[migration.Name] = migration.Applied
		}
	}
	for name, applied := range required {
		if !applied {
			return closeWithError(fmt.Errorf("cn35store: required migration %s is pending; no migration was run", name))
		}
	}
	if _, err := db.Exec(`SELECT 1 FROM cn35_accounts LIMIT 0`); err != nil {
		return closeWithError(fmt.Errorf("cn35store: target lacks CN3.5 account schema: %w", err))
	}
	return &Store{db: db, clock: time.Now}, nil
}

func existingSQLitePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("cn35store: SQLite path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("cn35store: not a regular SQLite file: %s", absolute)
	}
	return absolute, nil
}
