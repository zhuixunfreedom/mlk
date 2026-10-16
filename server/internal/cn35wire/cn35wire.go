// Package cn35wire 是国服 3.5.0 协议层的 HTTP 适配：把 cn35protocol 的
// 帧/AES/DES 原语接到 *http.Request / http.ResponseWriter 上，供 cn35 账号
// 与业务路由复用。本包不持有业务逻辑与会话持久化 —— 会话存取经
// SessionStore 接口注入（持久实现见后续账号任务）。
//
// 版本路由：cn35 的所有编解码常量集中在 internal/cn35protocol，本包是唯一
// HTTP 入口；路由注册层（cn35game.Register）装配到 mercd35 进程。
//
// 客户端证据：docs/协议对接/cn350/contracts/00-transport.md，
// JSONRequester.cs（getHeader/Execute/DeassembleResponse）、
// AssembleModule.cs（ConvertRequest 信封）、CryptManager.cs（DES-ECB 头）。
package cn35wire

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
)

// 受控错误：调用方按类映射为 4xx/静默拒绝，绝不可落入业务写。
var (
	// ErrMissingHeader 必需身份头缺失（APP_ID_1/APP_ID_4/Encrypted 等）。
	ErrMissingHeader = errors.New("cn35wire: 缺少必需请求头")
	// ErrBadIdentity 身份头 DES 解密失败或 hex 非法。
	ErrBadIdentity = errors.New("cn35wire: 身份头解密失败")
	// ErrNoSession userId 无已绑定会话（未 chooseServer / 状态丢失）。
	ErrNoSession = errors.New("cn35wire: 会话未绑定")
	// ErrDecode 帧/密文/信封解码失败（透传 cn35protocol 错误）。
	ErrDecode = errors.New("cn35wire: 请求解码失败")
	// ErrEnvelopeJSON type3 明文不是合法 [subHeader,[biz...]] 数组。
	ErrEnvelopeJSON = errors.New("cn35wire: verify 信封 JSON 非法")
)

// Session 是一条已绑定账号会话：chooseServer 成功后由业务层写入。
// S0 为服务端播种值（type300 '1' 标记下发），S1 为客户端信封 "r" 值。
type Session struct {
	UserID string
	SK     string
	S0     string
	S1     string
}

// SessionStore 会话存取接口。07/09 用 SQLite/账号存储实现；
// 测试与本地联调可用 MemoryStore。
type SessionStore interface {
	// Load 按 userId 取会话；不存在返回 ErrNoSession（或 nil+false，见实现约定）。
	Load(userID string) (Session, error)
	// Save 绑定/覆盖 userId 的会话（chooseServer 成功路径调用）。
	Save(s Session) error
}

// MemoryStore 进程内 SessionStore，供单测与开发联调；不持久化。
type MemoryStore struct {
	sessions map[string]Session
}

// NewMemoryStore 构造空内存会话表。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]Session)}
}

// Load 实现 SessionStore。
func (m *MemoryStore) Load(userID string) (Session, error) {
	s, ok := m.sessions[userID]
	if !ok {
		return Session{}, ErrNoSession
	}
	return s, nil
}

// Save 实现 SessionStore。
func (m *MemoryStore) Save(s Session) error {
	m.sessions[s.UserID] = s
	return nil
}

// NewS0 生成一个 32 字符会话密钥串（S0）：客户端 native 只要求 32 字节
// 可打印字符（type300 拷贝 [1:0x21]），字符集沿用 S1 的可见字母表。
func NewS0() (string, error) {
	var b [cn35protocol.SessionKeyLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("cn35wire: rand: %w", err)
	}
	out := make([]byte, cn35protocol.SessionKeyLen)
	for i, v := range b {
		out[i] = cn35protocol.S1Charset[int(v)%len(cn35protocol.S1Charset)]
	}
	return string(out), nil
}

// ---- verify 通道（POST /account?uid=666666，type3 请求 / type300 响应）----

// VerifyRequest 是解包后的账号通道请求。
type VerifyRequest struct {
	// SubHeader 信封头字段（v/mv/ct/sk/aid/lang/pf/notify/st/uk）。
	SubHeader map[string]any
	// Business 业务文档数组（每元素含 "method" 字段），与 results[] 对应。
	Business []json.RawMessage
	// Methods[i] == Business[i]["method"]。
	Methods []string
	// S1 仅 chooseServer 请求非空（信封 "r" 字段，32 字符客户端盐）。
	S1 string
	// Wrapped 明文是否带 {"r","m","data"} 信封。
	Wrapped bool
}

// DecodeVerifyRequest 解一条 type3 请求体：帧校验 → xor195 → zlib →
// AES-static → （可选 chooseServer 信封解包）→ JSON 信封拆分。
// 所有失败路径返回分类错误，调用方直接拒绝，不进入任何账号写。
func DecodeVerifyRequest(body []byte) (*VerifyRequest, error) {
	plain, err := cn35protocol.DecodeAccountRequest(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	env, s1, wrapped, err := cn35protocol.UnwrapAccountRequest(plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	var top []json.RawMessage
	if err := json.Unmarshal(env, &top); err != nil {
		return nil, fmt.Errorf("%w: 顶层数组: %v", ErrEnvelopeJSON, err)
	}
	if len(top) != 2 {
		return nil, fmt.Errorf("%w: 顶层数组元素数 %d != 2", ErrEnvelopeJSON, len(top))
	}
	req := &VerifyRequest{S1: s1, Wrapped: wrapped}
	if err := json.Unmarshal(top[0], &req.SubHeader); err != nil {
		return nil, fmt.Errorf("%w: subHeader: %v", ErrEnvelopeJSON, err)
	}
	if err := json.Unmarshal(top[1], &req.Business); err != nil {
		return nil, fmt.Errorf("%w: business: %v", ErrEnvelopeJSON, err)
	}
	req.Methods = make([]string, len(req.Business))
	for i, doc := range req.Business {
		var probe struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(doc, &probe); err != nil {
			return nil, fmt.Errorf("%w: business[%d]: %v", ErrEnvelopeJSON, i, err)
		}
		req.Methods[i] = probe.Method
	}
	return req, nil
}

// VerifyResult 是一条 results[] 元素：业务字段与 method/retCode 同层扁平。
type VerifyResult map[string]any

// MarshalJSON 序列化客户端下发 JSON：关闭 HTML 转义。客户端 JSON 解析不把
// \u0026 还原为 &（story350-04 实机 logcat：`Page : QuestSelect\u0026method
// Conf is Empty` → PageMessage 按字面 & 切分失败、页名污染），凡含
// name=X&method=Y 页面消息（area_select_message/quest_select_message/
// next_page_message 等）或 < > 的字段都必须走本函数。
//
// Encode 前先经 sanitizeClientStrings 配平字符串值内的 [ ] { }——
// 客户端解析器的深度计数不受字符串态门控（见 sanitize.go），不平衡
// 括号会击穿整篇文档解析（/gachas Now-Loading 实机事故）。
func MarshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sanitizeClientStrings(v)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// EncodeVerifyResponse 组装并加密一条 type300 响应：
//
//	{"header":{"errCode":0,["uk":..]},
//	 "results":[{...},"retCode":200,"method":<m>]（按请求 method 顺序补齐）,
//	 "events":[...]}
//
// s0Seed 非空时以 '1' 标记播种（最晚 chooseServer 响应必须播种；幂等覆盖合法）。
// methods 为请求侧 method 序列：results[i].method 缺省时自动补 methods[i]，
// 并统一补 retCode=200（调用方可先置非 200 覆盖）。
func EncodeVerifyResponse(results []VerifyResult, events []map[string]any, methods []string, uk string, s0Seed string) ([]byte, error) {
	for i, res := range results {
		if _, ok := res["retCode"]; !ok {
			res["retCode"] = 200
		}
		if _, ok := res["method"]; !ok && i < len(methods) {
			res["method"] = methods[i]
		}
	}
	header := map[string]any{"errCode": 0}
	if uk != "" {
		header["uk"] = uk
	}
	if events == nil {
		events = []map[string]any{}
	}
	payload, err := MarshalJSON(map[string]any{
		"header": header, "results": results, "events": events,
	})
	if err != nil {
		return nil, fmt.Errorf("cn35wire: verify 响应 JSON: %w", err)
	}
	return cn35protocol.EncodeAccountResponse(payload, s0Seed)
}

// WriteVerifyResponse 把加密帧写入响应（Content-Type: application/octet-stream）。
func WriteVerifyResponse(w http.ResponseWriter, results []VerifyResult, events []map[string]any, methods []string, uk, s0Seed string) error {
	wire, err := EncodeVerifyResponse(results, events, methods, uk, s0Seed)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(wire)
	return err
}

// ---- game 通道（{host}{path}?e=..., Encrypted:true, APP_ID_*）----

// GameRequest 是解码后的业务请求视图。
type GameRequest struct {
	Session                  // UserID/SK/S0/S1（S0/S1 来自 SessionStore）
	Query         url.Values // ?e= 解密后 ParseQuery；无 e= 时为明文 query
	Form          url.Values // Encrypted:true 时 type4 解密；否则明文 urlencoded
	Method        string     // Form["_method"]（GET|POST），客户端恒带
	AppT          string     // APP_T 解密后的 serverTime（未带/失败时为空，不强制）
	EncryptedBody bool       // body 是否走了 type4 解密
}

// DecodeGameRequest 解一条业务请求：
//  1. APP_ID_1 → DES-ECB → userId → SessionStore.Load → (S0,S1)
//  2. APP_ID_4 → DES-ECB → sk（填入 Session.SK，鉴权比较由业务层做）
//  3. RawQuery 取 e= 原始段（base64 '+' 不先行 URL 解码）→ DecryptQuery
//  4. Encrypted:true → DecryptFormBody，否则明文 ParseQuery
//  5. APP_T 可选消费：解密取 serverTime，失败不致命（不鉴权用）
//
// 任何一步失败即返回分类错误 —— 调用方 4xx，不得进入业务写。
func DecodeGameRequest(r *http.Request, body []byte, store SessionStore) (*GameRequest, error) {
	appID1 := r.Header.Get("APP_ID_1")
	if appID1 == "" {
		return nil, fmt.Errorf("%w: APP_ID_1", ErrMissingHeader)
	}
	userID, err := cn35protocol.DecryptIdentity(cn35protocol.IdentityDesKey, appID1)
	if err != nil {
		return nil, fmt.Errorf("%w: APP_ID_1: %w", ErrBadIdentity, err)
	}
	sess, err := store.Load(userID)
	if err != nil {
		return nil, err // ErrNoSession 已分类
	}
	sess.UserID = userID
	req := &GameRequest{Session: sess}

	if appID4 := r.Header.Get("APP_ID_4"); appID4 != "" {
		sk, err := cn35protocol.DecryptIdentity(cn35protocol.IdentityDesKey, appID4)
		if err != nil {
			return nil, fmt.Errorf("%w: APP_ID_4: %w", ErrBadIdentity, err)
		}
		req.SK = sk
	}

	query, err := decodeQueryParam(r.URL.RawQuery, sess.S0, sess.S1)
	if err != nil {
		return nil, err
	}
	req.Query = query

	var form url.Values
	if strings.EqualFold(r.Header.Get("Encrypted"), "true") {
		plain, err := cn35protocol.DecryptFormBody(body, sess.S0, sess.S1)
		if err != nil {
			return nil, fmt.Errorf("%w: 表单体: %w", ErrDecode, err)
		}
		form, err = url.ParseQuery(string(plain))
		req.EncryptedBody = true
	} else {
		form, err = url.ParseQuery(string(body))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: 表单明文解析: %v", ErrEnvelopeJSON, err)
	}
	req.Form = form
	req.Method = form.Get("_method")

	if appT := r.Header.Get("APP_T"); appT != "" {
		if t, err := cn35protocol.DecryptAPP_T(appT, sess.S0, sess.S1); err == nil {
			req.AppT = t
		}
	}
	return req, nil
}

// decodeQueryParam 从 RawQuery 还原参数：存在 e= 段时按 type2 解密，
// 否则按明文 query 解析。e 值取原始段（客户端不转义 base64 的 '+'/'/'/'='）。
func decodeQueryParam(rawQuery, s0, s1 string) (url.Values, error) {
	for _, seg := range strings.Split(rawQuery, "&") {
		if v, ok := strings.CutPrefix(seg, "e="); ok {
			// 兼容已被上游归一化为空格的 '+'。
			plain, err := cn35protocol.DecryptQuery(strings.ReplaceAll(v, " ", "+"), s0, s1)
			if err != nil {
				return nil, fmt.Errorf("%w: query e=: %w", ErrDecode, err)
			}
			vals, err := url.ParseQuery(string(plain))
			if err != nil {
				return nil, fmt.Errorf("%w: query e= 明文: %v", ErrEnvelopeJSON, err)
			}
			return vals, nil
		}
	}
	return url.ParseQuery(rawQuery)
}

// DecodeStaticQuery 供静态文件通道使用：GET {name}.json.encode?e=...。
// 与游戏通道共用 e= 解码；静态请求可能不带身份头，调用方在能解析会话时
// 传入 (s0,s1)，解析不了就传空串让 e= 段按明文兜底失败 —— 由路由层决定
// 是否拒绝（静态 e= 内容仅是 version=ts，服务端可不消费）。
func DecodeStaticQuery(rawQuery, s0, s1 string) (url.Values, error) {
	return decodeQueryParam(rawQuery, s0, s1)
}

// WriteGameResponse 写业务响应。encrypted=true 时走 type301
// （aes_enc(deflate(json)) + ENCRYPTED:true 头）；false 时明文 JSON ——
// 客户端仅按 ENCRYPTED 头决定是否解码，两者都合法（00-transport §1）。
func WriteGameResponse(w http.ResponseWriter, payload []byte, sess Session, encrypted bool) error {
	if !encrypted {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(payload)
		return err
	}
	wire, err := cn35protocol.EncodeGameResponse(payload, sess.S0, sess.S1)
	if err != nil {
		return err
	}
	w.Header().Set("ENCRYPTED", "true")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(wire)
	return err
}
