// Package httpapi 提供 CN 服务的 HTTP 骨架：请求上下文、路由注册、
// 响应封装与请求取证。
//
// 设计底线（来自 docs/PROGRESS.md 工作原则 1「禁止猜测式修复」）：
//
//	凡响应模型未经客户端运行时验证的路由，一律返回 501 并把请求写入
//	取证日志，绝不返回编造的「看起来像对的」响应。自 S-OBS-01 起，501 的
//	取证记录为脱敏形态：敏感 header 只留 sha256 指纹、请求体只留 SHA-256，
//	并聚合为可排序 backlog（/mercd/unknown-endpoints）；已确认路由的 200
//	取证记录保持原有的全量明文字段。
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
)

// ModelStatus 标记一条路由的响应模型确认程度。它决定该路由当前允许返回什么。
type ModelStatus uint8

const (
	// ModelUnknown 响应结构未确认。只允许返回 501 + 取证记录。
	ModelUnknown ModelStatus = iota
	// ModelProbed 已有候选响应，但尚未经客户端运行时验证，默认仍不返回。
	ModelProbed
	// ModelConfirmed 已由客户端运行时验证通过。
	ModelConfirmed
)

func (m ModelStatus) String() string {
	switch m {
	case ModelProbed:
		return "probed"
	case ModelConfirmed:
		return "confirmed"
	default:
		return "unknown"
	}
}

// MarshalJSON 让路由表以可读字符串而非数字导出，便于 /mercd/routes 直接阅读。
func (m ModelStatus) MarshalJSON() ([]byte, error) {
	return []byte(`"` + m.String() + `"`), nil
}

// Handler 是业务处理器。返回 error 表示处理失败，由框架统一转成诊断响应
// 并记录到取证日志；返回 nil 表示处理器已经自己写过响应。
type Handler func(*Ctx) error

// ErrNotImplemented 表示「响应模型尚未确认」，是骨架期的正常返回值。
var ErrNotImplemented = errors.New("httpapi: CN 响应模型未确认")

// Ctx 承载单次请求的全部上下文，是处理器唯一的输入。
type Ctx struct {
	W      http.ResponseWriter
	R      *http.Request
	Body   []byte
	Log    *slog.Logger
	Route  string
	Status ModelStatus
	// Decoded 存放处理器解码后的请求字段（如二进制帧解出的方法名与各请求字段），
	// 由取证日志写入 NDJSON 的 decoded 字段。可选；为 nil 时不输出。
	Decoded map[string]any
}

// Query 取查询参数，语义同 net/url 的 Query().Get。
func (c *Ctx) Query(name string) string { return c.R.URL.Query().Get(name) }

// Header 取请求头，语义同 http.Header.Get。
func (c *Ctx) Header(name string) string { return c.R.Header.Get(name) }

// IsEncrypted reports whether the request carries the client's encryption hint.
// 501 脱敏取证（unknown_evidence.go）共用同一判定。
func (c *Ctx) IsEncrypted() bool { return requestEncrypted(c.R) }

// NotImplemented 生成一个「响应模型未确认」的占位处理器。
// note 会出现在 501 响应体与日志里，说明这条路由卡在哪一步。
func NotImplemented(note string) Handler {
	return func(c *Ctx) error {
		return &UnimplementedError{Route: c.Route, Note: note}
	}
}

// UnimplementedError 让框架能把「未确认」与「处理出错」区分开：
// 前者是当前阶段的预期状态，返回 501；后者返回 500。
type UnimplementedError struct {
	Route string
	Note  string
}

func (e *UnimplementedError) Error() string {
	if e.Note == "" {
		return "响应模型未确认: " + e.Route
	}
	return "响应模型未确认: " + e.Route + " — " + e.Note
}

// Is 让 errors.Is(err, ErrNotImplemented) 对任何 UnimplementedError 成立。
func (e *UnimplementedError) Is(target error) bool { return target == ErrNotImplemented }
