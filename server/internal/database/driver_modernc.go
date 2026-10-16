//go:build !android

package database

// 桌面/服务器端继续用锁定版本的纯 Go 驱动 modernc.org/sqlite
// （CGO_ENABLED=0 可交叉编译单二进制）。Android 驱动选择见 driver_android.go。
import _ "modernc.org/sqlite"
