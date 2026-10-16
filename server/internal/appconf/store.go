// store.go 把保序文档模型接到磁盘：serve_app_conf_android.json 的
// 读取、带备份的保存与备份清单。备份目录落在 state 目录（已 gitignore），
// 不污染 assets fixture 树。
package appconf

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileName 是 assets 目录下 app_conf 源 fixture 的文件名。
const FileName = "serve_app_conf_android.json"

// Store 管理一份 app_conf 源文件及其历史备份。
type Store struct {
	File      string
	BackupDir string
}

// NewStore 以 assetsDir/serve_app_conf_android.json 为源文件，
// stateDir/backups/app_conf/ 为备份目录。
func NewStore(assetsDir, stateDir string) *Store {
	return &Store{
		File:      filepath.Join(assetsDir, FileName),
		BackupDir: filepath.Join(stateDir, "backups", "app_conf"),
	}
}

// Load 读取并解析源文件。
func (s *Store) Load() (*Value, error) {
	raw, err := os.ReadFile(s.File)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", s.File, err)
	}
	doc, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", s.File, err)
	}
	return doc, nil
}

// Save 先把当前文件内容复制进备份目录，再把 doc 以缩进形态原子写回。
// 返回备份文件路径；源文件此前不存在时备份路径为空。
func (s *Store) Save(doc *Value) (string, error) {
	backupPath := ""
	if old, err := os.ReadFile(s.File); err == nil {
		if err := os.MkdirAll(s.BackupDir, 0o755); err != nil {
			return "", fmt.Errorf("创建备份目录失败: %w", err)
		}
		backupPath = filepath.Join(s.BackupDir,
			strings.TrimSuffix(FileName, ".json")+"."+time.Now().Format("20060102-150405.000000")+".json")
		if err := os.WriteFile(backupPath, old, 0o644); err != nil {
			return "", fmt.Errorf("写入备份 %s 失败: %w", backupPath, err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("读取旧文件失败: %w", err)
	}

	pretty, err := doc.MarshalIndent()
	if err != nil {
		return "", fmt.Errorf("文档序列化失败: %w", err)
	}
	tmp := s.File + ".tmp"
	if err := os.WriteFile(tmp, pretty, 0o644); err != nil {
		return "", fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, s.File); err != nil {
		return "", fmt.Errorf("替换 %s 失败: %w", s.File, err)
	}
	return backupPath, nil
}

// BackupInfo 描述一份历史备份。
type BackupInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// Backups 按文件名倒序列出备份（时间戳命名，即时间倒序）。
func (s *Store) Backups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(s.BackupDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]BackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{
			Name:    entry.Name(),
			Path:    filepath.Join(s.BackupDir, entry.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}
