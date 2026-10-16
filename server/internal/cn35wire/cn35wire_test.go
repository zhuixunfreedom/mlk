// cn35wire_test.go 协议层 HTTP 适配测试：verify 信封往返、chooseServer
// 包装解包、game 通道身份头+e=+表单体解码、受控错误分类。
// 测试中的"客户端"方向一律调用 cn35protocol 的编码 API（与真实客户端同源），
// DES oracle 见 cn35protocol/identity_test.go。
package cn35wire_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
)

const (
	testUID = "666666"
	testSK  = "sk-session-01"
	testS0  = "AaBbCcDdEeFfGgHhIiJjKkLlMmNnOoPp" // 32B
	testS1  = "ZzYyXxWwVvUuTtSsRrQqPpOoNnMmLlAa" // 32B
)

// newEnvelope 构造一条客户端 verify 明文信封 [{sub},[{biz}]]。
func newEnvelope(t *testing.T, biz string) []byte {
	t.Helper()
	return []byte(`[{"v":1316,"mv":"1","ct":2,"sk":"gip-eurhieur8387583979385798327958udfhdjf","aid":"11","lang":"zh_CN","pf":"uc","notify":true,"st":0},[` + biz + `]]`)
}

func encodeVerify(t *testing.T, envelope []byte) []byte {
	t.Helper()
	wire, err := cn35protocol.EncodeAccountRequest(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestVerifyRoundTrip_Plain(t *testing.T) {
	wire := encodeVerify(t, newEnvelope(t, `{"method":"metaInfo"}`))
	req, err := cn35wire.DecodeVerifyRequest(wire)
	if err != nil {
		t.Fatal(err)
	}
	if req.Wrapped || req.S1 != "" {
		t.Fatalf("非 chooseServer 不应有信封: %+v", req)
	}
	if len(req.Methods) != 1 || req.Methods[0] != "metaInfo" {
		t.Fatalf("methods = %v", req.Methods)
	}
	if req.SubHeader["v"].(float64) != 1316 || req.SubHeader["pf"] != "uc" {
		t.Fatalf("subHeader = %v", req.SubHeader)
	}
}

func TestVerifyRoundTrip_ChooseServerWrapped(t *testing.T) {
	// 客户端 chooseServer：明文被包成 {"r":"<S1>","m":"<sig>","data":<信封>}。
	inner := newEnvelope(t, `{"method":"chooseServer","serverId":"1"}`)
	wrapped := `{"r":"` + testS1 + `","m":"aabbcc","data":` + string(inner) + `}`
	wire := encodeVerify(t, []byte(wrapped))
	req, err := cn35wire.DecodeVerifyRequest(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Wrapped || req.S1 != testS1 {
		t.Fatalf("wrapped=%v s1=%q", req.Wrapped, req.S1)
	}
	if req.Methods[0] != "chooseServer" {
		t.Fatalf("methods = %v", req.Methods)
	}
}

func TestVerifyResponse_SeedsS0(t *testing.T) {
	wire, err := cn35wire.EncodeVerifyResponse(
		[]cn35wire.VerifyResult{{"serverId": "1"}},
		[]map[string]any{{"firstActive": true}},
		[]string{"chooseServer"}, "", testS0)
	if err != nil {
		t.Fatal(err)
	}
	payload, seeded, err := cn35protocol.DecodeAccountResponse(wire)
	if err != nil {
		t.Fatal(err)
	}
	if seeded != testS0 {
		t.Fatalf("seeded S0 = %q", seeded)
	}
	var doc struct {
		Header  map[string]any   `json:"header"`
		Results []map[string]any `json:"results"`
		Events  []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Header["errCode"].(float64) != 0 {
		t.Fatalf("header = %v", doc.Header)
	}
	if doc.Results[0]["retCode"].(float64) != 200 || doc.Results[0]["method"] != "chooseServer" {
		t.Fatalf("results = %v", doc.Results)
	}
	if doc.Events[0]["firstActive"] != true {
		t.Fatalf("events = %v", doc.Events)
	}
}

func TestVerifyResponse_NoSeedMarker0(t *testing.T) {
	wire, err := cn35wire.EncodeVerifyResponse([]cn35wire.VerifyResult{{"retCode": 500}}, nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	payload, seeded, err := cn35protocol.DecodeAccountResponse(wire)
	if err != nil {
		t.Fatal(err)
	}
	if seeded != "" {
		t.Fatalf("无播种响应 seeded = %q", seeded)
	}
	var doc struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Results[0]["retCode"].(float64) != 500 {
		t.Fatalf("显式 retCode 被覆盖: %v", doc.Results)
	}
}

func newStore(t *testing.T) *cn35wire.MemoryStore {
	t.Helper()
	store := cn35wire.NewMemoryStore()
	if err := store.Save(cn35wire.Session{UserID: testUID, SK: testSK, S0: testS0, S1: testS1}); err != nil {
		t.Fatal(err)
	}
	return store
}

// buildGameRequest 模拟客户端构造一条加密业务请求。
func buildGameRequest(t *testing.T, store *cn35wire.MemoryStore, uid string, query, form string) *http.Request {
	t.Helper()
	sess, err := store.Load(uid)
	if err != nil {
		t.Fatal(err)
	}
	s0, s1 := sess.S0, sess.S1
	target := "http://game.local/users/update"
	if query != "" {
		e, err := cn35protocol.EncryptQuery(query, "1700000000", s0, s1)
		if err != nil {
			t.Fatal(err)
		}
		target += "?e=" + e // base64 '+'/'/'/'=' 原样进 RawQuery
	}
	var body []byte
	if form != "" {
		body, err = cn35protocol.EncryptFormBody(form, "1700000001", s0, s1)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	id1, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, uid)
	id4, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, testSK)
	id2 := cn35protocol.HashIdentity(uid)
	r.Header.Set("APP_ID_1", id1)
	r.Header.Set("APP_ID_2", id2)
	r.Header.Set("APP_ID_4", id4)
	r.Header.Set("Encrypted", "true")
	appT, err := cn35protocol.EncryptAPP_T("1700000002", s0, s1)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("APP_T", appT)
	r.Body = http.NoBody // body 由调用方以 []byte 传入 DecodeGameRequest
	return r
}

func TestGameDecode_EncryptedQueryAndForm(t *testing.T) {
	store := newStore(t)
	form := "_method=POST&nickname=hero&_id=42"
	r := buildGameRequest(t, store, testUID, "page=home&tab=1", form)
	body, _ := cn35protocol.EncryptFormBody(form, "1700000001", testS0, testS1)
	req, err := cn35wire.DecodeGameRequest(r, body, store)
	if err != nil {
		t.Fatal(err)
	}
	if req.UserID != testUID || req.SK != testSK || req.S0 != testS0 || req.S1 != testS1 {
		t.Fatalf("session = %+v", req.Session)
	}
	if req.Query.Get("page") != "home" || req.Query.Get("tab") != "1" {
		t.Fatalf("query = %v", req.Query)
	}
	if req.Method != "POST" || req.Form.Get("nickname") != "hero" || req.Form.Get("_id") != "42" {
		t.Fatalf("form = %v method=%q", req.Form, req.Method)
	}
	if req.AppT != "1700000002" {
		t.Fatalf("APP_T = %q", req.AppT)
	}
	if !req.EncryptedBody {
		t.Fatal("Encrypted:true 未走密文路径")
	}
}

func TestGameDecode_PlainQueryAndForm(t *testing.T) {
	store := newStore(t)
	r := buildGameRequest(t, store, testUID, "", "")
	r.URL.RawQuery = "a=1"
	r.Header.Del("Encrypted")
	req, err := cn35wire.DecodeGameRequest(r, []byte("_method=GET&k=v"), store)
	if err != nil {
		t.Fatal(err)
	}
	if req.Query.Get("a") != "1" || req.Form.Get("k") != "v" || req.Method != "GET" || req.EncryptedBody {
		t.Fatalf("明文路径错误: %+v", req)
	}
}

func TestGameDecode_Errors(t *testing.T) {
	store := newStore(t)

	// 缺 APP_ID_1
	r := httptest.NewRequest(http.MethodPost, "http://g/users/update", nil)
	if _, err := cn35wire.DecodeGameRequest(r, nil, store); !errors.Is(err, cn35wire.ErrMissingHeader) {
		t.Fatalf("缺 APP_ID_1: %v", err)
	}
	// APP_ID_1 非 hex
	r = httptest.NewRequest(http.MethodPost, "http://g/users/update", nil)
	r.Header.Set("APP_ID_1", "nothex!")
	if _, err := cn35wire.DecodeGameRequest(r, nil, store); !errors.Is(err, cn35wire.ErrBadIdentity) {
		t.Fatalf("非法 APP_ID_1: %v", err)
	}
	// 未绑定会话的合法身份头
	idGhost, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, "ghost")
	r = httptest.NewRequest(http.MethodPost, "http://g/users/update", nil)
	r.Header.Set("APP_ID_1", idGhost)
	if _, err := cn35wire.DecodeGameRequest(r, nil, store); !errors.Is(err, cn35wire.ErrNoSession) {
		t.Fatalf("未知会话: %v", err)
	}
	// 密文损坏 → ErrDecode（绝不进业务写）
	r = buildGameRequest(t, store, testUID, "", "")
	r.Header.Del("Encrypted") // 让 form 先不触发；改测 e= 损坏
	r.URL.RawQuery = "e=%%%badbase64%%%"
	if _, err := cn35wire.DecodeGameRequest(r, nil, store); !errors.Is(err, cn35wire.ErrDecode) {
		t.Fatalf("损坏 e=: %v", err)
	}
	// APP_ID_4 密文损坏 → ErrBadIdentity
	r = buildGameRequest(t, store, testUID, "", "")
	r.Header.Set("APP_ID_4", "00")
	if _, err := cn35wire.DecodeGameRequest(r, nil, store); !errors.Is(err, cn35wire.ErrBadIdentity) {
		t.Fatalf("损坏 APP_ID_4: %v", err)
	}
}

func TestGameDecode_WrongSessionKeyFails(t *testing.T) {
	store := newStore(t)
	// 用一对别的 S0/S1 加密 → 服务端用正确会话密钥解 → padding/zlib 必失败。
	e, _ := cn35protocol.EncryptQuery("a=1", "1700000000", strings.Repeat("x", 32), strings.Repeat("y", 32))
	r := buildGameRequest(t, store, testUID, "", "")
	r.Header.Del("Encrypted")
	r.URL.RawQuery = "e=" + e
	if _, err := cn35wire.DecodeGameRequest(r, nil, store); !errors.Is(err, cn35wire.ErrDecode) {
		t.Fatalf("错误密钥密文: %v", err)
	}
}

func TestGameResponse_BothModes(t *testing.T) {
	sess := cn35wire.Session{UserID: testUID, S0: testS0, S1: testS1}
	payload := []byte(`{"data":{"user":{"game_status":2}}}`)

	// 明文模式：无 ENCRYPTED 头，body 为 JSON 原文。
	w := httptest.NewRecorder()
	if err := cn35wire.WriteGameResponse(w, payload, sess, false); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("ENCRYPTED") != "" {
		t.Fatal("明文响应不应带 ENCRYPTED 头")
	}
	if w.Body.String() != string(payload) {
		t.Fatal("明文 body 不一致")
	}

	// 加密模式：ENCRYPTED:true + type301，客户端 DecodeGameResponse 还原。
	w = httptest.NewRecorder()
	if err := cn35wire.WriteGameResponse(w, payload, sess, true); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("ENCRYPTED") != "true" {
		t.Fatal("加密响应必须带 ENCRYPTED:true")
	}
	plain, err := cn35protocol.DecodeGameResponse(w.Body.Bytes(), testS0, testS1)
	if err != nil || string(plain) != string(payload) {
		t.Fatalf("type301 还原失败: %v %q", err, plain)
	}
}

func TestVerifyDecode_Errors(t *testing.T) {
	// 截断帧
	if _, err := cn35wire.DecodeVerifyRequest([]byte{0, 1, 2}); !errors.Is(err, cn35wire.ErrDecode) {
		t.Fatalf("截断帧: %v", err)
	}
	// 合法帧但明文不是合法信封 JSON：AES 解密成功需要正确密文 —— 构造
	// 一个明文为垃圾的信封（客户端编码 API 允许任意明文）。
	wire := encodeVerify(t, []byte("not-json"))
	if _, err := cn35wire.DecodeVerifyRequest(wire); !errors.Is(err, cn35wire.ErrEnvelopeJSON) {
		t.Fatalf("非 JSON 明文: %v", err)
	}
	// 顶层数组元素数非法
	wire = encodeVerify(t, []byte(`[{"v":1316}]`))
	if _, err := cn35wire.DecodeVerifyRequest(wire); !errors.Is(err, cn35wire.ErrEnvelopeJSON) {
		t.Fatalf("单元素顶层: %v", err)
	}
	// 损坏的 chooseServer 信封（r 长度非法）
	wire = encodeVerify(t, []byte(`{"r":"short","m":"x","data":[{},[]]}`))
	if _, err := cn35wire.DecodeVerifyRequest(wire); !errors.Is(err, cn35wire.ErrDecode) {
		t.Fatalf("非法信封: %v", err)
	}
}

func TestNewS0(t *testing.T) {
	s0, err := cn35wire.NewS0()
	if err != nil {
		t.Fatal(err)
	}
	if len(s0) != 32 {
		t.Fatalf("S0 长度 %d", len(s0))
	}
	s0b, _ := cn35wire.NewS0()
	if s0 == s0b {
		t.Fatal("S0 重复")
	}
	// 字符集检查
	for _, c := range s0 {
		if !strings.ContainsRune(cn35protocol.S1Charset, c) {
			t.Fatalf("S0 含非法字符 %q", c)
		}
	}
}
