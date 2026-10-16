// envfile_test.go env 文件解析与密钥优先级验收：
// KEY=VALUE / 注释 / export / 引号 / 显式缺失报错 / flag>env>文件。
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mercd35.env")
	content := `# 注释行
MERC35_WEB_AUTHKEY=web-key-1
export MERC35_ADMIN_TOKEN="admin tok"
EMPTY=
QUOTED='单引号值'
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["MERC35_WEB_AUTHKEY"] != "web-key-1" || m["MERC35_ADMIN_TOKEN"] != "admin tok" ||
		m["EMPTY"] != "" || m["QUOTED"] != "单引号值" {
		t.Fatalf("解析结果: %v", m)
	}
	// 显式缺失路径报错
	if _, err := LoadEnvFile(filepath.Join(dir, "missing.env")); err == nil {
		t.Fatal("显式缺失 env 文件应报错")
	}
	// 空路径+无候选文件 → nil,nil（切到不含 env 文件的独立目录验证探测）
	old, _ := os.Getwd()
	emptyDir := t.TempDir()
	if err := os.Chdir(emptyDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if m, err := LoadEnvFile(""); err != nil || m != nil {
		t.Fatalf("无 env 文件应 nil,nil: %v %v", m, err)
	}
	// 自动探测顺序：mercd35.env 优先于 .env
	if err := os.WriteFile(filepath.Join(emptyDir, ".env"), []byte("K=dotenv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m, err := LoadEnvFile(""); err != nil || m["K"] != "dotenv" {
		t.Fatalf("自动探测 .env: %v %v", m, err)
	}
	// 坏行报错
	bad := filepath.Join(dir, "bad.env")
	if err := os.WriteFile(bad, []byte("NO_EQUALS_SIGN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnvFile(bad); err == nil {
		t.Fatal("坏行应报错")
	}
}

func TestEnvValuePrecedence(t *testing.T) {
	t.Setenv("TEST_KEY_X", "env-val")
	file := map[string]string{"TEST_KEY_X": "file-val", "TEST_KEY_Y": "file-y"}
	if got := envValue("flag-val", "TEST_KEY_X", file); got != "flag-val" {
		t.Fatalf("flag 应最先: %q", got)
	}
	if got := envValue("", "TEST_KEY_X", file); got != "env-val" {
		t.Fatalf("进程 env 应覆盖文件: %q", got)
	}
	if got := envValue("", "TEST_KEY_Y", file); got != "file-y" {
		t.Fatalf("文件兜底: %q", got)
	}
	if got := envValue("", "TEST_KEY_Z", file); got != "" {
		t.Fatalf("缺失应空: %q", got)
	}
}
