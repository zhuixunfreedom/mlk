// appconf_test.go cn35_app_conf_versions（0014）归档原语测试：
// 归档写行+审计同事务、sha 去重、版本列表倒序、取文档与未命中错误。
package cn35store

import (
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestAppConfArchiveWriteAndDedup(t *testing.T) {
	s := openStore(t, t.TempDir())
	doc1 := []byte(`{"version":1,"pages":{}}`)
	doc2 := []byte(`{"version":2,"pages":{}}`)

	v1, ins, err := s.ArchiveAppConf(doc1, "tester", "first")
	if err != nil || !ins || v1 != 1 {
		t.Fatalf("首次归档 = (%d,%v,%v)，want (1,true,nil)", v1, ins, err)
	}
	// 同一文档再归档：sha 去重，不新增行，返回既有版本号。
	v1b, ins2, err := s.ArchiveAppConf(doc1, "tester", "dup")
	if err != nil || ins2 || v1b != v1 {
		t.Fatalf("重复归档 = (%d,%v,%v)，want (%d,false,nil)", v1b, ins2, err, v1)
	}
	v2, ins3, err := s.ArchiveAppConf(doc2, "tester", "")
	if err != nil || !ins3 || v2 != 2 {
		t.Fatalf("第二归档 = (%d,%v,%v)，want (2,true,nil)", v2, ins3, err)
	}

	// 审计行：只有新插入才写（action=app_conf.archive，2 行）。
	var audits int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_admin_audit
		WHERE action='app_conf.archive'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("审计行 = %d，want 2", audits)
	}

	list, err := s.AppConfVersions(0)
	if err != nil || len(list) != 2 || list[0].Version != 2 || list[1].Version != 1 {
		t.Fatalf("版本列表 = %+v err=%v", list, err)
	}
	if list[0].Actor != "tester" || list[0].Size != len(doc2) {
		t.Fatalf("元数据不符: %+v", list[0])
	}

	v, err := s.AppConfVersionDoc(1)
	if err != nil || v.Doc != string(doc1) {
		t.Fatalf("取文档 = %+v err=%v", v, err)
	}
	if _, err := s.AppConfVersionDoc(99); err != cn35game.ErrAppConfVersionUnknown {
		t.Fatalf("未命中错误 = %v，want ErrAppConfVersionUnknown", err)
	}
}

func TestAppConfArchiveRejectsBadDoc(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, _, err := s.ArchiveAppConf(nil, "a", ""); err == nil {
		t.Fatal("空文档应拒绝")
	}
	if _, _, err := s.ArchiveAppConf([]byte(`{bad`), "a", ""); err == nil {
		t.Fatal("非法 JSON 应拒绝")
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_app_conf_versions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("非法输入不应落行，got %d", n)
	}
}
