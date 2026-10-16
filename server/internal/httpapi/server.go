package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options controls the single CN HTTP listener.
type Options struct {
	Addr           string
	RequestLog     string
	EventLog       string
	Verbose        bool
	StripAPIPrefix bool
	// CORS 非空时作为 Access-Control-Allow-Origin 的值回给浏览器，
	// 并短路 OPTIONS 预检（204）。仅用于本机 UI 复原页跨源调接口；
	// 游戏客户端不是浏览器，生产路径不需要它。
	CORS string
	// Middleware 非空时包裹最终 handler（如 JP 侧的路径归一化）。
	Middleware func(http.Handler) http.Handler
	// TLSCertFile/TLSKeyFile 成对非空时 Run 走 HTTPS（本地自签重定向方案）。
	TLSCertFile string
	TLSKeyFile  string
}

// Server is a small HTTP dispatcher with request evidence logging.
type Server struct {
	opt    Options
	reg    *Registry
	log    *slog.Logger
	rec    *recorder
	events *eventWriter
	once   sync.Once
	h      http.Handler
}

// NewServer constructs a server around a CN route registry.
func NewServer(opt Options, reg *Registry, log *slog.Logger) (*Server, error) {
	if reg == nil {
		return nil, fmt.Errorf("httpapi: registry 不能为空")
	}
	if log == nil {
		log = slog.Default()
	}
	rec, err := newRecorder(opt.RequestLog)
	if err != nil {
		return nil, err
	}
	events, err := newEventWriter(opt.EventLog)
	if err != nil {
		_ = rec.Close()
		return nil, err
	}
	return &Server{opt: opt, reg: reg, log: log, rec: rec, events: events}, nil
}

// Close closes evidence streams.
func (s *Server) Close() error {
	err := s.rec.Close()
	if e := s.events.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

// Handler returns the reusable HTTP handler.
func (s *Server) Handler() http.Handler {
	s.once.Do(func() {
		s.h = http.HandlerFunc(s.serve)
		if s.opt.Middleware != nil {
			s.h = s.opt.Middleware(s.h)
		}
	})
	return s.h
}

// NormalizeAPIPath tolerates an optional legacy-looking /api123 prefix while
// keeping the CN route table canonical. The CN client normally sends no such
// prefix; this is only a harmless path normalizer, not a compatibility mode.
func NormalizeAPIPath(path string) (string, string) {
	if len(path) < 5 || path[0] != '/' {
		return path, ""
	}
	segment, tail, hasTail := strings.Cut(path[1:], "/")
	if len(segment) < 4 || !strings.HasPrefix(segment, "api") {
		return path, ""
	}
	for i := 3; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return path, ""
		}
	}
	prefix := "/" + segment
	if !hasTail || tail == "" {
		return "/", prefix
	}
	return "/" + tail, prefix
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if s.opt.CORS != "" {
		w.Header().Set("Access-Control-Allow-Origin", s.opt.CORS)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "*")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	started := time.Now()
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "读取请求体失败", http.StatusBadRequest)
		return
	}
	rawPath, rawURI := r.URL.Path, r.URL.RequestURI()
	// 管理后台静态资源请求（/mercd/admin/ 下的 js/css/png/json）跳过
	// events.ndjson 与 requests.ndjson：两个文件的语义是游戏客户端协议
	// 取证真源，几十个浏览器静态资源会把它冲垮。-v 的 slog 摘要不受影响。
	staticUI := strings.HasPrefix(rawPath, StaticUIPrefix) || strings.HasPrefix(rawPath, AdminAPIPrefix) ||
		strings.HasPrefix(rawPath, OpsPrefix+"api/") || rawPath == OpsPrefix+"api"
	if !staticUI {
		s.events.write(started, r.Method, rawPath, evidenceURI(r.URL, rawURI))
	}

	normalized, prefix := rawPath, ""
	if s.opt.StripAPIPrefix {
		normalized, prefix = NormalizeAPIPath(rawPath)
	}
	route, known := s.reg.Lookup(r.Method, normalized)
	if !known {
		route = Route{Method: strings.ToUpper(r.Method), Path: normalized, Group: groupOf(normalized), Note: "未注册 CN 路由"}
	}
	ctx := &Ctx{
		W: w, R: r, Body: body,
		Log:   s.log.With("route", route.Path, "method", r.Method),
		Route: route.Path, Status: route.Status,
	}
	status := &statusWriter{ResponseWriter: w, code: http.StatusOK}
	var serveErr error
	if !known {
		serveErr = &UnimplementedError{Route: route.Path, Note: route.Note}
		s.renderError(status, route, serveErr)
	} else {
		handler, _ := s.reg.handlerFor(r.Method, normalized)
		serveErr = s.invoke(handler, ctx, route)
		s.renderError(status, route, serveErr)
	}

	c := capture{route: route, req: r, body: body, code: status.code,
		duration: time.Since(started), known: known, rawPath: rawPath, rawURI: rawURI,
		apiPrefix: prefix, ctx: ctx}
	if status.code == http.StatusNotImplemented {
		// S-OBS-01：501 只落脱敏取证（聚合 + slog + ndjson），
		// 明文 body 与敏感 header 全值绝不写盘。
		u := buildUnknownRecord(c, unimplementedNote(route, serveErr), time.Now())
		s.recordUnknownEvidence(u)
	} else if !staticUI {
		// 常规记录保留明文 body 与协议密文头全值（既有取证契约），但凭据类
		// header（authorization/x-admin-token/cookie 等）与敏感查询参数仍按
		// 脱敏名单指纹化——管理面令牌绝不能落进可共享的 requests.ndjson。
		s.rec.record(c)
	}
	if s.opt.Verbose {
		s.log.Info("CN 请求", "path", evidenceURI(r.URL, rawURI), "status", status.code, "known", known,
			"body_len", len(body), "duration", time.Since(started).String())
	}
}

// recordUnknownEvidence 落盘一次 501 的全部取证副作用：进程内 backlog 聚合、
// slog 结构化告警、requests.ndjson 追加脱敏记录。三处共用同一份脱敏字段。
func (s *Server) recordUnknownEvidence(u unknownRecord) {
	s.reg.observeUnknown(u)
	s.log.Warn("CN 501 未实现请求",
		"method", u.Method, "path", u.Path, "encrypted", u.Encrypted,
		"body_sha256", u.BodySHA256, "body_len", u.BodyLen,
		"client_addr", u.ClientAddr, "route_known", u.Known, "note", u.Note)
	s.rec.recordUnknown(u)
}

// OpsPrefix identifies local operational endpoints.
const OpsPrefix = "/mercd/"

// StaticUIPrefix 是本机管理后台前端静态资源前缀。这些请求不写协议取证日志：
// requests.ndjson / events.ndjson 的语义是游戏客户端协议证据，浏览器静态资源不属于其中。
const StaticUIPrefix = OpsPrefix + "admin/"

// AdminAPIPrefix 是管理后台数据接口前缀（admin-web 调用）。同理不写
// 协议取证日志：浏览器轮询会淹没客户端证据流；其凭据头（Bearer/Cookie）
// 也不应出现在可共享的取证文件里。
const AdminAPIPrefix = OpsPrefix + "adminapi/"

func (s *Server) invoke(handler Handler, ctx *Ctx, route Route) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("处理器 panic: %v", recovered)
			s.log.Error("CN 处理器 panic", "route", route.Path, "panic", recovered)
		}
	}()
	if handler == nil {
		return &UnimplementedError{Route: route.Path, Note: route.Note}
	}
	return handler(ctx)
}

func (s *Server) renderError(w *statusWriter, route Route, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, ErrNotImplemented) {
		WriteDiagnostic(w, http.StatusNotImplemented, diagnostic{
			Status: "not_implemented", Route: route.Path, Stage: route.Status.String(),
			Note: unimplementedNote(route, err),
		})
		return
	}
	s.log.Error("CN 处理器返回错误", "route", route.Path, "err", err)
	WriteDiagnostic(w, http.StatusInternalServerError, diagnostic{
		Status: "handler_error", Route: route.Path, Stage: route.Status.String(), Note: err.Error(),
	})
}

// unimplementedNote 解析 501 诊断体与脱敏取证记录共用的 note：
// UnimplementedError 自带说明时优先，否则退回路由登记说明。
func unimplementedNote(route Route, err error) string {
	note := route.Note
	var unimplemented *UnimplementedError
	if errors.As(err, &unimplemented) && unimplemented.Note != "" {
		note = unimplemented.Note
	}
	return note
}

// Run starts the listener until cancellation or SIGINT/SIGTERM.
func (s *Server) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if (s.opt.TLSCertFile == "") != (s.opt.TLSKeyFile == "") {
		return fmt.Errorf("httpapi: TLSCertFile 与 TLSKeyFile 必须成对提供")
	}
	listener, err := net.Listen("tcp", s.opt.Addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", s.opt.Addr, err)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		scheme := "http"
		if s.opt.TLSCertFile != "" {
			scheme = "https"
		}
		s.log.Info("服务已启动", "addr", listener.Addr().String(), "scheme", scheme, "routes", s.reg.Len(), "reqlog", s.opt.RequestLog)
		var err error
		if s.opt.TLSCertFile != "" {
			err = server.ServeTLS(listener, s.opt.TLSCertFile, s.opt.TLSKeyFile)
		} else {
			err = server.Serve(listener)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	const maxBody = 4 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("请求体超过 %d 字节上限", maxBody)
	}
	return body, nil
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

type capture struct {
	route     Route
	req       *http.Request
	body      []byte
	code      int
	duration  time.Duration
	known     bool
	rawPath   string
	rawURI    string
	apiPrefix string
	ctx       *Ctx
}

type captureRecord struct {
	Time      string            `json:"time"`
	Method    string            `json:"method"`
	URI       string            `json:"uri"`
	Path      string            `json:"path"`
	PathRaw   string            `json:"path_raw,omitempty"`
	APIPrefix string            `json:"api_prefix,omitempty"`
	Group     string            `json:"group"`
	Query     map[string]string `json:"query,omitempty"`
	Status    int               `json:"status"`
	DurMS     int64             `json:"dur_ms"`
	Headers   map[string]string `json:"headers,omitempty"`
	BodyB64   string            `json:"body_b64,omitempty"`
	BodyLen   int               `json:"body_len"`
	Known     bool              `json:"route_known"`
	Critical  bool              `json:"startup_critical"`
	Decoded   map[string]any    `json:"decoded,omitempty"`
}

type recorder struct {
	mu sync.Mutex
	f  *os.File
}

func newRecorder(path string) (*recorder, error) {
	if path == "" {
		return &recorder{}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开取证日志 %q 失败: %w", path, err)
	}
	return &recorder{f: f}, nil
}

func (r *recorder) Close() error {
	if r.f == nil {
		return nil
	}
	return r.f.Close()
}

func (r *recorder) record(c capture) {
	if r.f == nil {
		return
	}
	entry := captureRecord{
		Time: time.Now().Format(time.RFC3339Nano), Method: c.req.Method,
		URI:  evidenceURI(c.req.URL, c.rawURI),
		Path: c.route.Path, Group: c.route.Group, Status: c.code,
		DurMS: c.duration.Milliseconds(), Headers: evidenceHeaders(c.req.Header),
		BodyLen: len(c.body), Known: c.known, Critical: isStartupCritical(c.route.Path),
		PathRaw: c.rawPath, APIPrefix: c.apiPrefix,
	}
	if c.rawPath == c.route.Path {
		entry.PathRaw = ""
	}
	if len(c.body) > 0 {
		entry.BodyB64 = encodeBase64(c.body)
	}
	if query := c.req.URL.Query(); len(query) > 0 {
		entry.Query = redactQuery(query)
	}
	if c.ctx != nil && c.ctx.Decoded != nil {
		entry.Decoded = c.ctx.Decoded
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.f.Write(append(line, '\n'))
}

// recordUnknown 追加一条 501 脱敏取证记录（S-OBS-01）到同一条请求日志流。
func (r *recorder) recordUnknown(u unknownRecord) {
	if r.f == nil {
		return
	}
	line, err := json.Marshal(u)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.f.Write(append(line, '\n'))
}

type eventWriter struct {
	mu sync.Mutex
	f  *os.File
}

func newEventWriter(path string) (*eventWriter, error) {
	if path == "" {
		return &eventWriter{}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开事件日志 %q 失败: %w", path, err)
	}
	return &eventWriter{f: f}, nil
}

func (e *eventWriter) Close() error {
	if e.f == nil {
		return nil
	}
	return e.f.Close()
}

func (e *eventWriter) write(at time.Time, method, path, uri string) {
	if e.f == nil {
		return
	}
	line, err := json.Marshal(map[string]any{
		"time": at.Format(time.RFC3339Nano), "unix": at.UnixMilli(),
		"method": method, "path": path, "uri": uri,
	})
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.f.Write(append(line, '\n'))
}

var startupCriticalPaths = map[string]bool{
	"/account":                        true,
	"/asset_info_android.json.encode": true,
	"/users/preset_data.json":         true,
	"/app_conf_android.json.encode":   true,
}

func isStartupCritical(path string) bool { return startupCriticalPaths[path] }
