// unknown_evidence.go 落实 S-OBS-01：501（未实现路由）的脱敏取证与可排序 backlog。
//
// M3 首层冒烟会大量撞上未实现接口。每次 501 必须留下「可聚合、可分享、
// 不泄密」的证据：method/path、加密标记、脱敏 header、请求体 SHA-256、
// 触发时间与客户端地址；明文 body 与敏感 header 全值绝不落盘。
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// unknownEvent 写入 ndjson 记录的 event 字段，用于区分 501 脱敏记录与普通 capture 记录。
const unknownEvent = "unknown_endpoint"

// unknownRecord 是 501 的脱敏取证记录，追加进与 captureRecord 同一条
// requests.ndjson 流，字段命名保持同一风格（小写下划线、time 为 RFC3339）。
//
// 与普通记录的刻意差异：
//   - 敏感 header / 查询参数（密码、token、cookie、App_id_1 等）只保留
//     sha256 指纹前缀 + 原长度，可聚合、不可还原；
//   - 请求体只落 body_sha256，不落 body_b64 明文；
//   - 不落 uri 整值（查询串无法整值脱敏，改由 path + 脱敏后的 query 表达）；
//   - 不落 decoded（未实现路由的解码字段内容不可预知，一律不外带）。
type unknownRecord struct {
	Time       string            `json:"time"`
	Event      string            `json:"event"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	PathRaw    string            `json:"path_raw,omitempty"`
	APIPrefix  string            `json:"api_prefix,omitempty"`
	Group      string            `json:"group"`
	Query      map[string]string `json:"query,omitempty"`
	Status     int               `json:"status"`
	DurMS      int64             `json:"dur_ms"`
	Encrypted  bool              `json:"encrypted"`
	Headers    map[string]string `json:"headers"`
	BodySHA256 string            `json:"body_sha256,omitempty"`
	BodyLen    int               `json:"body_len"`
	ClientAddr string            `json:"client_addr"`
	Known      bool              `json:"route_known"`
	Note       string            `json:"note,omitempty"`
}

// buildUnknownRecord 把一次 501 capture 转成脱敏记录。at 由调用方传入，
// 保证 slog、ndjson 与聚合视图共用同一触发时间。
func buildUnknownRecord(c capture, note string, at time.Time) unknownRecord {
	u := unknownRecord{
		Time:       at.Format(time.RFC3339Nano),
		Event:      unknownEvent,
		Method:     c.req.Method,
		Path:       c.route.Path,
		PathRaw:    c.rawPath,
		APIPrefix:  c.apiPrefix,
		Group:      c.route.Group,
		Status:     c.code,
		DurMS:      c.duration.Milliseconds(),
		Encrypted:  requestEncrypted(c.req),
		Headers:    redactHeaders(c.req.Header),
		BodyLen:    len(c.body),
		ClientAddr: c.req.RemoteAddr,
		Known:      c.known,
		Note:       note,
	}
	if u.PathRaw == u.Path {
		u.PathRaw = ""
	}
	if len(c.body) > 0 {
		sum := sha256.Sum256(c.body)
		u.BodySHA256 = hex.EncodeToString(sum[:])
	}
	if query := c.req.URL.Query(); len(query) > 0 {
		u.Query = redactQuery(query)
	}
	return u
}

// requestEncrypted 判断请求是否携带客户端加密信封提示（Encrypted 头为
// true/True/1）。Ctx.IsEncrypted 与 501 取证共用这一处判定。
func requestEncrypted(r *http.Request) bool {
	if r == nil {
		return false
	}
	v := r.Header.Get("Encrypted")
	return v == "true" || v == "True" || v == "1"
}

// sensitiveNameExact 精确命中（小写比较）即整值脱敏的名称。
// App_id_1 / App_t 是客户端的设备主标识与请求签名 token，
// S-OBS-01 点名不落全值。
var sensitiveNameExact = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"app_id_1":            true,
	"app_t":               true,
	"x-auth-token":        true,
	"x-api-key":           true,
	"password":            true,
}

// sensitiveNameFragment 片段命中（小写 contains）即整值脱敏，兜住
// X-Api-Token / User-Password 之类的变体命名。
var sensitiveNameFragment = []string{
	"password", "passwd", "secret", "token", "cookie", "authorization",
}

// isSensitiveName 报告 header / 查询参数名是否命中脱敏名单（大小写不敏感）。
func isSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	if sensitiveNameExact[lower] {
		return true
	}
	for _, fragment := range sensitiveNameFragment {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

// redactHeaderValue 保留非敏感值原样；敏感值替换为「sha256 指纹前缀 + 原长度」。
// 指纹前缀对同一客户端稳定，可用于聚合去重，但无法还原全值。
func redactHeaderValue(name, value string) string {
	if value == "" || !isSensitiveName(name) {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:12] + ":len=" + strconv.Itoa(len(value))
}

// redactHeaders 复制请求头（每头取首值，与 flattenHeaders 口径一致）并脱敏敏感项。
func redactHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		result[key] = redactHeaderValue(key, values[0])
	}
	return result
}

// redactQuery 复制查询参数（每键取首值，与 captureRecord 口径一致）并对命中
// 脱敏名单的键值整值脱敏。
func redactQuery(query url.Values) map[string]string {
	result := make(map[string]string, len(query))
	for key, values := range query {
		if len(values) == 0 {
			continue
		}
		result[key] = redactHeaderValue(key, values[0])
	}
	return result
}

// protocolEvidenceHeaders 是常规 200 记录中保留全值的协议密文头：
// app_id_1（设备主标识）与 app_t（请求签名）本身即密文，协议取证需要
// 原值做跨请求关联（既有契约见 unknown_evidence_test.go 的 200 记录断言）。
// 其余命中脱敏名单的凭据类头（authorization、x-admin-token、cookie 等）
// 没有协议取证价值，一律指纹化——否则 /mercd/cn35/admin/* 的管理令牌会
// 明文落盘默认可共享的 requests.ndjson。
var protocolEvidenceHeaders = map[string]bool{
	"app_id_1": true,
	"app_t":    true,
}

// evidenceHeaders 是常规请求记录的 header 脱敏口径：凭据类头指纹化，
// 仅协议密文头保留全值。
func evidenceHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		v := values[0]
		if isSensitiveName(key) && !protocolEvidenceHeaders[strings.ToLower(key)] {
			v = redactHeaderValue(key, v)
		}
		result[key] = v
	}
	return result
}

// evidenceURI 与 evidenceHeaders 同口径处理 URI 查询串：命中脱敏名单的
// 参数值指纹化后重组（丢值重建 RawQuery 编码）；无敏感键时原样返回
// rawURI，保证既有记录逐字节不变。
func evidenceURI(u *url.URL, rawURI string) string {
	if u == nil {
		return rawURI
	}
	q := u.Query()
	hit := false
	for key, values := range q {
		if !isSensitiveName(key) {
			continue
		}
		hit = true
		for i, v := range values {
			values[i] = redactHeaderValue(key, v)
		}
	}
	if !hit {
		return rawURI
	}
	u2 := *u
	u2.RawQuery = q.Encode()
	return u2.RequestURI()
}

// UnknownEndpointStat 是 501 backlog 聚合视图的一行，按命中次数降序输出，
// 供总控排定「先补哪个接口」的优先级。
type UnknownEndpointStat struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	Count          int    `json:"count"`
	EncryptedCount int    `json:"encrypted_count"`
	FirstSeen      string `json:"first_seen"`
	LastSeen       string `json:"last_seen"`
	LastNote       string `json:"last_note,omitempty"`
}

// unknownTracker 在进程内聚合 501 命中（method+path → 次数/加密次数/首末时间）。
// 持久化真源是 requests.ndjson 里的脱敏记录；聚合计数随进程重启归零。
type unknownTracker struct {
	mu    sync.Mutex
	byKey map[string]*UnknownEndpointStat
}

func newUnknownTracker() *unknownTracker {
	return &unknownTracker{byKey: make(map[string]*UnknownEndpointStat)}
}

func (t *unknownTracker) observe(u unknownRecord) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := strings.ToUpper(u.Method) + " " + u.Path
	stat, ok := t.byKey[key]
	if !ok {
		stat = &UnknownEndpointStat{Method: strings.ToUpper(u.Method), Path: u.Path, FirstSeen: u.Time}
		t.byKey[key] = stat
	}
	stat.Count++
	if u.Encrypted {
		stat.EncryptedCount++
	}
	stat.LastSeen = u.Time
	stat.LastNote = u.Note
}

// Snapshot 按命中次数降序返回聚合副本；次数并列时按 path、method 升序，
// 保证输出确定可测。
func (t *unknownTracker) Snapshot() []UnknownEndpointStat {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]UnknownEndpointStat, 0, len(t.byKey))
	for _, stat := range t.byKey {
		out = append(out, *stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}
