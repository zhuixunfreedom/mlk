package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newEvidenceServer 构造带临时取证日志与 slog 缓冲的测试服务。
// 路由表覆盖三种形态：未注册、已登记未实现（M3 主场景）、已确认 200。
func newEvidenceServer(t *testing.T) (*Server, *httptest.Server, *bytes.Buffer, string) {
	t.Helper()
	reg := NewRegistry()
	reg.HandleWithStatus(http.MethodPost, "/users/gacha", "占位：卡池模型未确认", ModelUnknown, nil)
	reg.HandleWithStatus(http.MethodPost, "/users/done", "已确认路由", ModelConfirmed, func(c *Ctx) error {
		WriteJSON(c.W, http.StatusOK, map[string]any{"ok": true})
		return nil
	})
	logBuf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reqLog := filepath.Join(t.TempDir(), "requests.ndjson")
	srv, err := NewServer(Options{RequestLog: reqLog}, reg, log)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, logBuf, reqLog
}

func readNDJSON(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取取证日志: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("取证日志为空")
	}
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("解析 ndjson 行 %q: %v", line, err)
		}
		records = append(records, m)
	}
	return records
}

func headersOf(t *testing.T, record map[string]any) map[string]string {
	t.Helper()
	raw, ok := record["headers"].(map[string]any)
	if !ok {
		t.Fatalf("记录缺少 headers: %#v", record)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, _ := v.(string)
		out[k] = s
	}
	return out
}

// S-OBS-01 核心：未知 GET/POST、加密与明文请求的 501 取证字段齐全且脱敏。
func TestUnknown501EvidenceSanitized(t *testing.T) {
	_, ts, logBuf, reqLog := newEvidenceServer(t)

	appID := "a78db97769fd1e928ef860856a99ebdb2280cd62d4c9a6bc" // App_id_1 全值（仅测试样本）
	appT := "R1H3fLQlwZxfVar2IO4deGUk0UjNW7R9rb22UcWJUn8="      // App_t 签名 token 样本
	cookieVal := "sid=topsecret-cookie"
	authVal := "Bearer topsecret-token"
	body := []byte("password=hunter2&user=mel")
	bodySum := sha256.Sum256(body)
	bodyHex := hex.EncodeToString(bodySum[:])
	appIDFingerprint := "sha256:" + sha256Hex(appID)[:12] + ":len=" + strconv.Itoa(len(appID))

	// 1) 加密信封的未知 POST：携带全套敏感 header。
	post, err := http.NewRequest(http.MethodPost, ts.URL+"/users/gacha/exec?e=AbCdEf1234567890%3D%3D&uid=666666",
		strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Encrypted", "true")
	post.Header.Set("App_id_1", appID)
	post.Header.Set("App_t", appT)
	post.Header.Set("Cookie", cookieVal)
	post.Header.Set("Authorization", authVal)
	resp, err := ts.Client().Do(post)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("未知 POST 状态 = %d，期望 501", resp.StatusCode)
	}

	// 2) 明文未知 GET：无加密标记。
	resp, err = ts.Client().Get(ts.URL + "/shop/list")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("未知 GET 状态 = %d，期望 501", resp.StatusCode)
	}

	// 3) 已登记但未实现（M3 冒烟主场景）：route_known 必须为 true。
	postGacha, err := http.NewRequest(http.MethodPost, ts.URL+"/users/gacha", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	postGacha.Header.Set("Encrypted", "1")
	postGacha.Header.Set("App_id_1", appID)
	resp, err = ts.Client().Do(postGacha)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("已登记未实现 POST 状态 = %d，期望 501", resp.StatusCode)
	}

	// 4) 200 路径：取证行为不受脱敏改造影响（全量 header + body_b64）。
	postDone, err := http.NewRequest(http.MethodPost, ts.URL+"/users/done", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	postDone.Header.Set("App_id_1", appID)
	resp, err = ts.Client().Do(postDone)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("已确认路由状态 = %d，期望 200", resp.StatusCode)
	}

	records := readNDJSON(t, reqLog)
	if len(records) != 4 {
		t.Fatalf("取证记录数 = %d，期望 4", len(records))
	}

	var encryptedPOST, plainGET, known501, ok200 map[string]any
	for _, record := range records {
		switch {
		case record["path"] == "/users/gacha/exec":
			encryptedPOST = record
		case record["path"] == "/shop/list":
			plainGET = record
		case record["path"] == "/users/gacha":
			known501 = record
		case record["path"] == "/users/done":
			ok200 = record
		}
	}
	if encryptedPOST == nil || plainGET == nil || known501 == nil || ok200 == nil {
		t.Fatalf("取证记录不齐全: %v", records)
	}

	for name, record := range map[string]map[string]any{
		"加密未知POST": encryptedPOST,
		"明文未知GET":  plainGET,
		"已登记未实现":   known501,
	} {
		if record["event"] != unknownEvent {
			t.Errorf("%s: event = %v，期望 %q", name, record["event"], unknownEvent)
		}
		if record["status"] != float64(http.StatusNotImplemented) {
			t.Errorf("%s: status = %v，期望 501", name, record["status"])
		}
		when, _ := record["time"].(string)
		if when == "" {
			t.Errorf("%s: 缺少触发时间", name)
		} else if _, err := time.Parse(time.RFC3339, when); err != nil {
			t.Errorf("%s: 触发时间 %q 不是 RFC3339: %v", name, when, err)
		}
		if addr, _ := record["client_addr"].(string); addr == "" {
			t.Errorf("%s: 缺少客户端地址", name)
		}
		if _, hasBody := record["body_b64"]; hasBody {
			t.Errorf("%s: 脱敏记录不得包含 body_b64", name)
		}
		if _, hasURI := record["uri"]; hasURI {
			t.Errorf("%s: 脱敏记录不得包含未脱敏的 uri 整值", name)
		}
		if _, hasDecoded := record["decoded"]; hasDecoded {
			t.Errorf("%s: 脱敏记录不得包含 decoded", name)
		}
	}

	// 加密未知 POST：字段值与脱敏断言。
	if encryptedPOST["method"] != http.MethodPost || encryptedPOST["encrypted"] != true {
		t.Errorf("加密未知POST: method/encrypted = %v/%v，期望 POST/true",
			encryptedPOST["method"], encryptedPOST["encrypted"])
	}
	if encryptedPOST["route_known"] != false {
		t.Errorf("加密未知POST: route_known = %v，期望 false", encryptedPOST["route_known"])
	}
	if got := encryptedPOST["body_sha256"]; got != bodyHex {
		t.Errorf("加密未知POST: body_sha256 = %v，期望 %q", got, bodyHex)
	}
	if got := encryptedPOST["body_len"]; got != float64(len(body)) {
		t.Errorf("加密未知POST: body_len = %v，期望 %d", got, len(body))
	}
	headers := headersOf(t, encryptedPOST)
	if got := headers["App_id_1"]; got != appIDFingerprint {
		t.Errorf("App_id_1 脱敏值 = %q，期望 %q", got, appIDFingerprint)
	}
	for _, name := range []string{"App_t", "Cookie", "Authorization"} {
		if v := headers[name]; v == "" || !strings.HasPrefix(v, "sha256:") {
			t.Errorf("%s 未按 sha256 指纹脱敏: %q", name, v)
		}
	}
	if headers["Encrypted"] != "true" || headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Errorf("非敏感 header 应保留原值: %#v", headers)
	}
	// 查询参数：普通键保留（e 是密文体非明文，按既定口径保留），命中名单的键脱敏。
	query, ok := encryptedPOST["query"].(map[string]any)
	if !ok {
		t.Fatalf("加密未知POST: 缺少 query: %#v", encryptedPOST)
	}
	if query["uid"] != "666666" || query["e"] != "AbCdEf1234567890==" {
		t.Errorf("非敏感查询参数应保留: %#v", query)
	}

	// 明文未知 GET：encrypted 必须为 false。
	if plainGET["method"] != http.MethodGet || plainGET["encrypted"] != false {
		t.Errorf("明文未知GET: method/encrypted = %v/%v，期望 GET/false",
			plainGET["method"], plainGET["encrypted"])
	}
	if plainGET["group"] != "shop" {
		t.Errorf("明文未知GET: group = %v，期望 shop", plainGET["group"])
	}

	// 已登记未实现：route_known=true、note 来自路由登记说明。
	if known501["route_known"] != true {
		t.Errorf("已登记未实现: route_known = %v，期望 true", known501["route_known"])
	}
	if note, _ := known501["note"].(string); note != "占位：卡池模型未确认" {
		t.Errorf("已登记未实现: note = %q，期望登记说明", note)
	}

	// 明文敏感值绝不出现：逐行检查 501 脱敏记录与 slog。
	// （200 记录按既有契约保留全量 header/body，不在脱敏扫描范围内。）
	raw, _ := os.ReadFile(reqLog)
	forbidden := []string{appID, appT, cookieVal, authVal, "hunter2", "topsecret"}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.Contains(line, `"event":"`+unknownEvent+`"`) {
			continue
		}
		for _, secret := range forbidden {
			if strings.Contains(line, secret) {
				t.Errorf("501 脱敏记录泄漏明文敏感值 %q: %s", secret, line)
			}
		}
	}
	for _, secret := range forbidden {
		if strings.Contains(logBuf.String(), secret) {
			t.Errorf("slog 泄漏明文敏感值 %q", secret)
		}
	}
	if !strings.Contains(logBuf.String(), "CN 501 未实现请求") {
		t.Errorf("slog 缺少 501 结构化告警，输出:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), bodyHex) {
		t.Errorf("slog 501 告警缺少 body_sha256，输出:\n%s", logBuf.String())
	}

	// 200 路径行为不变：全量 header 与 body_b64 照旧。
	okHeaders := headersOf(t, ok200)
	if okHeaders["App_id_1"] != appID {
		t.Errorf("200 记录应保留 App_id_1 全值（既有行为），得到 %q", okHeaders["App_id_1"])
	}
	if _, has := ok200["body_b64"]; !has {
		t.Errorf("200 记录应保留 body_b64（既有行为）")
	}
	if _, has := ok200["event"]; has {
		t.Errorf("200 记录不应带 unknown_endpoint 事件标记")
	}
}

// sha256Hex 与 http.FprintLen 只是把测试内的期望值计算集中到一处。
func sha256Hex(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// backlog 聚合：按命中次数降序、并列按 path/method 升序，加密计数单独累计。
func TestUnknownEndpointBacklogAggregation(t *testing.T) {
	srv, ts, _, _ := newEvidenceServer(t)

	hit := func(method, path string, encrypted bool) {
		t.Helper()
		var req *http.Request
		var err error
		if method == http.MethodGet {
			req, err = http.NewRequest(method, ts.URL+path, nil)
		} else {
			req, err = http.NewRequest(method, ts.URL+path, strings.NewReader("x=1"))
		}
		if err != nil {
			t.Fatal(err)
		}
		if encrypted {
			req.Header.Set("Encrypted", "true")
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s %s 状态 = %d，期望 501", method, path, resp.StatusCode)
		}
	}

	// /users/gacha ×3（加密 ×2）：应排第一。
	hit(http.MethodPost, "/users/gacha", true)
	hit(http.MethodPost, "/users/gacha", true)
	hit(http.MethodPost, "/users/gacha", false)
	// /quests/execute/9.json ×2：排第二。
	hit(http.MethodPost, "/quests/execute/9.json", false)
	hit(http.MethodPost, "/quests/execute/9.json", false)
	// /aaa/y 与 /zzz/x 各 ×1：同次数按 path 升序。
	hit(http.MethodGet, "/zzz/x", false)
	hit(http.MethodGet, "/aaa/y", false)

	stats := srv.reg.UnknownEndpoints()
	if len(stats) != 4 {
		t.Fatalf("聚合条目数 = %d，期望 4: %#v", len(stats), stats)
	}
	type expectation struct {
		method string
		path   string
		count  int
		enc    int
	}
	want := []expectation{
		{http.MethodPost, "/users/gacha", 3, 2},
		{http.MethodPost, "/quests/execute/9.json", 2, 0},
		{http.MethodGet, "/aaa/y", 1, 0},
		{http.MethodGet, "/zzz/x", 1, 0},
	}
	for i, exp := range want {
		got := stats[i]
		if got.Method != exp.method || got.Path != exp.path ||
			got.Count != exp.count || got.EncryptedCount != exp.enc {
			t.Errorf("聚合第 %d 位 = {%s %s count=%d enc=%d}，期望 {%s %s count=%d enc=%d}",
				i, got.Method, got.Path, got.Count, got.EncryptedCount,
				exp.method, exp.path, exp.count, exp.enc)
		}
		if _, err := time.Parse(time.RFC3339, got.FirstSeen); err != nil {
			t.Errorf("%s %s first_seen %q 不是 RFC3339: %v", got.Method, got.Path, got.FirstSeen, err)
		}
		if _, err := time.Parse(time.RFC3339, got.LastSeen); err != nil {
			t.Errorf("%s %s last_seen %q 不是 RFC3339: %v", got.Method, got.Path, got.LastSeen, err)
		}
	}
	if note := stats[0].LastNote; note != "占位：卡池模型未确认" {
		t.Errorf("/users/gacha last_note = %q，期望登记说明", note)
	}

	// 200 路径绝不进入 backlog。
	resp, err := ts.Client().Post(ts.URL+"/users/done", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	stats = srv.reg.UnknownEndpoints()
	if len(stats) != 4 {
		t.Fatalf("200 命中后聚合条目数 = %d，期望仍为 4", len(stats))
	}
}

// 脱敏名单单元断言：精确名、片段名、大小写、空值与保留值。
func TestRedactHeadersAndQueryPolicy(t *testing.T) {
	headers := http.Header{}
	// net/http 会把键规范化成「首字母大写、其余小写」的形态（如 App_id_1、App_t），
	// 脱敏匹配按小写整名比较，不依赖该形态。
	headers.Set("App_id_1", "device-primary-id")
	headers.Set("App_t", "signature-token")
	headers.Set("X-Api-Token", "variant-token")
	headers.Set("User-Password", "p@ss")
	headers.Set("Encrypted", "true")
	headers.Set("User-Agent", "Dalvik/2.1.0")

	redacted := redactHeaders(headers)
	for _, name := range []string{"App_id_1", "App_t", "X-Api-Token", "User-Password"} {
		v := redacted[name]
		if v == "" || !strings.HasPrefix(v, "sha256:") {
			t.Errorf("敏感头 %s 未脱敏: %q", name, v)
		}
	}
	if redacted["Encrypted"] != "true" || redacted["User-Agent"] != "Dalvik/2.1.0" {
		t.Errorf("非敏感头应保留原值: %#v", redacted)
	}
	if redacted["App_id_1"] != redactHeaderValue("App_id_1", "device-primary-id") {
		t.Errorf("同一值指纹必须稳定，供聚合使用")
	}
	if got := redactHeaderValue("Cookie", ""); got != "" {
		t.Errorf("空值应保持为空，得到 %q", got)
	}

	query := redactQuery(parseQuery(t, "uid=666666&access_token=abc&version=123"))
	if query["uid"] != "666666" || query["version"] != "123" {
		t.Errorf("非敏感查询参数应保留: %#v", query)
	}
	if v := query["access_token"]; !strings.HasPrefix(v, "sha256:") {
		t.Errorf("token 查询参数未脱敏: %q", v)
	}
}

// 常规 200 记录的凭据脱敏（ROSTER350-06 引入管理面后收紧）：
// Authorization / X-Admin-Token / Cookie 等凭据类头与命中名单的查询参数
// 一律指纹化，管理令牌绝不落进默认可共享的 requests.ndjson；
// 协议密文头（app_id_1/app_t）按既有契约保留全值，其余键原样。
func TestRequestLogRedactsCredentials(t *testing.T) {
	_, ts, _, reqLog := newEvidenceServer(t)

	auth := "Bearer topsecret-admin-bearer"
	adminTok := "topsecret-x-admin-token"
	cookie := "sid=topsecret-session"
	appID := "a78db97769fd1e928ef860856a99ebdb2280cd62d4c9a6bc"
	req, err := http.NewRequest(http.MethodPost,
		ts.URL+"/users/done?access_token=querysecret&uid=666666",
		strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("X-Admin-Token", adminTok)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("App_id_1", appID)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("200 路径状态 = %d", resp.StatusCode)
	}

	records := readNDJSON(t, reqLog)
	if len(records) != 1 {
		t.Fatalf("取证记录数 = %d，期望 1", len(records))
	}
	rec := records[0]
	headers := headersOf(t, rec)
	for _, name := range []string{"Authorization", "X-Admin-Token", "Cookie"} {
		v := headers[name]
		if v == "" || !strings.HasPrefix(v, "sha256:") {
			t.Errorf("凭据头 %s 未指纹化: %q", name, v)
		}
	}
	if headers["App_id_1"] != appID {
		t.Errorf("协议密文头 App_id_1 应按契约保留全值，得到 %q", headers["App_id_1"])
	}
	query, ok := rec["query"].(map[string]any)
	if !ok {
		t.Fatalf("记录缺少 query: %#v", rec)
	}
	if v, _ := query["access_token"].(string); !strings.HasPrefix(v, "sha256:") {
		t.Errorf("敏感查询参数 access_token 未指纹化: %q", v)
	}
	if query["uid"] != "666666" {
		t.Errorf("非敏感查询参数应保留原值: %#v", query)
	}
	uri, _ := rec["uri"].(string)
	if strings.Contains(uri, "querysecret") {
		t.Errorf("uri 泄漏敏感查询明文: %q", uri)
	}

	// 全文兜底：任何凭据明文都不得出现在取证日志里。
	raw, err := os.ReadFile(reqLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"topsecret-admin-bearer", "topsecret-x-admin-token",
		"topsecret-session", "querysecret"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("取证日志泄漏明文敏感值 %q: %s", secret, string(raw))
		}
	}
}

func parseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return values
}
