package appconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 保存契约：旧文件先整体复制进备份目录，新内容以缩进形态原子写回。
func TestStoreSaveBacksUpOldFile(t *testing.T) {
	assets := t.TempDir()
	state := t.TempDir()
	store := NewStore(assets, state)

	old := "{\n  \"version\": 1\n}\n"
	if err := os.WriteFile(store.File, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := Parse([]byte(`{"version":2,"pages":{"Home":{"parts":{"A":{"x":1}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	backupPath, err := store.Save(doc)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backupPath == "" {
		t.Fatal("应返回备份路径")
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("备份不可读: %v", err)
	}
	if string(backup) != old {
		t.Fatalf("备份内容 = %q，期望旧文件原文", backup)
	}

	rewritten, err := os.ReadFile(store.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rewritten), "\n  \"version\": 2") {
		t.Fatalf("写回文件应为缩进形态:\n%s", rewritten)
	}
	backups, err := store.Backups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("Backups = %v, %v", backups, err)
	}
}

// 首次保存（无旧文件）不应产生备份，也不应报错。
func TestStoreSaveFirstWriteNoBackup(t *testing.T) {
	assets := t.TempDir()
	state := t.TempDir()
	store := NewStore(assets, state)

	doc, err := Parse([]byte(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	backupPath, err := store.Save(doc)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backupPath != "" {
		t.Fatalf("首次保存不应有备份，得到 %q", backupPath)
	}
	if _, err := os.Stat(store.File); err != nil {
		t.Fatalf("文件未写入: %v", err)
	}
	if filepath.Dir(store.File) != assets {
		t.Fatalf("File 不在 assets 目录: %s", store.File)
	}
}
