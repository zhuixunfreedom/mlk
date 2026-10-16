package ops

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// OpsDeps contains the small set of state exposed by the ops endpoints.
type OpsDeps struct {
	Registry   *httpapi.Registry
	Started    time.Time
	AssetsDir  string
	StateDir   string
	ServerHost string
	// Accounts 返回当前账号总数；nil 时 health 的 accounts 字段为 0。
	Accounts func() (int, error)

	Log *slog.Logger
}

// HealthResponse 同时用于实际出包与 Huma 文档字段生成。
type HealthResponse struct {
	Status            string `json:"status"`
	Service           string `json:"service"`
	Protocol          string `json:"protocol"`
	UptimeSec         int    `json:"uptime_sec"`
	RoutesTotal       int    `json:"routes_total"`
	RoutesImplemented int    `json:"routes_implemented"`
	Accounts          int    `json:"accounts"`
	ServerHost        string `json:"server_host"`
	AssetsDir         string `json:"assets_dir"`
	StateDir          string `json:"state_dir"`
}

// RegisterOps 注册无鉴权只读运维端点 /mercd/health、/mercd/routes、
// /mercd/unknown-endpoints。返回注册路由数，与 Register 风格一致。
func RegisterOps(reg *httpapi.Registry, dep OpsDeps) int {
	if dep.Log == nil {
		dep.Log = slog.Default()
	}
	n := 0
	add := func(path, note string, h httpapi.Handler) {

		if reg.HandleWithStatus(http.MethodGet, httpapi.OpsPrefix+path, note,
			httpapi.ModelConfirmed, h) {
			n++
		}
	}

	add("health", "CN3.5 服务健康检查", func(c *httpapi.Ctx) error {
		accounts := 0
		if dep.Accounts != nil {
			var err error
			accounts, err = dep.Accounts()
			if err != nil {
				return err
			}
		}
		routes := dep.Registry.Routes()
		implemented := 0
		for _, route := range routes {
			if route.Status == httpapi.ModelConfirmed {
				implemented++
			}
		}
		httpapi.WriteJSON(c.W, http.StatusOK, HealthResponse{
			Status: "ok", Service: "mercd35", Protocol: "cn35-3.5.0",
			UptimeSec:   int(time.Since(dep.Started).Seconds()),
			RoutesTotal: len(routes), RoutesImplemented: implemented, Accounts: accounts,
			ServerHost: dep.ServerHost, AssetsDir: dep.AssetsDir, StateDir: dep.StateDir,
		})
		return nil
	})
	reg.DescribeResponse(http.MethodGet, httpapi.OpsPrefix+"health", HealthResponse{})

	add("routes", "导出 CN3.5 路由及响应状态", func(c *httpapi.Ctx) error {
		httpapi.WriteJSON(c.W, http.StatusOK, map[string]any{"routes": dep.Registry.Routes()})
		return nil
	})

	// S-OBS-01：501 未实现请求的可排序 backlog。数据来自 Registry 的进程内
	// 聚合（命中次数降序），用于按客户端实际撞墙频次排定补接口优先级；
	// 脱敏明细以 requests.ndjson 的 unknown_endpoint 记录为持久化真源。
	add("unknown-endpoints", "聚合 501 未实现请求 backlog（按命中次数降序）",
		func(c *httpapi.Ctx) error {
			stats := dep.Registry.UnknownEndpoints()
			total := 0
			for _, stat := range stats {
				total += stat.Count
			}
			httpapi.WriteJSON(c.W, http.StatusOK, map[string]any{
				"unknown_endpoints":  stats,
				"distinct_endpoints": len(stats),
				"total_501_hits":     total,
			})
			return nil
		})

	dep.Log.Info("CN3.5 运维端点已注册", "prefix", httpapi.OpsPrefix, "routes", n)
	return n
}
