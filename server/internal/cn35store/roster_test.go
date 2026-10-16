// roster_test.go ROSTER350-03/05/07 名册治理存储层测试：
// 导入幂等/预览回滚/发布校验拒绝/发布原子性/版本回滚/启用名单口径。
package cn35store

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
)

// testImport 加载真实清单+卡片目录为导入输入。
func testImport(t *testing.T) *cn35game.RosterImport {
	t.Helper()
	in, _, err := cn35roster.LoadImport(
		filepath.Join("..", "..", "assets", "cn350"),
		filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("LoadImport: %v", err)
	}
	return in
}

// seedPoolDefs 简化池定义写入（绕过 SeedGachaRoster 直接落草稿面）。
func writeSeedPools(t *testing.T, s *Store) {
	t.Helper()
	for _, def := range cn35game.GachaSeedPools() {
		if err := s.SaveGachaPoolDef(def, "test"); err != nil {
			t.Fatalf("池定义 %s: %v", def.PoolID, err)
		}
	}
}

// enableMembers 把成员 id 集在名册上置 enabled。
func enableMembers(t *testing.T, s *Store, members map[string][]cn35game.GachaDraftMember) {
	t.Helper()
	var ids []int
	for _, ms := range members {
		for _, m := range ms {
			ids = append(ids, m.UnitID)
		}
	}
	yes := true
	if err := s.SetRosterFlags(ids, &yes, nil, "test-enable", "test"); err != nil {
		t.Fatalf("名册启用: %v", err)
	}
}

// TestRosterImportIdempotent 同一份导入重复执行不产生新写（修订史不增长）。
func TestRosterImportIdempotent(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)

	res1, err := s.ImportRoster(in, true, "test")
	if err != nil {
		t.Fatalf("首次导入: %v", err)
	}
	if res1.AssetRows == 0 || res1.TemplateUpserts == 0 {
		t.Fatalf("首次导入应有写入: %+v", res1)
	}
	res2, err := s.ImportRoster(in, true, "test")
	if err != nil {
		t.Fatalf("二次导入: %v", err)
	}
	if res2.TemplateUpserts != 0 {
		t.Fatalf("幂等二次导入不应有模板 upsert: %+v", res2)
	}
	// 修订史条数应等于模板数（二次导入不新增）。
	var revs int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_unit_template_revisions`).Scan(&revs); err != nil {
		t.Fatal(err)
	}
	if revs != res1.TemplateUpserts {
		t.Fatalf("修订史=%d 应等于首次 upsert 数 %d", revs, res1.TemplateUpserts)
	}
	// 资源证据行数与清单一致（394 合格单位 × 8 必需族，按清单实际计）。
	var assets int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_unit_assets`).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if assets != res1.AssetRows {
		t.Fatalf("资源行=%d want %d", assets, res1.AssetRows)
	}
}

// TestRosterImportPreviewRollsBack 预览导入整包回滚，不留任何行。
func TestRosterImportPreviewRollsBack(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	res, err := s.ImportRoster(in, false, "test")
	if err != nil {
		t.Fatalf("预览导入: %v", err)
	}
	if res.Committed {
		t.Fatal("预览结果不应标记 committed")
	}
	for _, table := range []string{
		"cn35_unit_templates", "cn35_unit_assets", "cn35_unit_roster",
		"cn35_roster_sources", "cn35_admin_audit",
	} {
		var n int
		if err := s.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("预览回滚后 %s 应为空，实际 %d 行", table, n)
		}
	}
}

// TestPublishRejectsGoldPoolUR 金币池含 5 星成员 → 发布拒绝且零写入。
func TestPublishRejectsGoldPoolUR(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)

	members := cn35game.GachaSeedMembers(in.Manifest)
	// 找一个真实 5 星合格单位塞进金币池。
	var ur int
	for _, m := range members["2"] {
		id := m.UnitID
		tpl, ok, err := s.UnitTemplate(id)
		if err != nil || !ok {
			continue
		}
		if tpl.Rare == 5 {
			ur = id
			break
		}
	}
	if ur == 0 {
		t.Skip("清单无 5 星成员可构造")
	}
	enableMembers(t, s, members)
	yes := true
	if err := s.SetRosterFlags([]int{ur}, &yes, nil, "test-ur", "test"); err != nil {
		t.Fatal(err)
	}
	members["3"] = append(members["3"], cn35game.GachaDraftMember{PoolID: "3", UnitID: ur, Weight: 1})
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.PublishGachaDraft("test", "gold-ur")
	var pubErr *cn35game.GachaPublishError
	if !errors.As(err, &pubErr) {
		t.Fatalf("金币池含5星应被发布校验拒绝，实际 err=%v", err)
	}
	// 原子性：失败发布不产生版本行、不动生效指针。
	var vs, rel int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_pool_versions`).Scan(&vs)
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_release`).Scan(&rel)
	if vs != 0 || rel != 0 {
		t.Fatalf("失败发布留下了版本/指针: versions=%d release=%d", vs, rel)
	}
}

// TestPublishRejectsDisabledPoolMembers 停用池仍有成员 → 发布拒绝。
func TestPublishRejectsDisabledPoolMembers(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	// 往停用池 1 塞一个合法成员。
	extra := members["2"][0]
	if err := s.ReplaceDraftMembers("1",
		[]cn35game.GachaDraftMember{{PoolID: "1", UnitID: extra.UnitID, Weight: 1}}, "test"); err != nil {
		t.Fatal(err)
	}
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PublishGachaDraft("test", "disabled-has-members"); err == nil {
		t.Fatal("停用池带成员的发布应被拒绝")
	}
}

// seedPoolDef 取回某池的种子定义（测试据此做受控变更）。
func seedPoolDef(t *testing.T, id string) cn35game.GachaPoolDef {
	t.Helper()
	for _, d := range cn35game.GachaSeedPools() {
		if d.PoolID == id {
			return d
		}
	}
	t.Fatalf("种子池 %s 不存在", id)
	return cn35game.GachaPoolDef{}
}

// TestPublishEmptyNonURMatchesEngineFallback non_ur_weights 缺省时，
// 发布可达集合与引擎回退 {SR,HR} 同口径：
//   - 正例：SR/HR/UR 成员齐备 → 发布通过（修复前会误判 30/40 不可达）；
//   - 反例：只留 5★ 成员 → SR/HR 可达但无候选 → 拒绝（否则发布出
//     运行期约 98% 抽取失败的死池）。
func TestPublishEmptyNonURMatchesEngineFallback(t *testing.T) {
	in := testImport(t)

	// 正例。
	s := openStore(t, t.TempDir())
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	def := seedPoolDef(t, "2")
	def.Pity.NonURWeights = nil
	if err := s.SaveGachaPoolDef(def, "test"); err != nil {
		t.Fatal(err)
	}
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PublishGachaDraft("test", "empty-nonur-full"); err != nil {
		t.Fatalf("缺省 non_ur_weights + 全档成员应可发布: %v", err)
	}

	// 反例：同配置下钻石池只留 5★ 成员。
	s2 := openStore(t, t.TempDir())
	if _, err := s2.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s2)
	if err := s2.SaveGachaPoolDef(def, "test"); err != nil {
		t.Fatal(err)
	}
	var urOnly []cn35game.GachaDraftMember
	for _, m := range members["2"] {
		tpl, ok, err := s2.UnitTemplate(m.UnitID)
		if err == nil && ok && tpl.Rare == 5 {
			urOnly = append(urOnly, m)
		}
	}
	if len(urOnly) == 0 {
		t.Skip("清单无 5 星成员可构造")
	}
	enableMembers(t, s2, map[string][]cn35game.GachaDraftMember{"2": urOnly, "3": members["3"]})
	if err := s2.ReplaceDraftMembers("2", urOnly, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s2.ReplaceDraftMembers("3", members["3"], "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.PublishGachaDraft("test", "ur-only-empty-nonur"); err == nil {
		t.Fatal("non_ur_weights 缺省且仅 5★ 成员的发布应被拒绝（SR/HR 可达但无成员）")
	}
}

// TestPublishRejectsWeightKeysOutOfDomain non_ur_weights/distribution 的
// 稀有度键限定 10/20/30/40：50 会绕过保底通道偷出 5★、60+ 是超客户端
// 契约的稀有度，发布期必须拒绝。
func TestPublishRejectsWeightKeysOutOfDomain(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	// 钻石池 non_ur_weights 混入 50 键。
	d2 := seedPoolDef(t, "2")
	d2.Pity.NonURWeights = map[int]int{50: 5, 40: 18, 30: 80}
	if err := s.SaveGachaPoolDef(d2, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishGachaDraft("test", "bad-nonur-key"); err == nil {
		t.Fatal("non_ur_weights 含 50 键的发布应被拒绝")
	}
	// 恢复合法池2，再给金币池 distribution 混入 60 键。
	d2.Pity.NonURWeights = map[int]int{40: 18, 30: 80}
	if err := s.SaveGachaPoolDef(d2, "test"); err != nil {
		t.Fatal(err)
	}
	d3 := seedPoolDef(t, "3")
	d3.Distribution = map[int]int{10: 70, 20: 24, 30: 5, 40: 1, 60: 1}
	if err := s.SaveGachaPoolDef(d3, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishGachaDraft("test", "bad-dist-key"); err == nil {
		t.Fatal("distribution 含 60 键的发布应被拒绝")
	}
}

// TestPublishAndRollback 发布后回滚：版本行不删，生效指针回 v1。
func TestPublishAndRollback(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, _, err := cn35game.SeedGachaRoster(s, testImport(t), "test"); err != nil {
		t.Fatalf("种子化: %v", err)
	}
	cfg1, err := s.ActiveGachaConfig()
	if err != nil || cfg1 == nil {
		t.Fatalf("种子后应有生效配置: %v", err)
	}
	// 双池齐全且停用槽不在发布面。
	gotPools := map[string]bool{}
	for _, p := range cfg1.Rules.Pools {
		gotPools[p.PoolID] = true
	}
	if !gotPools["2"] || !gotPools["3"] || len(gotPools) != 2 {
		t.Fatalf("生效配置应为 2+3 双池: %v", gotPools)
	}

	// 改草稿再发 v2（去掉金币池一名成员制造差异）。
	ms, err := s.GachaDraftMembers()
	if err != nil {
		t.Fatal(err)
	}
	less := append([]cn35game.GachaDraftMember{}, ms["3"][1:]...)
	if err := s.ReplaceDraftMembers("3", less, "test"); err != nil {
		t.Fatal(err)
	}
	v2, err := s.PublishGachaDraft("test", "v2")
	if err != nil {
		t.Fatalf("第二次发布: %v", err)
	}
	if v2.Version == cfg1.Version {
		t.Fatal("差异发布不应复用同版本号")
	}
	cfg2, err := s.ActiveGachaConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Version != v2.Version {
		t.Fatalf("生效版本=%d want %d", cfg2.Version, v2.Version)
	}

	// 回滚 v1：指针回移，v2 行保留。
	if err := s.ActivateGachaVersion(cfg1.Version, "test", "rollback"); err != nil {
		t.Fatal(err)
	}
	back, err := s.ActiveGachaConfig()
	if err != nil || back.Version != cfg1.Version {
		t.Fatalf("回滚后版本=%v want %d", back.Version, cfg1.Version)
	}
	if len(back.MembersByPool["3"]) != len(ms["3"]) {
		t.Fatalf("回滚后金币池成员=%d want %d", len(back.MembersByPool["3"]), len(ms["3"]))
	}
	var vcount int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_pool_versions`).Scan(&vcount)
	if vcount != 2 {
		t.Fatalf("回滚不应删除版本行: versions=%d", vcount)
	}
	// 审计链完整。
	audits, err := s.AuditLog(50)
	if err != nil {
		t.Fatal(err)
	}
	acts := map[string]bool{}
	for _, a := range audits {
		acts[a.Action] = true
	}
	for _, want := range []string{"roster.import", "pool.publish", "pool.activate"} {
		if !acts[want] {
			t.Fatalf("审计缺动作 %s: %v", want, acts)
		}
	}
}

// TestActiveConfigRequiresPublish 未发布时返回 nil（运行期执行面据此拒绝）。
func TestActiveConfigRequiresPublish(t *testing.T) {
	s := openStore(t, t.TempDir())
	cfg, err := s.ActiveGachaConfig()
	if err != nil || cfg != nil {
		t.Fatalf("未发布应返回 nil,nil: %v %v", cfg, err)
	}
}

// TestPublishRejectsGoldPoolPityGroup 金币池绑定保底组必须拒绝——
// 保底计数器按组共享，金币消耗混入会跨池推进钻石保底（经济漏洞）。
func TestPublishRejectsGoldPoolPityGroup(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	d3 := seedPoolDef(t, "3")
	d3.PityGroup = "cn350-diamond-regular"
	if err := s.SaveGachaPoolDef(d3, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.PublishGachaDraft("test", "gold-pity-group")
	if err == nil || !strings.Contains(err.Error(), "保底组") {
		t.Fatalf("金币池配保底组应被拒: %v", err)
	}
}

// TestPublishRejectsLimitedPoolNoWindow 启用的限时池缺时间窗必须拒绝——
// gachaPoolOpen 对 begin/end<=0 恒判未开放，发布出去就是占槽死池。
func TestPublishRejectsLimitedPoolNoWindow(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	// 启用槽1 限时池（begin=end=0），按保底可达档 30/40/50 各给一名成员。
	d1 := seedPoolDef(t, "1")
	d1.Enabled = true
	if err := s.SaveGachaPoolDef(d1, "test"); err != nil {
		t.Fatal(err)
	}
	byRare := map[int]int{}
	var lm []cn35game.GachaDraftMember
	for _, m := range members["2"] {
		tpl, ok, err := s.UnitTemplate(m.UnitID)
		if err != nil || !ok {
			continue
		}
		if byRare[tpl.Rare] == 0 && tpl.Rare >= 3 {
			byRare[tpl.Rare] = 1
			lm = append(lm, m)
		}
	}
	if err := s.ReplaceDraftMembers("1", lm, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.PublishGachaDraft("test", "limited-no-window")
	if err == nil || !strings.Contains(err.Error(), "时间窗") {
		t.Fatalf("限时池缺时间窗应被拒: %v", err)
	}
}

// TestPublishRejectsFeaturedNonUR UP 对象必须是池内 5 星成员——
// 否则引擎建池即报错（死池），发布期必须拦下。
func TestPublishRejectsFeaturedNonUR(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	// 找一个 4★ 成员当非法 UP。
	var sr int
	for _, m := range members["2"] {
		tpl, ok, err := s.UnitTemplate(m.UnitID)
		if err == nil && ok && tpl.Rare == 4 {
			sr = m.UnitID
			break
		}
	}
	if sr == 0 {
		t.Skip("清单无 4 星成员可构造")
	}
	d2 := seedPoolDef(t, "2")
	d2.FeaturedIDs = []string{strconv.Itoa(sr)}
	if err := s.SaveGachaPoolDef(d2, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.PublishGachaDraft("test", "featured-nonur")
	if err == nil || !strings.Contains(err.Error(), "5 星成员") {
		t.Fatalf("非 5 星 UP 对象应被拒: %v", err)
	}
}

// TestPublishRejectsViaReadback "校验未逐条复制"的约束（如常驻池携带
// 限时字段——DB 无 CHECK 拦截）由快照自洽回读兜底：拒绝发布
// "写库成功、加载即失败"的版本。（max_bonus 有 DB CHECK，不作此例。）
func TestPublishRejectsViaReadback(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	writeSeedPools(t, s)
	members := cn35game.GachaSeedMembers(in.Manifest)
	enableMembers(t, s, members)
	for pid, ms := range members {
		if err := s.ReplaceDraftMembers(pid, ms, "test"); err != nil {
			t.Fatal(err)
		}
	}
	d3 := seedPoolDef(t, "3")
	d3.BeginAt, d3.EndAt = 1, 2 // 常驻池携带限时字段：结构校验未拦截
	if err := s.SaveGachaPoolDef(d3, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.PublishGachaDraft("test", "nonlimited-with-window")
	if err == nil || !strings.Contains(err.Error(), "回读") {
		t.Fatalf("常驻池携带限时字段应被自洽回读拦截: %v", err)
	}
	var vs int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_pool_versions`).Scan(&vs)
	if vs != 0 {
		t.Fatalf("失败发布不应产生版本行: %d", vs)
	}
}

// TestImportBundleNames 证据行 bundle 必须与 served 索引键同源：
// unit/unit_<fam>_<id>（fam=unit 时 unit/unit_<id>）。
func TestImportBundleNames(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.ImportRoster(testImport(t), true, "test"); err != nil {
		t.Fatal(err)
	}
	check := func(fam, want string) {
		t.Helper()
		var bundle string
		if err := s.DB().QueryRow(
			`SELECT bundle FROM cn35_unit_assets WHERE unit_id=152 AND family=?`, fam).
			Scan(&bundle); err != nil {
			t.Fatalf("152/%s 证据行: %v", fam, err)
		}
		if bundle != want {
			t.Fatalf("family=%s bundle=%q want %q", fam, bundle, want)
		}
	}
	check("unit", "unit/unit_152")
	check("btn", "unit/unit_btn_152")
	check("gray_btn", "unit/unit_gray_btn_152")
	check("list", "unit/unit_list_152")
	check("square", "unit/unit_square_152")
	check("large", "unit/unit_large_152")
	check("rectangle", "unit/unit_rectangle_152")
	check("silhouette", "unit/unit_silhouette_152")
	// 任意 large_ns 行也遵循同一形态。
	var ns string
	if err := s.DB().QueryRow(
		`SELECT bundle FROM cn35_unit_assets WHERE family='large_ns' LIMIT 1`).Scan(&ns); err == nil {
		if !strings.HasPrefix(ns, "unit/unit_large_ns_") {
			t.Fatalf("large_ns bundle=%q 前缀非法", ns)
		}
	}
}

// TestImportMissingNormalizedAndCompleteFilter missing 列写入统一 '[]'
// 而非 'null'；complete=full/partial 过滤互补覆盖全部有模板的名册行。
func TestImportMissingNormalizedAndCompleteFilter(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.ImportRoster(testImport(t), true, "test"); err != nil {
		t.Fatal(err)
	}
	var nulls int
	if err := s.DB().QueryRow(
		`SELECT count(*) FROM cn35_unit_templates WHERE missing='null'`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 0 {
		t.Fatalf("missing 应归一化为 '[]'，残留 'null' 行 %d", nulls)
	}
	_, full, err := s.RosterUnits(cn35game.RosterFilter{Complete: "full"})
	if err != nil {
		t.Fatal(err)
	}
	_, partial, err := s.RosterUnits(cn35game.RosterFilter{Complete: "partial"})
	if err != nil {
		t.Fatal(err)
	}
	var wantFull, wantPartial int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_unit_roster r
		JOIN cn35_unit_templates t ON t.unit_id=r.unit_id
		WHERE t.missing IN ('[]','null')`).Scan(&wantFull); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_unit_roster r
		JOIN cn35_unit_templates t ON t.unit_id=r.unit_id
		WHERE t.missing NOT IN ('[]','null')`).Scan(&wantPartial); err != nil {
		t.Fatal(err)
	}
	if full != wantFull || partial != wantPartial {
		t.Fatalf("complete 过滤 full=%d(want %d) partial=%d(want %d)",
			full, wantFull, partial, wantPartial)
	}
	// 取一个无缺失字段的单位，确认它只出现在 full 一侧。
	var completeID int
	if err := s.DB().QueryRow(
		`SELECT unit_id FROM cn35_unit_templates WHERE missing='[]' LIMIT 1`).Scan(&completeID); err != nil {
		t.Skip("无完整模板单位")
	}
	rows, _, err := s.RosterUnits(cn35game.RosterFilter{Complete: "full", Query: strconv.Itoa(completeID)})
	if err != nil || len(rows) != 1 {
		t.Fatalf("完整单位 %d 应在 full 侧: rows=%d err=%v", completeID, len(rows), err)
	}
	rows, _, err = s.RosterUnits(cn35game.RosterFilter{Complete: "partial", Query: strconv.Itoa(completeID)})
	if err != nil || len(rows) != 0 {
		t.Fatalf("完整单位 %d 不应在 partial 侧: rows=%d err=%v", completeID, len(rows), err)
	}
}

// TestImportResetsStaleEvidence 清单重生成后撤收的单位，再导入必须
// 清空陈旧资源证据（resource_ok=0、missing_required=全部必需族、
// assets 行消失）——清单是资源证据唯一真源。
func TestImportResetsStaleEvidence(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	d, err := s.RosterUnitDetail(152)
	if err != nil || !d.Roster.ResourceOK || len(d.Assets) == 0 {
		t.Fatalf("首导后 152 应有资源证据: %+v err=%v", d.Roster, err)
	}
	// 模拟清单重生成后撤收 152。
	delete(in.Manifest.Units, "152")
	res, err := s.ImportRoster(in, true, "test")
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.RosterUnitDetail(152)
	if err != nil {
		t.Fatal(err)
	}
	if d.Roster.ResourceOK {
		t.Fatal("清单撤收后 152 resource_ok 应归零")
	}
	if len(d.Assets) != 0 {
		t.Fatalf("清单撤收后 152 不应有证据行: %d", len(d.Assets))
	}
	if len(d.Roster.MissingRequired) != len(cn35game.RosterRequiredFamilies) {
		t.Fatalf("152 missing_required=%v 应为全部必需族", d.Roster.MissingRequired)
	}
	if !strings.Contains(strings.Join(res.Notes, ";"), "重置") {
		t.Fatalf("导入结果应记录陈旧行重置说明: %v", res.Notes)
	}
}

// TestRosterFlagsPersist 管理位与审计落库；导入不覆盖管理位。
func TestRosterFlagsPersist(t *testing.T) {
	s := openStore(t, t.TempDir())
	in := testImport(t)
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	no := false
	if err := s.SetRosterFlags([]int{152}, &no, nil, "手动停用", "test"); err != nil {
		t.Fatal(err)
	}
	d, err := s.RosterUnitDetail(152)
	if err != nil {
		t.Fatal(err)
	}
	if d.Roster.Enabled || !d.Roster.ResourceOK {
		t.Fatalf("152 应为 disabled+resource_ok: %+v", d.Roster)
	}
	// 再导入不覆盖 enabled。
	if _, err := s.ImportRoster(in, true, "test"); err != nil {
		t.Fatal(err)
	}
	d, err = s.RosterUnitDetail(152)
	if err != nil {
		t.Fatal(err)
	}
	if d.Roster.Enabled {
		t.Fatal("导入不应覆盖管理位 enabled")
	}
	if len(d.Assets) == 0 {
		t.Fatal("152 应有资源证据行")
	}
}
