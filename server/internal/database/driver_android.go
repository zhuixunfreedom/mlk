//go:build android

package database

import (
	"database/sql"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Android 应用沙箱（zygote seccomp）禁止 stat/lstat/fstat/statx 族原始
// syscall；modernc.org/sqlite 的 libc 转译层直接发这些 syscall，在 App 内
// 会被 SIGSYS 杀死。改用 ncruces/go-sqlite3（wazero WASM SQLite，VFS 走
// Go os 包 → newfstatat，合规）。注册别名 "sqlite" 使现有 sql.Open
// 调用点对驱动无感。
func init() {
	sql.Register("sqlite", &driver.SQLite{})
}
