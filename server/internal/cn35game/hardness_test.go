// hardness_test.go STAT350-03A/03B：consts.max_guts_up_count 下发与
// hardness 重建测试 profile 的分层门控（默认关闭）。
package cn35game_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

const hardnessAsset = "serve_hardness_rebuild_test.json"

func hardnessServer(t *testing.T, profile string) *httptest.Server {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts:        st,
		Sessions:        st,
		Inventory:       st,
		Catalog:         cat,
		ServerHost:      "127.0.0.1:18091",
		AssetsDir:       filepath.Join("..", "..", "assets", "cn350"),
		HardnessProfile: profile,
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestHardnessProfileShape 重建 profile 形状：11 档 × 7 键，id 顺序依客户端
// Hardness 枚举（1 Soft..11 HardL5）。
func TestHardnessProfileShape(t *testing.T) {
	rows, err := cn35game.LoadHardnessProfile(filepath.Join("..", "..", "assets", "cn350", hardnessAsset))
	if err != nil {
		t.Fatalf("LoadHardnessProfile: %v", err)
	}
	if len(rows) != cn35game.HardnessCategoryCount {
		t.Fatalf("档数=%d want %d", len(rows), cn35game.HardnessCategoryCount)
	}
	for i, r := range rows {
		m := r.(map[string]any)
		if m["id"].(int) != i+1 {
			t.Fatalf("第 %d 行 id=%v want %d（客户端 Hardness 顺序 1柔软/2常规/3坚硬/4-7 SoftL2-L5/8-11 HardL2-L5）",
				i, m["id"], i+1)
		}
		rates := m["bonus_rates"].(map[string]any)
		if len(rates) != cn35game.HardnessWeaponCount {
			t.Fatalf("id=%v bonus_rates 键数=%d want %d", m["id"], len(rates), cn35game.HardnessWeaponCount)
		}
	}
	// 抽查：id=1 柔软 Slash(1)=1.2 / id=11 坚硬L5 Slash=0.4（与客户端回退方向一致）。
	r1 := rows[0].(map[string]any)["bonus_rates"].(map[string]any)
	if r1["1"].(float64) != 1.2 {
		t.Fatalf("id=1 bonus_rates[1]=%v want 1.2", r1["1"])
	}
	r11 := rows[10].(map[string]any)["bonus_rates"].(map[string]any)
	if r11["1"].(float64) != 0.4 || r11["3"].(float64) != 1.6 {
		t.Fatalf("id=11 bonus_rates: %v", r11)
	}
}

// TestHardnessProfileRejectsBadShape 形状不符必须明确报错（不降级、不补齐）。
func TestHardnessProfileRejectsBadShape(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"wrong_count": `{"rows":[{"id":1,"name":"a","bonus_rates":{"1":1,"2":1,"3":1,"4":1,"5":1,"6":1,"7":1}}]}`,
		"wrong_order": `{"rows":[
			{"id":2,"name":"a","bonus_rates":{"1":1,"2":1,"3":1,"4":1,"5":1,"6":1,"7":1}},
			{"id":1,"name":"b","bonus_rates":{"1":1,"2":1,"3":1,"4":1,"5":1,"6":1,"7":1}}]}`,
		"missing_key": `{"rows":[
			{"id":1,"name":"a","bonus_rates":{"1":1,"2":1,"3":1,"4":1,"5":1,"6":1}}]}`,
		"no_name": `{"rows":[
			{"id":1,"bonus_rates":{"1":1,"2":1,"3":1,"4":1,"5":1,"6":1,"7":1}}]}`,
	}
	for name, body := range cases {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cn35game.LoadHardnessProfile(p); err == nil {
			t.Errorf("%s: 形状不符应报错", name)
		}
	}
}

// TestPresetDataConstsMaxGutsUpCount 03A：consts.max_guts_up_count 可在
// preset_data 读到（默认 profile）。
func TestPresetDataConstsMaxGutsUpCount(t *testing.T) {
	ts := hardnessServer(t, "")
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-hardness-a")
	data := gameData(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "")
	consts := data["consts"].(map[string]any)
	if v, ok := consts["max_guts_up_count"].(float64); !ok || v != 30 {
		t.Fatalf("consts.max_guts_up_count=%v want 30（服务端可控参数，重建值）", consts["max_guts_up_count"])
	}
}

// TestPresetDataDefaultNoHardness 03B：默认 profile 不下发 hardness
// （表存在会关掉客户端已知回退，C0.4）。
func TestPresetDataDefaultNoHardness(t *testing.T) {
	ts := hardnessServer(t, "")
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-hardness-b")
	data := gameData(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "")
	if _, has := data["hardness"]; has {
		t.Fatalf("默认 profile 不得下发 hardness（会关闭客户端回退）: %v", data["hardness"])
	}
}

// TestPresetDataHardnessProfileEnabled 03B：显式启用后按 Hardness 顺序下发 11 档。
func TestPresetDataHardnessProfileEnabled(t *testing.T) {
	ts := hardnessServer(t, hardnessAsset)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-hardness-c")
	data := gameData(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "")
	rows, ok := data["hardness"].([]any)
	if !ok || len(rows) != cn35game.HardnessCategoryCount {
		t.Fatalf("启用后 hardness 档数=%v want %d", data["hardness"], cn35game.HardnessCategoryCount)
	}
	for i, r := range rows {
		m := r.(map[string]any)
		if m["id"].(float64) != float64(i+1) {
			t.Fatalf("第 %d 行 id=%v want %d", i, m["id"], i+1)
		}
		if _, has := m["bonus_rates"].(map[string]any)["7"]; !has {
			t.Fatalf("id=%v 缺 bonus_rates[7]（Heal）", m["id"])
		}
	}
}
