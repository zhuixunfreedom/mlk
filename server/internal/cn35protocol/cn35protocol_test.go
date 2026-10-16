package cn35protocol

import (
	"bytes"
	"strings"
	"testing"
)

const (
	testS0 = "0123456789abcdef0123456789abcdef" // 32B
	testS1 = "qwertyuiopasdfghjklzxcvbnm123456" // 32B
)

func TestGameCipherLengths(t *testing.T) {
	key, iv := GameCipher(testS0, testS1)
	if len(key) != 16 || len(iv) != 16 {
		t.Fatalf("key/iv len = %d/%d", len(key), len(iv))
	}
	// S1 为空时（chooseServer 之前）也必须工作。
	key2, iv2 := GameCipher(testS0, "")
	if len(key2) != 16 || len(iv2) != 16 {
		t.Fatalf("empty-S1 key/iv len = %d/%d", len(key2), len(iv2))
	}
	if bytes.Equal(key, key2) || bytes.Equal(iv, iv2) {
		t.Fatal("S1 必须参与密钥推导")
	}
}

func TestAccountRequestRoundTrip(t *testing.T) {
	env := []byte(`[{"v":"3.5.0","mv":"1","ct":2,"sk":"","aid":"x","lang":"zh","pf":"android","notify":true,"st":0}],[{"method":"metaInfo"}]]`)
	wire, err := EncodeAccountRequest(env)
	if err != nil {
		t.Fatal(err)
	}
	if wire[0] != 0x00 || wire[1] != 0x64 {
		t.Fatalf("version bytes = %02x %02x", wire[0], wire[1])
	}
	back, err := DecodeAccountRequest(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, env) {
		t.Fatalf("roundtrip mismatch:\n%s\n%s", back, env)
	}
}

func TestAccountRequestBadChecksum(t *testing.T) {
	env := []byte(`x`)
	wire, err := EncodeAccountRequest(env)
	if err != nil {
		t.Fatal(err)
	}
	wire[20] ^= 0xff
	if _, err := DecodeAccountRequest(wire); err == nil {
		t.Fatal("tampered frame must fail")
	}
}

func TestAccountResponseSeed(t *testing.T) {
	payload := []byte(`{"header":{"errCode":0},"results":[{"retCode":200}]}`)
	wire, err := EncodeAccountResponse(payload, testS0)
	if err != nil {
		t.Fatal(err)
	}
	back, s0, err := DecodeAccountResponse(wire)
	if err != nil {
		t.Fatal(err)
	}
	if s0 != testS0 {
		t.Fatalf("seeded S0 = %q", s0)
	}
	if !bytes.Equal(back, payload) {
		t.Fatalf("payload mismatch: %s", back)
	}
}

func TestAccountResponseNoSeed(t *testing.T) {
	payload := []byte(`{"header":{"errCode":0},"results":[]}`)
	wire, err := EncodeAccountResponse(payload, "")
	if err != nil {
		t.Fatal(err)
	}
	back, s0, err := DecodeAccountResponse(wire)
	if err != nil {
		t.Fatal(err)
	}
	if s0 != "" {
		t.Fatalf("unexpected seed %q", s0)
	}
	if !bytes.Equal(back, payload) {
		t.Fatalf("payload mismatch: %s", back)
	}
}

func TestAccountResponseBadSeedLen(t *testing.T) {
	if _, err := EncodeAccountResponse([]byte(`{}`), "short"); err == nil {
		t.Fatal("non-32B seed must fail")
	}
}

func TestGameChannelRoundTrip(t *testing.T) {
	// type2 ?e=
	q, err := EncryptQuery("method=preset_data&tutorial_session=true", "1700000000", testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecryptQuery(q, testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "method=preset_data&tutorial_session=true" {
		t.Fatalf("query = %q", plain)
	}
	// type4 body
	body, err := EncryptFormBody("_method=update&x=1", "1700000001", testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = DecryptFormBody(body, testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "_method=update&x=1" {
		t.Fatalf("form = %q", plain)
	}
	// APP_T
	tok, err := EncryptAPP_T("1700000002", testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = DecryptQuery(tok, testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "1700000002"+AppTSalt {
		t.Fatalf("APP_T = %q", plain)
	}
	// type301 response
	resp := []byte(`{"header":{"errCode":0},"results":[{"retCode":200,"data":{"ok":1}}]}`)
	wire, err := EncodeGameResponse(resp, testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeGameResponse(wire, testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, resp) {
		t.Fatalf("game resp mismatch: %s", back)
	}
}

// TestChooseServerEnvelopeShape 锁定 chooseServer 信封形态：
// {"r":"<S1>","m":"<sig>","data":<原请求体>} —— 由原生字符串模板拼接，
// data 段是未经 JSON 转义的原始请求（含顶层 `],[`），整体并非合法 JSON。
func TestChooseServerEnvelopeShape(t *testing.T) {
	inner := []byte(`[{"v":"3.5.0"},[{"method":"chooseServer","serverId":1}]]`)
	env := `{"r":"` + testS1 + `","m":"deadbeef","data":` + string(inner) + `}`
	biz, s1, w, err := UnwrapAccountRequest([]byte(env))
	if err != nil || !w || s1 != testS1 {
		t.Fatalf("unwrap: w=%v s1=%q err=%v", w, s1, err)
	}
	if string(biz) != string(inner) {
		t.Fatalf("data = %s", biz)
	}
	if !strings.Contains(string(biz), "chooseServer") {
		t.Fatal("data 必须携带原始请求")
	}
}

func TestUnwrapAccountRequest(t *testing.T) {
	// 非信封：原样返回
	raw := []byte(`[{"v":"3.5.0"},[{"method":"metaInfo"}]]`)
	b, s1, w, err := UnwrapAccountRequest(raw)
	if err != nil || w || s1 != "" || !bytes.Equal(b, raw) {
		t.Fatalf("raw: w=%v s1=%q b=%s err=%v", w, s1, b, err)
	}
	// 信封：提取 r 与 data（data 含 `],[` 顶层边界）
	inner := []byte(`[{"v":"3.5.0"},[{"method":"chooseServer","serverId":1}]]`)
	env := []byte(`{"r":"` + testS1 + `","m":"deadbeef","data":` + string(inner) + `}`)
	b, s1, w, err = UnwrapAccountRequest(env)
	if err != nil || !w || s1 != testS1 {
		t.Fatalf("env: w=%v s1=%q err=%v", w, s1, err)
	}
	if !bytes.Equal(b, inner) {
		t.Fatalf("env data = %s", b)
	}
	// 信封 r 长度非法必须报错
	bad := []byte(`{"r":"short","m":"x","data":[]}`)
	if _, _, _, err := UnwrapAccountRequest(bad); err == nil {
		t.Fatal("bad r length must fail")
	}
}

// TestAESBlockAlignedPadding 回归：明文在 16 字节边界上时必须仍补一整块
// PKCS7（客户端 native 与独立 Python 预言机均为恒填充语义）。早期实现仅在
// len%16!=0 时填充，导致恰好对齐的表单/响应无法被解密端 unpad。
func TestAESBlockAlignedPadding(t *testing.T) {
	// 构造 stp 后缀后总长恰为 16 的倍数：48B。
	form := strings.Repeat("a", 33) // 33+15("&stp="+10位) = 48
	ct, err := EncryptFormBody(form, "1700000001", testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecryptFormBody(ct, testS0, testS1)
	if err != nil {
		t.Fatalf("block-aligned form decrypt: %v", err)
	}
	if string(plain) != form {
		t.Fatalf("plain = %q", plain)
	}
	// 响应方向同样：payload 长度恰好对齐。
	payload := []byte(strings.Repeat("b", 32))
	wire, err := EncodeGameResponse(payload, testS0, testS1)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeGameResponse(wire, testS0, testS1)
	if err != nil || !bytes.Equal(back, payload) {
		t.Fatalf("game resp roundtrip: %v", err)
	}
}
