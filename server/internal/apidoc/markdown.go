package apidoc

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// RoutesMarkdown 渲染 routes.md：按分组排列的接口速查表，
// 便于在编辑器/聊天工具里直接浏览或贴进契约文档。
func RoutesMarkdown(reg *httpapi.Registry, opt Options) []byte {
	opt = opt.withDefaults()
	routes := reg.Routes()

	groups := make(map[string][]httpapi.Route)
	for _, route := range routes {
		groups[route.Group] = append(groups[route.Group], route)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		// account 排最前（启动链首包），mercd 运维端点排最后，其余按字典序。
		weight := func(name string) int {
			switch name {
			case "account":
				return -1
			case "mercd":
				return 1
			}
			return 0
		}
		if weight(names[i]) != weight(names[j]) {
			return weight(names[i]) < weight(names[j])
		}
		return names[i] < names[j]
	})

	serviceName := "mercd35 CN 3.5.0"
	contractRef := "docs/协议对接/cn350/contracts/"
	var b strings.Builder
	fmt.Fprintf(&b, "# 接口路由表（%s）\n\n", serviceName)
	fmt.Fprintf(&b, "> 本文件由 `go run ./cmd/apidoc`（server/ 目录）从活路由表自动生成，勿手改。\n")
	fmt.Fprintf(&b, "> 生成时间 %s · 路由数 %d · 默认监听 `%s`。\n",
		opt.Now.UTC().Format(time.RFC3339), len(routes), opt.BaseURL)
	fmt.Fprintf(&b, "> 交互版见同目录 `index.html`，机器可读版见 `openapi.json`，字段级契约见 `%s`。\n\n", contractRef)

	for _, name := range names {
		list := groups[name]
		title := groupTitles[name]
		if title == "" {
			title = name
		}
		fmt.Fprintf(&b, "## %s · `%s`（%d 条）\n\n", title, name, len(list))
		b.WriteString("| 方法 | 路径 | 状态 | 说明 |\n|---|---|---|---|\n")
		for _, route := range list {
			path := route.Path
			if route.Prefix {
				path = strings.TrimSuffix(route.Path, "/") + "/{filename}"
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n",
				route.Method, path, route.Status.String(), mdEscape(route.Note))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func mdEscape(text string) string {
	text = strings.ReplaceAll(text, "|", "\\|")
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}
