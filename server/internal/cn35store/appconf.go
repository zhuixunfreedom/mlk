// appconf.go cn35_app_conf_versions（迁移 0014）读写原语。
//
// PAGE-EDITOR：app_conf GUI 编辑器在每次保存前把旧文档完整归档到本表，
// 同事务写 cn35_admin_audit 审计行（action=app_conf.archive）。归档只做
// INSERT——doc_sha256 唯一冲突说明同一旧文档已在库，按去重语义返回
// (既有版本号, false, nil)，不算错误。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// appConfArchiveLimit 版本列表默认上限（limit<=0 时套用）。
const appConfArchiveLimit = 200

// ArchiveAppConf 归档保存前的旧文档：INSERT 版本行 + 审计行，同一事务。
// 同一文档重复归档被 doc_sha256 唯一约束去重（返回既有版本号、inserted=false）。
func (s *Store) ArchiveAppConf(prevDoc []byte, actor, note string) (int64, bool, error) {
	if len(prevDoc) == 0 {
		return 0, false, fmt.Errorf("cn35store: app_conf 归档文档为空")
	}
	if !json.Valid(prevDoc) {
		return 0, false, fmt.Errorf("cn35store: app_conf 归档文档不是合法 JSON")
	}
	sum := sha256.Sum256(prevDoc)
	sha := hex.EncodeToString(sum[:])
	if actor == "" {
		actor = "admin-api"
	}
	var version int64
	var inserted bool
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		// 去重命中：直接返回既有版本号，不再插行也不重复审计。
		err := tx.QueryRowContext(ctx,
			`SELECT version FROM cn35_app_conf_versions WHERE doc_sha256=?`, sha).
			Scan(&version)
		switch {
		case err == nil:
			inserted = false
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO cn35_app_conf_versions (doc_sha256,doc,actor,note,created_at)
			 VALUES (?,?,?,?,?)`,
			sha, string(prevDoc), actor, note, nowUTC())
		if err != nil {
			return err
		}
		version, err = res.LastInsertId()
		if err != nil {
			return err
		}
		inserted = true
		return appendAuditTx(tx, actor, "app_conf.archive", "serve_app_conf_android.json",
			map[string]any{"version": version, "doc_sha256": sha, "bytes": len(prevDoc)})
	})
	if err != nil {
		return 0, false, err
	}
	return version, inserted, nil
}

// AppConfVersions 按版本倒序列出归档元数据。
func (s *Store) AppConfVersions(limit int) ([]cn35game.AppConfVersionMeta, error) {
	if limit <= 0 {
		limit = appConfArchiveLimit
	}
	rows, err := s.db.Query(`SELECT version,doc_sha256,length(doc),actor,note,created_at
		FROM cn35_app_conf_versions ORDER BY version DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]cn35game.AppConfVersionMeta, 0)
	for rows.Next() {
		var m cn35game.AppConfVersionMeta
		if err = rows.Scan(&m.Version, &m.DocSHA256, &m.Size, &m.Actor, &m.Note,
			&m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppConfVersionDoc 取某版本完整归档；不存在返回 ErrAppConfVersionUnknown。
func (s *Store) AppConfVersionDoc(version int64) (*cn35game.AppConfVersion, error) {
	var v cn35game.AppConfVersion
	err := s.db.QueryRow(`SELECT version,doc_sha256,length(doc),actor,note,created_at,doc
		FROM cn35_app_conf_versions WHERE version=?`, version).
		Scan(&v.Meta.Version, &v.Meta.DocSHA256, &v.Meta.Size, &v.Meta.Actor,
			&v.Meta.Note, &v.Meta.CreatedAt, &v.Doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrAppConfVersionUnknown
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
