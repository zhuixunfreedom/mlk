package httpapi

import (
	"sort"
	"strings"
)

// Route 一条已注册的路由。
type Route struct {
	Method string // 目前启动链实测全为 POST
	Path   string
	Group  string
	Note   string
	Status ModelStatus
	// Prefix 为 true 时 Path 是最长前缀而非精确路径（末段是数据 id，
	// 如 /quests/execute/1.json），由 HandlePrefixWithStatus 登记；
	// 文档导出与 501 诊断可据此区分「真实 path」与「前缀模板」。
	Prefix bool
	// ResponseModel 为文档提供实际出包的 Go 类型；不改变处理器或响应字节。
	ResponseModel any `json:"-"`
}

// Registry 收集路由定义。它不直接绑定 net/http，由 Server 统一展开成 mux 模式，
// 这样路由表可以被列举、被打印、被测试，而不必启动服务。
//
// 它同时承载 501 未实现请求的进程内聚合（S-OBS-01）：Server 在 501 发生时
// 写入观察，/mercd/unknown-endpoints 管理端点读取聚合，两者共享同一注册表
// 而不必改动 main 装配代码。
type Registry struct {
	routes       []Route
	handlers     []Handler
	seen         map[string]int // "METHOD /path" -> routes 下标
	prefixRoutes []prefixRoute
	prefixSeen   map[string]struct{}
	unknown      *unknownTracker
}

// prefixRoute is an intentionally small escape hatch for endpoints whose path
// contains a server-selected identifier (for example /quests/execute/1.json).
// It keeps the matcher in the registry without requiring a guessed list of ids.
type prefixRoute struct {
	method string
	prefix string
	index  int
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		seen:       make(map[string]int, 512),
		prefixSeen: make(map[string]struct{}, 32),
		unknown:    newUnknownTracker(),
	}
}

func routeKey(method, path string) string { return method + " " + path }

// Handle 注册一条路由，响应模型状态默认为 ModelUnknown。
func (r *Registry) Handle(method, path, note string, h Handler) bool {
	return r.HandleWithStatus(method, path, note, ModelUnknown, h)
}

// HandleWithStatus 注册一条路由并显式声明响应模型状态。
//
// 重复注册同一 (method, path) 会被忽略并返回 false。这一点是刻意设计：
// 启动顺序是「先注册已明确的真实处理器 → 再用生成的路由表补齐占位处理器」，
// 后者必须不能覆盖前者。
func (r *Registry) HandleWithStatus(method, path, note string, st ModelStatus, h Handler) bool {
	k := routeKey(method, path)
	if _, dup := r.seen[k]; dup {
		return false
	}
	if h == nil {
		h = NotImplemented(note)
	}
	r.seen[k] = len(r.routes)
	r.routes = append(r.routes, Route{
		Method: strings.ToUpper(method),
		Path:   path,
		Group:  groupOf(path),
		Note:   note,
		Status: st,
	})
	r.handlers = append(r.handlers, h)
	return true
}

// HandlePrefixWithStatus registers a longest-prefix route. It is useful for
// endpoints whose final path component is data rather than a route name. The
// prefix is still exposed in Routes and request evidence, so a dynamic request
// never looks like an unregistered 501 fallback.
func (r *Registry) HandlePrefixWithStatus(method, prefix, note string, st ModelStatus, h Handler) bool {
	method = strings.ToUpper(method)
	if prefix == "" || !strings.HasPrefix(prefix, "/") {
		return false
	}
	key := routeKey(method, prefix)
	if _, dup := r.seen[key]; dup {
		return false
	}
	if _, dup := r.prefixSeen[key]; dup {
		return false
	}
	if h == nil {
		h = NotImplemented(note)
	}
	r.seen[key] = len(r.routes)
	r.prefixSeen[key] = struct{}{}
	r.routes = append(r.routes, Route{
		Method: method,
		Path:   prefix,
		Group:  groupOf(prefix),
		Note:   note,
		Status: st,
		Prefix: true,
	})
	r.handlers = append(r.handlers, h)
	r.prefixRoutes = append(r.prefixRoutes, prefixRoute{
		method: method,
		prefix: prefix,
		index:  len(r.routes) - 1,
	})
	return true
}

// Has 报告某路由是否已注册。
func (r *Registry) Has(method, path string) bool {
	_, ok := r.seen[routeKey(method, path)]
	return ok
}

// Lookup 按 (方法, 路径) 取回路由定义。方法名大小写不敏感。
//
// 请求路径可能先经过归一化，所以「这条请求属于哪条登记路由」必须在注册表里查，
// 而不能依赖 net/http mux 的静态模式匹配。
func (r *Registry) Lookup(method, path string) (Route, bool) {
	method = strings.ToUpper(method)
	if i, ok := r.seen[routeKey(method, path)]; ok {
		// Exact routes take precedence over a dynamic prefix route.
		return r.routes[i], true
	}
	if i, ok := r.lookupPrefix(method, path); ok {
		return r.routes[i], true
	}
	return Route{}, false
}

// handlerFor 返回某路由挂的处理器。
func (r *Registry) handlerFor(method, path string) (Handler, bool) {
	method = strings.ToUpper(method)
	if i, ok := r.seen[routeKey(method, path)]; ok {
		return r.handlers[i], true
	}
	if i, ok := r.lookupPrefix(method, path); ok {
		return r.handlers[i], true
	}
	return nil, false
}

func (r *Registry) lookupPrefix(method, path string) (int, bool) {
	method = strings.ToUpper(method)
	best := -1
	bestLen := -1
	for _, entry := range r.prefixRoutes {
		if entry.method != method || !strings.HasPrefix(path, entry.prefix) {
			continue
		}
		if len(entry.prefix) > bestLen {
			best = entry.index
			bestLen = len(entry.prefix)
		}
	}
	return best, best >= 0
}

// Len 返回已注册路由数。
func (r *Registry) Len() int { return len(r.routes) }

// DescribeResponse 在同一注册表附加响应类型，供 Huma 生成字段 Schema。
// model 应与处理器实际序列化的类型一致；不存在的路由返回 false。
func (r *Registry) DescribeResponse(method, path string, model any) bool {
	i, ok := r.seen[routeKey(strings.ToUpper(method), path)]
	if !ok {
		return false
	}
	r.routes[i].ResponseModel = model
	return true
}

// Routes 按 (路径, 方法) 排序返回路由副本，便于稳定输出。
func (r *Registry) Routes() []Route {
	out := make([]Route, len(r.routes))
	copy(out, r.routes)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// handlersInRegistrationOrder 返回与 r.routes 同序的处理器切片。
func (r *Registry) handlersInRegistrationOrder() []Handler { return r.handlers }

// observeUnknown 由 Server 在 501 发生时调用，更新 backlog 聚合计数。
func (r *Registry) observeUnknown(u unknownRecord) {
	if r == nil {
		return
	}
	r.unknown.observe(u)
}

// UnknownEndpoints 返回 501 未实现请求的聚合视图（按命中次数降序），
// 供 /mercd/unknown-endpoints 输出可排序 backlog。计数随进程重启归零，
// 持久化真源是 requests.ndjson 中的脱敏记录。
func (r *Registry) UnknownEndpoints() []UnknownEndpointStat {
	if r == nil {
		return nil
	}
	return r.unknown.Snapshot()
}

func groupOf(path string) string {
	p := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}
