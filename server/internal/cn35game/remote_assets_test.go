// remote_assets_test.go 远端资源叠加管理器单测（包内，可访问 now/stableAge
// 注入点与状态文件路径）。HTTP 面测试见 remote_assets_http_test.go。
package cn35game

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 小基线清单：version,100 + 两条资产行（unit/unit_152、monster/monster_39）。
const testManifest = "version,100@ios_version,13@android_version,20@maintenance," +
	"@152,23,1,0,1@39,9,1,0,1\n"

func writeBaseManifest(t *testing.T, assetsDir string) {
	t.Helper()
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, assetInfoFile),
		[]byte(testManifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRemoteFile 造远端候选文件；oldMtime=true 时 mtime 拨到 1 小时前
// （首轮扫描即稳定），false 保持当前时间（首轮 pending）。
func writeRemoteFile(t *testing.T, remoteDir, rel string, content []byte, oldMtime bool) {
	t.Helper()
	full := filepath.Join(remoteDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if oldMtime {
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(full, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func fakeBundle(payload string) []byte {
	return append([]byte("UnityWeb\x00"), payload...)
}

func md5_8(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])[:8]
}

func newTestMgr(remote, assets, state string) *RemoteAssets {
	return NewRemoteAssets(remote, assets, state, nil)
}

// 1) 远端为空 → body 与基线字节一致，版本=基线，不写状态文件。
func TestRemoteAssetsEmptyMatchesBaseline(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)

	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if string(m.Body()) != testManifest {
		t.Fatalf("body != 基线:\n%s", m.Body())
	}
	if m.Version() != 100 {
		t.Fatalf("版本 = %d, want 100", m.Version())
	}
	if _, err := os.Stat(filepath.Join(state, "remote_assets_state.json")); !os.IsNotExist(err) {
		t.Fatalf("空远端不应写状态文件: %v", err)
	}
}

// 2) 新增 unit/unit_414 → 追加 `414,23,1,0,1,<md5>`，版本=基线+1，
// 状态落盘；同 state 新建管理器（模拟重启）版本不变。
func TestRemoteAssetsNewEntryAndRestart(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	content := fakeBundle("fake-414")
	writeRemoteFile(t, remote, "unit/unit_414.Android.unity3d", content, true)

	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	wantRow := "@414,23,1,0,1," + md5_8(content)
	if !strings.HasSuffix(string(m.Body()), wantRow+"\n") {
		t.Fatalf("追加行缺失: 尾部 %.60q", m.Body()[max(0, len(m.Body())-60):])
	}
	if m.Version() != 101 {
		t.Fatalf("版本 = %d, want 101", m.Version())
	}
	stRaw, err := os.ReadFile(filepath.Join(state, "remote_assets_state.json"))
	if err != nil {
		t.Fatalf("状态文件未落盘: %v", err)
	}
	var st remoteStateFile
	if err := json.Unmarshal(stRaw, &st); err != nil || st.AssetInfoVersion != 101 {
		t.Fatalf("状态文件: %s err=%v", stRaw, err)
	}
	// 基线行原样保留（无重复名）。
	body := string(m.Body())
	if strings.Count(body, "@152,23,1,0,1") != 1 || strings.Count(body, "@39,9,1,0,1") != 1 {
		t.Fatalf("基线行被改/重复:\n%s", body)
	}

	// 重启：同 state 新管理器 digest 未变 → 版本不递增、文件不重写。
	m2 := newTestMgr(remote, assets, state)
	if err := m2.Scan(); err != nil {
		t.Fatal(err)
	}
	if m2.Version() != 101 {
		t.Fatalf("重启后版本 = %d, want 101", m2.Version())
	}
	if string(m2.Body()) != string(m.Body()) {
		t.Fatal("重启后 body 变化")
	}
}

// 3) 覆盖基线同名行：远端 unit/unit_152 → 原行替换为带 md5、advance=1，
// 清单内无重复名。
func TestRemoteAssetsOverrideBaselineRow(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	content := fakeBundle("override-152")
	writeRemoteFile(t, remote, "unit/unit_152.Android.unity3d", content, true)

	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	body := string(m.Body())
	want := "152,23,1,0,1," + md5_8(content)
	if !strings.Contains(body, "@"+want+"@") && !strings.HasSuffix(body, "@"+want+"\n") {
		t.Fatalf("覆盖行缺失: %s", body)
	}
	if strings.Count(body, "@152,23,") != 1 {
		t.Fatalf("重复名 152: %s", body)
	}
	e, ok := m.Entry("unit/unit_152")
	if !ok || !e.ReplacesEmbedded || e.MD5 != md5_8(content) {
		t.Fatalf("entry: %+v ok=%v", e, ok)
	}
}

// 4) 删除远端文件再扫描 → 行恢复基线原行，版本再 +1。
func TestRemoteAssetsRemovalBumpsVersion(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	full := filepath.Join(remote, "unit", "unit_414.Android.unity3d")
	writeRemoteFile(t, remote, "unit/unit_414.Android.unity3d", fakeBundle("x"), true)

	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if m.Version() != 101 {
		t.Fatalf("首轮版本 = %d, want 101", m.Version())
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if m.Version() != 102 {
		t.Fatalf("删除后版本 = %d, want 102", m.Version())
	}
	body := string(m.Body())
	if strings.Contains(body, "414") {
		t.Fatalf("删除后仍含 414 行: %s", body)
	}
	// 除 version 行外与基线一致。
	baseRows := strings.Split(strings.TrimSuffix(testManifest, "\n"), "@")
	outRows := strings.Split(strings.TrimSuffix(body, "\n"), "@")
	if len(outRows) != len(baseRows) {
		t.Fatalf("行数 %d != 基线 %d", len(outRows), len(baseRows))
	}
	for i := 1; i < len(baseRows); i++ {
		if outRows[i] != baseRows[i] {
			t.Fatalf("行 %d 不一致: %q vs %q", i, outRows[i], baseRows[i])
		}
	}
}

// 5) 坏魔数 / 无法映射 AssetType / .iPhone → rejected 或忽略，不进清单。
func TestRemoteAssetsRejected(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	writeRemoteFile(t, remote, "unit/unit_900.Android.unity3d",
		[]byte("NOT-UNITY-BYTES"), true) // 坏魔数
	writeRemoteFile(t, remote, "foo/bar_1.Android.unity3d",
		fakeBundle("x"), true) // 无 AssetType
	writeRemoteFile(t, remote, "unit/unit_901.iPhone.unity3d",
		fakeBundle("x"), true) // iPhone → 忽略
	writeRemoteFile(t, remote, "._unit_902.Android.unity3d",
		fakeBundle("x"), true) // macOS 残渣 → 忽略

	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	rep := m.Report()
	if len(rep.Entries) != 0 {
		t.Fatalf("不应有有效条目: %+v", rep.Entries)
	}
	// .iPhone 与 ._* 静默忽略（不进 rejected）；另两个进 rejected。
	if len(rep.Rejected) != 2 {
		t.Fatalf("rejected = %+v, want 2", rep.Rejected)
	}
	reasons := map[string]string{}
	for _, r := range rep.Rejected {
		reasons[r.File] = r.Reason
	}
	if !strings.Contains(reasons["unit/unit_900.Android.unity3d"], "魔数") {
		t.Fatalf("坏魔数原因: %+v", reasons)
	}
	if !strings.Contains(reasons["foo/bar_1.Android.unity3d"], "AssetType") {
		t.Fatalf("无 AssetType 原因: %+v", reasons)
	}
	if string(m.Body()) != testManifest {
		t.Fatal("全 rejected 时 body 应等于基线")
	}
	if m.Version() != 100 {
		t.Fatalf("版本 = %d, want 100", m.Version())
	}
}

// 6) 稳定性：mtime=now 首轮 pending 不下发；第二轮 (size,mtime) 未变 → 下发。
func TestRemoteAssetsStability(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	writeRemoteFile(t, remote, "unit/unit_414.Android.unity3d",
		fakeBundle("fresh"), false) // mtime=now

	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if len(m.Report().Pending) != 1 || len(m.Report().Entries) != 0 {
		t.Fatalf("首轮应 pending: %+v", m.Report())
	}
	if string(m.Body()) != testManifest {
		t.Fatal("pending 文件不应进清单")
	}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	rep := m.Report()
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "unit/unit_414" {
		t.Fatalf("第二轮应下发: %+v", rep.Entries)
	}
	if !strings.Contains(string(m.Body()), "@414,23,1,0,1,") {
		t.Fatal("第二轮清单缺 414 行")
	}
}

// 7) 注入时钟验证 mtime<now-10s 边界：mtime 恰为 now-10s 时不算稳定。
func TestRemoteAssetsStableAgeBoundary(t *testing.T) {
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	full := filepath.Join(remote, "unit", "unit_414.Android.unity3d")
	writeRemoteFile(t, remote, "unit/unit_414.Android.unity3d", fakeBundle("b"), false)

	fixed := time.Now()
	m := newTestMgr(remote, assets, state)
	m.now = func() time.Time { return fixed }
	m.stableAge = 10 * time.Second
	// mtime = fixed-9s → 不稳定（且首轮无观察记录）。
	mt := fixed.Add(-9 * time.Second)
	if err := os.Chtimes(full, mt, mt); err != nil {
		t.Fatal(err)
	}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if len(m.Report().Pending) != 1 {
		t.Fatalf("mtime 9s 应 pending: %+v", m.Report().Pending)
	}
	// mtime = fixed-11s → 首轮即稳定。
	mt2 := fixed.Add(-11 * time.Second)
	if err := os.Chtimes(full, mt2, mt2); err != nil {
		t.Fatal(err)
	}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if len(m.Report().Entries) != 1 {
		t.Fatalf("mtime 11s 应稳定下发: %+v", m.Report())
	}
}

// 9) 真实 unit_414 文件断言 md5 前 8。
//
// 2026-09-27（LEGACY-ASSETS-01）：本测试原断言手工重建版 unit_414 的
// md5 前8 e0f9cdea / sha256 e647b2b5…（UNIT414-REBUILD）。玩家提供的关服前设备包
// 已给出该单位的**原始生产字节**，迁移时按「原始优先」覆盖了重建件，故此处改为
// 断言原始件哈希。旧重建件证据保留在 artifacts/tasks/unit414-rebuild-acceptance/
// （历史证据，不改写）。
func TestRemoteAssetsRealUnit414MD5(t *testing.T) {
	src := filepath.Join("..", "..", "server-state-cn35", "remote_assets",
		"unit", "unit_414.Android.unity3d")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("真实 unit_414 不在默认远端目录: %v", err)
	}
	assets := t.TempDir()
	remote := t.TempDir()
	state := t.TempDir()
	writeBaseManifest(t, assets)
	writeRemoteFile(t, remote, "unit/unit_414.Android.unity3d", raw, true)
	m := newTestMgr(remote, assets, state)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	e, ok := m.Entry("unit/unit_414")
	if !ok {
		t.Fatal("unit_414 未入条目")
	}
	if e.MD5 != "229b8674" {
		t.Fatalf("md5 前8 = %s, want 229b8674（关服前原始生产件）", e.MD5)
	}
	if e.SHA256 != "eb4e1db28e3263c578e3e976ee00a1e25de4ebca632dc2e230dc669b0c7913e5" {
		t.Fatalf("sha256 = %s", e.SHA256)
	}
}
