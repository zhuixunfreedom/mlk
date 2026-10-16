package apidoc

import (
	"net/http"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/swaggest/swgui"
	"github.com/swaggest/swgui/v5emb"
)

const Prefix = "/mercd/api/"

// Register 将 Huma 的文档端点挂在已有 HTTP 骨架内。
// 在所有业务/管理路由登记之后调用，Huma 只匹配本命名空间内的请求。
// authKey 非空时文档面（重定向 + UI + spec）同样要求 web authkey
// 凭据（Bearer 头或 mercd_auth Cookie）；为空保持无鉴权现状。
func Register(reg *httpapi.Registry, authKey string) {
	var handler http.Handler
	redirect := func(c *httpapi.Ctx) error {
		http.Redirect(c.W, c.R, Prefix, http.StatusPermanentRedirect)
		return nil
	}

	reg.HandleWithStatus(http.MethodGet, strings.TrimSuffix(Prefix, "/"), "重定向到 Huma 接口文档", httpapi.ModelConfirmed, redirect)
	// Huma v2.39.1 原生 spec handler 懒缓存没有加锁；仅串行文档 spec/schema
	// 请求，静态资源并行提供，避免第一次并发读取产生 race。
	var specMu sync.Mutex
	docs := func(c *httpapi.Ctx) error {
		if strings.HasPrefix(c.R.URL.Path, Prefix+"openapi") || strings.HasPrefix(c.R.URL.Path, Prefix+"schemas/") {
			specMu.Lock()
			defer specMu.Unlock()
		}
		handler.ServeHTTP(c.W, c.R)
		return nil
	}

	reg.HandlePrefixWithStatus(http.MethodGet, Prefix, "Huma OpenAPI 与内嵌 Swagger UI", httpapi.ModelConfirmed, docs)
	mux := http.NewServeMux()
	spec := Spec(reg, Options{BaseURL: "/", AssetBase: "-"})
	// 禁用 Huma 的 CDN 查看器，使用依赖内嵌的 Swagger UI；不改变游戏协议。
	humago.New(mux, huma.Config{OpenAPI: spec, OpenAPIPath: Prefix + "openapi", SchemasPath: Prefix + "schemas"})
	ui := v5emb.NewHandlerWithConfig(swgui.Config{Title: spec.Info.Title, SwaggerJSON: Prefix + "openapi.json", BasePath: Prefix,
		SettingsUI: map[string]string{"validatorUrl": "null", "docExpansion": "\"none\"", "filter": "true", "displayOperationId": "true", "showExtensions": "true", "persistAuthorization": "false"},
	})
	mux.Handle("GET "+Prefix, ui)
	handler = mux
}
