// cn35game_test.go CN 3.5.0 启动链契约测试：以 cn35protocol 编码 API 模拟
// 客户端（与设备发送同一条管线），走 metaInfo→heQuickLogin→getServerList→
// chooseServer(S0播种)→version_info→preset_data→asset_info/app_conf 全链。
package cn35game_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func testServer(t *testing.T) (*httptest.Server, *cn35wire.MemoryStore) {
	t.Helper()
	assets := filepath.Join("..", "..", "assets", "cn350")
	if _, err := os.Stat(filepath.Join(assets, "serve_preset_data.json")); err != nil {
		t.Skipf("cn350 fixture 目录不可用: %v", err)
	}
	sessions := cn35wire.NewMemoryStore()
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   cn35game.NewMemoryAccounts(),
		Sessions:   sessions,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  assets,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, sessions
}

// verifyCall 构造并发送一条客户端方向 type3 请求，返回解出的 results[]。
func verifyCall(t *testing.T, base string, biz string) map[string]any {
	t.Helper()
	env := `[{"v":1316,"mv":"1","ct":2,"sk":"gip-eurhieur8387583979385798327958udfhdjf","aid":"11","lang":"zh_CN","pf":"uc","notify":true,"st":0},[` + biz + `]]`
	wire, err := cn35protocol.EncodeAccountRequest([]byte(env))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(base+"/account?uid=666666", "application/octet-stream", strings.NewReader(string(wire)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw := readAll(t, resp)
	payload, _, err := cn35protocol.DecodeAccountResponse(raw)
	if err != nil {
		t.Fatalf("verify 响应解码: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("verify 响应 JSON: %v", err)
	}
	return doc
}

func verifyCallSeeded(t *testing.T, base string, biz string, s1 string) (map[string]any, string) {
	t.Helper()
	env := `[{"v":1316,"mv":"1","ct":2,"sk":"k","aid":"11","lang":"zh_CN","pf":"uc","notify":true,"st":1},[` + biz + `]]`
	plain := `{"r":"` + s1 + `","m":"deadbeef","data":` + env + `}`
	wire, err := cn35protocol.EncodeAccountRequest([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(base+"/account?uid=666666", "application/octet-stream", strings.NewReader(string(wire)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw := readAll(t, resp)
	payload, seeded, err := cn35protocol.DecodeAccountResponse(raw)
	if err != nil {
		t.Fatalf("chooseServer 响应解码: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, seeded
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	return body
}

func results0(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	res, ok := doc["results"].([]any)
	if !ok || len(res) == 0 {
		t.Fatalf("results 缺失: %v", doc)
	}
	return res[0].(map[string]any)
}

// gameGet 模拟客户端游戏通道 GET（_method=GET 表单 + ?e= 加密 + 身份头）。
func gameCall(t *testing.T, base, path, uid, sk, s0, s1, query, form string) []byte {
	t.Helper()
	url := base + path
	if query != "" {
		e, err := cn35protocol.EncryptQuery(query, "1700000000", s0, s1)
		if err != nil {
			t.Fatal(err)
		}
		url += "?e=" + e
	}
	var body []byte
	if form == "" {
		form = "_method=GET"
	}
	body, err := cn35protocol.EncryptFormBody(form, "1700000000", s0, s1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, uid)
	id4, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, sk)
	appT, _ := cn35protocol.EncryptAPP_T("1700000000", s0, s1)
	req.Header.Set("APP_ID_1", id1)
	req.Header.Set("APP_ID_4", id4)
	req.Header.Set("APP_T", appT)
	req.Header.Set("Encrypted", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw := readAll(t, resp)
	if resp.Header.Get("ENCRYPTED") != "true" {
		t.Fatalf("game 响应未加密: %s", raw[:min(len(raw), 200)])
	}
	plain, err := cn35protocol.DecodeGameResponse(raw, s0, s1)
	if err != nil {
		t.Fatalf("game 响应解码: %v", err)
	}
	return plain
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestStartupChain 完整启动链：登录→选服（S0 播种）→版本→快照→静态。
func TestStartupChain(t *testing.T) {
	ts, sessions := testServer(t)

	// 1) metaInfo：必经、无副作用
	doc := verifyCall(t, ts.URL, `{"method":"metaInfo","metaInfo":"device=x"}`)
	if r := results0(t, doc); r["retCode"].(float64) != 200 {
		t.Fatalf("metaInfo: %v", r)
	}

	// 2) heQuickLogin：新账号 → success+heAccountId+token+firstActive
	doc = verifyCall(t, ts.URL, `{"method":"heQuickLogin","randomStr":"rs-abc","deviceId":"dev-001"}`)
	r := results0(t, doc)
	if r["success"] != true || r["randomStr"] != "rs-abc" {
		t.Fatalf("heQuickLogin: %v", r)
	}
	heID, token := r["heAccountId"].(string), r["token"].(string)
	if heID == "" || token == "" {
		t.Fatal("heAccountId/token 为空")
	}
	if ev, _ := doc["events"].([]any); len(ev) == 0 {
		t.Fatal("新账号应下发 firstActive 事件")
	}
	// 幂等：同 deviceId 重进返回同一账号（token 随重登轮换，用最新值）
	doc = verifyCall(t, ts.URL, `{"method":"heQuickLogin","randomStr":"rs-2","deviceId":"dev-001"}`)
	if r2 := results0(t, doc); r2["heAccountId"] != heID {
		t.Fatalf("deviceId 幂等失败: %v vs %v", r2["heAccountId"], heID)
	} else {
		token = r2["token"].(string)
	}

	// 3) getServerList
	doc = verifyCall(t, ts.URL, `{"method":"getServerList","token":"`+token+`","heAccountId":"`+heID+`","deviceId":"dev-001","pf":"uc","clientVersion":"1316","b":"1","metaInfo2":"x"}`)
	r = results0(t, doc)
	if r["success"] != true {
		t.Fatalf("getServerList: %v", r)
	}
	srv := r["servers"].([]any)[0].(map[string]any)
	if srv["serverIP"] != "127.0.0.1:18091" || srv["assetInfoServerIp"] != "127.0.0.1:18091" || srv["staticHostIp"] != "127.0.0.1:18091" {
		t.Fatalf("servers 条目: %v", srv)
	}

	// 4) chooseServer：信封包装 + S0 播种
	s1 := "S1saltS1saltS1saltS1saltS1salt12" // 32B
	doc, seeded := verifyCallSeeded(t, ts.URL, `{"method":"chooseServer","heAccountId":"`+heID+`","token":"`+token+`","deviceId":"dev-001","deviceName":"meizu","deviceBootTime":"1","serverId":"1"}`, s1)
	r = results0(t, doc)
	if r["success"] != true {
		t.Fatalf("chooseServer: %v", r)
	}
	uid, sk := r["userId"].(string), r["sk"].(string)
	if uid == "" || sk == "" || seeded == "" || len(seeded) != 32 {
		t.Fatalf("chooseServer 缺字段/未播种: uid=%q sk=%q seeded=%q", uid, sk, seeded)
	}
	// 会话绑定核验
	sess, err := sessions.Load(uid)
	if err != nil || sess.S1 != s1 || sess.S0 != seeded || sess.SK != sk {
		t.Fatalf("会话绑定: %+v err=%v", sess, err)
	}

	// 5) version_info：非零版本与 fixture 一致
	plain := gameCall(t, ts.URL, "/users/version_info", uid, sk, seeded, s1, "v=1316", "_method=GET")
	var vi map[string]any
	if err := json.Unmarshal(plain, &vi); err != nil {
		t.Fatal(err)
	}
	// app_conf_version 直接读 serve_app_conf_android.json 的 version（04 面
	// 会随页面配置迭代 bump——断言与 fixture 当前值对齐，不锁死历史值）。
	confRaw, err := os.ReadFile(filepath.Join("..", "..", "assets", "cn350", "serve_app_conf_android.json"))
	if err != nil {
		t.Fatalf("读取 app_conf fixture: %v", err)
	}
	var confVer struct {
		Version float64 `json:"version"`
	}
	if err := json.Unmarshal(confRaw, &confVer); err != nil || confVer.Version == 0 {
		t.Fatalf("app_conf fixture version 非法: %v", err)
	}
	// asset_info_version 同理读 serve_asset_info fixture 的 `version,<n>`
	// 首段（CDN 下发资产增删会 bump——与服务端 loadVersions 同一事实源）。
	aiRaw, err := os.ReadFile(filepath.Join("..", "..", "assets", "cn350", "serve_asset_info_android4_v2_short.txt"))
	if err != nil {
		t.Fatalf("读取 asset_info fixture: %v", err)
	}
	aiFirst, _, _ := strings.Cut(string(aiRaw), "@")
	aiVer, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(aiFirst, "version,")), 10, 64)
	if err != nil || aiVer == 0 {
		t.Fatalf("asset_info fixture version 非法: %q err=%v", aiFirst, err)
	}
	if vi["asset_info_version"].(float64) != float64(aiVer) || vi["app_conf_version"].(float64) != confVer.Version {
		t.Fatalf("version_info: %v (fixture version %v)", vi, confVer.Version)
	}

	// 6) preset_data（带 tutorial_session=true）：动态 user 注入 + consts.time 时钟
	plain = gameCall(t, ts.URL, "/users/preset_data.json", uid, sk, seeded, s1, "tutorial_session=true", "_method=GET")
	var pd map[string]any
	if err := json.Unmarshal(plain, &pd); err != nil {
		t.Fatal(err)
	}
	data := pd["data"].(map[string]any)
	user := data["user"].(map[string]any)
	if user["id"] != uid {
		t.Fatalf("data.user.id=%v want %v", user["id"], uid)
	}
	if _, bad := user["game_status"]; bad {
		t.Fatalf("新账号不得下发 game_status: %v", user)
	}
	// PLAY350-07：leader_unit_id 按契约下发 JSON NUMBER（i() 比较用）。
	if user["leader_unit_id"].(float64) != 348 {
		t.Fatalf("leader_unit_id=%v（须 NUMBER 348/375 内嵌约束）", user["leader_unit_id"])
	}
	consts := data["consts"].(map[string]any)
	if consts["task_v2"] != true || consts["load_quest1"] != true {
		t.Fatalf("consts 教程开关: %v", consts)
	}
	if tsec := consts["time"].(float64); tsec < float64(time.Now().Unix()-60) {
		t.Fatalf("consts.time 未注入服务端时钟: %v", tsec)
	}
	tsess := data["tutorial_session"].(map[string]any)
	val := tsess["value"].(map[string]any)
	if _, ok := val["user_skills"].([]any); !ok {
		t.Fatalf("tutorial_session.value.user_skills 缺失: %v", val)
	}
	if _, ok := val["user_monsters"].([]any); !ok {
		t.Fatalf("tutorial_session.value.user_monsters 缺失: %v", val)
	}
	if data["user_coin"].(map[string]any)["amount"] == nil {
		t.Fatal("user_coin 缺失")
	}

	// 7) asset_info 静态：明文 @ 格式，首段 version
	resp, err := http.Get(ts.URL + "/asset_info_android4_v2_short.json.encode?version=20260101")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), aiFirst+"@") {
		t.Fatalf("asset_info 首段: %.40q", body)
	}

	// 8) app_conf：RenderWire 后紧凑 JSON，含 pages/partials
	resp, err = http.Get(ts.URL + "/app_conf_android.json.encode?version=20260101")
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, resp)
	resp.Body.Close()
	var conf map[string]any
	if err := json.Unmarshal(body, &conf); err != nil {
		t.Fatalf("app_conf 非法 JSON: %v body(%d)=%q", err, len(body), body[:min(len(body), 300)])
	}
	if _, ok := conf["pages"]; !ok {
		t.Fatal("app_conf 缺 pages")
	}
	if strings.Contains(string(body), `"_doc"`) {
		t.Fatal("RenderWire 未剥离 _ 注解键")
	}
	// version 与 version_info 一致即够：fixture 版本随 04 页面迭代 bump。
	if conf["version"].(float64) <= 0 {
		t.Fatalf("app_conf version=%v", conf["version"])
	}
}

// TestChooseServerBadToken 错误凭据不得建立会话/播种。
func TestChooseServerBadToken(t *testing.T) {
	ts, sessions := testServer(t)
	doc := verifyCall(t, ts.URL, `{"method":"heQuickLogin","randomStr":"r","deviceId":"d9"}`)
	heID := results0(t, doc)["heAccountId"].(string)

	doc, seeded := verifyCallSeeded(t, ts.URL,
		`{"method":"chooseServer","heAccountId":"`+heID+`","token":"wrong","deviceId":"d9","serverId":"1"}`,
		strings.Repeat("s", 32))
	if r := results0(t, doc); r["success"] != false {
		t.Fatalf("坏 token 应 success=false: %v", r)
	}
	if seeded != "" {
		t.Fatal("失败响应不得播种 S0")
	}
	if _, err := sessions.Load("anything"); err == nil {
		t.Fatal("不应建立任何会话")
	}
}

// TestGameBeforeChooseServer 会话未绑定时游戏请求必须受控拒绝。
func TestGameBeforeChooseServer(t *testing.T) {
	ts, _ := testServer(t)
	uid := "nouser"
	s0, s1 := strings.Repeat("0", 32), strings.Repeat("1", 32)
	url := ts.URL + "/users/version_info"
	body, _ := cn35protocol.EncryptFormBody("_method=GET", "1700000000", s0, s1)
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	id1, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, uid)
	req.Header.Set("APP_ID_1", id1)
	req.Header.Set("Encrypted", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("未绑定会话请求不得 200")
	}
}

// TestUnknownMethodFallback 平台动态 method 兜底 echo+200。
func TestUnknownMethodFallback(t *testing.T) {
	ts, _ := testServer(t)
	doc := verifyCall(t, ts.URL, `{"method":"uc.identifier","foo":1}`)
	if r := results0(t, doc); r["retCode"].(float64) != 200 || r["method"] != "uc.identifier" {
		t.Fatalf("兜底: %v", r)
	}
}

// TestAccountBindWire heAccountBind 全链（契约 05）：游客转正 → 另一设备
// 继承接管 → 错密受控失败。回执恒带 success/msg——旧兜底缺 success 会让
// 客户端 `_json["success"].b` NRE 软锁，本测试锁定该回归面。
func TestAccountBindWire(t *testing.T) {
	ts, _ := testServer(t)
	doc := verifyCall(t, ts.URL, `{"method":"heQuickLogin","randomStr":"r","deviceId":"dev-bind-w"}`)
	r := results0(t, doc)
	heID, token := r["heAccountId"].(string), r["token"].(string)

	// 转正：原 heAccountId 保留、token 轮换、带 msg
	doc = verifyCall(t, ts.URL,
		`{"method":"heAccountBind","accountName":"wire-acc","accountPwd":"pw-1","deviceId":"dev-bind-w","pf":"uc","bindEmail":"a@b.c","heAccountId":"`+heID+`","token":"`+token+`"}`)
	r = results0(t, doc)
	if r["success"] != true || r["heAccountId"] != heID {
		t.Fatalf("转正回执: %v", r)
	}
	if msg, _ := r["msg"].(string); msg == "" {
		t.Fatalf("成功回执应带 msg: %v", r)
	}
	if nt, _ := r["token"].(string); nt == "" || nt == token {
		t.Fatalf("token 应轮换: %v", r)
	}

	// 继承：另一设备游客凭据 + 已注册账密 → 返回被继承账号身份
	doc = verifyCall(t, ts.URL, `{"method":"heQuickLogin","randomStr":"r","deviceId":"dev-bind-x"}`)
	r2 := results0(t, doc)
	doc = verifyCall(t, ts.URL,
		`{"method":"heAccountBind","accountName":"wire-acc","accountPwd":"pw-1","deviceId":"dev-bind-x","pf":"uc","heAccountId":"`+r2["heAccountId"].(string)+`","token":"`+r2["token"].(string)+`"}`)
	r = results0(t, doc)
	if r["success"] != true || r["heAccountId"] != heID {
		t.Fatalf("继承应返回被继承账号 heAccountId: %v", r)
	}

	// 失败：错密 → success:false + msg（客户端弹错误框而非软锁）
	doc = verifyCall(t, ts.URL,
		`{"method":"heAccountBind","accountName":"wire-acc","accountPwd":"bad","deviceId":"dev-bind-x","pf":"uc","heAccountId":"","token":""}`)
	r = results0(t, doc)
	if r["success"] != false {
		t.Fatalf("错密回执: %v", r)
	}
	if msg, _ := r["msg"].(string); msg == "" {
		t.Fatalf("失败回执应带 msg: %v", r)
	}
}

// establishSession 走一遍最小登录链（heQuickLogin→chooseServer）返回会话四元组。
func establishSession(t *testing.T, base, deviceID string) (uid, sk, s0, s1 string) {
	t.Helper()
	doc := verifyCall(t, base, `{"method":"heQuickLogin","randomStr":"r","deviceId":"`+deviceID+`"}`)
	r := results0(t, doc)
	heID, token := r["heAccountId"].(string), r["token"].(string)
	s1 = strings.Repeat("s"+deviceID, 4)[:32]
	doc, seeded := verifyCallSeeded(t, base,
		`{"method":"chooseServer","heAccountId":"`+heID+`","token":"`+token+`","deviceId":"`+deviceID+`","serverId":"1"}`, s1)
	r = results0(t, doc)
	if r["success"] != true {
		t.Fatalf("chooseServer(%s): %v", deviceID, r)
	}
	return r["userId"].(string), r["sk"].(string), seeded, s1
}

// TestTutorialWriteChain 教程写面全链（契约 12/13 + Top 轮询）：
// verify_nickname → users/update(user_name) → users/messages →
// users/update(tutorial_finish) → preset_data 反映终态 → 重复提交幂等。
func TestTutorialWriteChain(t *testing.T) {
	ts, _ := testServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-tut-1")

	// 1) verify_nickname：新名可用
	plain := gameCall(t, ts.URL, "/users/verify_nickname", uid, sk, s0, s1,
		"", "_method=POST&user_name="+url.QueryEscape("旅者"))
	var vn map[string]any
	if err := json.Unmarshal(plain, &vn); err != nil || vn["result"] != true {
		t.Fatalf("verify_nickname: %s err=%v", plain, err)
	}

	// 2) users/update 仅昵称（Story1 步）：status:"success" 字面值
	plain = gameCall(t, ts.URL, "/users/update", uid, sk, s0, s1,
		"", "_method=POST&user_name="+url.QueryEscape("旅者"))
	var up map[string]any
	if err := json.Unmarshal(plain, &up); err != nil || up["status"] != "success" {
		t.Fatalf("users/update 昵称: %s err=%v", plain, err)
	}

	// 3) 断线重试：本人已持有同名 → verify_nickname 仍 true
	plain = gameCall(t, ts.URL, "/users/verify_nickname", uid, sk, s0, s1,
		"", "_method=POST&user_name="+url.QueryEscape("旅者"))
	if err := json.Unmarshal(plain, &vn); err != nil || vn["result"] != true {
		t.Fatalf("verify_nickname 自重: %s err=%v", plain, err)
	}

	// 4) users/messages：Top 轮询空类别字典
	plain = gameCall(t, ts.URL, "/users/messages", uid, sk, s0, s1,
		"last_read_at=0", "_method=GET")
	var msg map[string]any
	if err := json.Unmarshal(plain, &msg); err != nil {
		t.Fatalf("users/messages: %s", plain)
	}
	messages := msg["data"].(map[string]any)["messages"].(map[string]any)
	for _, cat := range []string{"user_help", "user_comment", "guild_help"} {
		if _, ok := messages[cat]; !ok {
			t.Fatalf("messages 缺类别 %s: %v", cat, messages)
		}
	}

	// 5) End 步：tutorial_finish=true + user_name 同事务
	plain = gameCall(t, ts.URL, "/users/update", uid, sk, s0, s1,
		"", "_method=POST&tutorial_finish=true&user_name="+url.QueryEscape("旅者"))
	if err := json.Unmarshal(plain, &up); err != nil || up["status"] != "success" {
		t.Fatalf("users/update 完成: %s err=%v", plain, err)
	}

	// 6) 终态反映到快照：game_status=2 + 昵称持久化
	plain = gameCall(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "", "_method=GET")
	var pd map[string]any
	if err := json.Unmarshal(plain, &pd); err != nil {
		t.Fatal(err)
	}
	user := pd["data"].(map[string]any)["user"].(map[string]any)
	if user["game_status"].(float64) != 2 {
		t.Fatalf("完成后 game_status: %v", user["game_status"])
	}
	if user["user_name"] != "旅者" && user["name"] != "旅者" {
		t.Fatalf("昵称未持久化: %v", user)
	}

	// 7) 重复 End 提交幂等成功
	plain = gameCall(t, ts.URL, "/users/update", uid, sk, s0, s1,
		"", "_method=POST&tutorial_finish=true&user_name="+url.QueryEscape("旅者"))
	if err := json.Unmarshal(plain, &up); err != nil || up["status"] != "success" {
		t.Fatalf("重复完成应幂等成功: %s err=%v", plain, err)
	}
}

// TestTutorialWriteIsolation 双账号昵称互斥：B 抢 A 的昵称 →
// verify_nickname=false、users/update=status:"error"，且 A 状态不被污染。
func TestTutorialWriteIsolation(t *testing.T) {
	ts, _ := testServer(t)
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-iso-A")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-iso-B")

	gameCall(t, ts.URL, "/users/update", uidA, skA, s0A, s1A,
		"", "_method=POST&user_name="+url.QueryEscape("独占名"))

	// B 查 A 的昵称 → false
	plain := gameCall(t, ts.URL, "/users/verify_nickname", uidB, skB, s0B, s1B,
		"", "_method=POST&user_name="+url.QueryEscape("独占名"))
	var vn map[string]any
	if err := json.Unmarshal(plain, &vn); err != nil || vn["result"] != false {
		t.Fatalf("占用名 verify 应 false: %s err=%v", plain, err)
	}

	// B 强写 A 的昵称 → status:"error"（事务回滚，不污染 tutorial_finish）
	plain = gameCall(t, ts.URL, "/users/update", uidB, skB, s0B, s1B,
		"", "_method=POST&tutorial_finish=true&user_name="+url.QueryEscape("独占名"))
	var up map[string]any
	if err := json.Unmarshal(plain, &up); err != nil || up["status"] != "error" {
		t.Fatalf("占用名 update 应 error: %s err=%v", plain, err)
	}
	// B 的 tutorial_finish 不得被那次失败请求置位
	plain = gameCall(t, ts.URL, "/users/preset_data.json", uidB, skB, s0B, s1B, "", "_method=GET")
	var pd map[string]any
	if err := json.Unmarshal(plain, &pd); err != nil {
		t.Fatal(err)
	}
	userB := pd["data"].(map[string]any)["user"].(map[string]any)
	if _, bad := userB["game_status"]; bad {
		t.Fatalf("B 失败请求不得置位 tutorial_finish: %v", userB)
	}
}
