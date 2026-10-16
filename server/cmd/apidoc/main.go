// Command apidoc 从活路由表（httpapi.Registry）导出 OpenAPI 3.0.3
// 文档、自包含 HTML 查看器与 Markdown 速查表，不开监听、不读业务资产。
//
// 用法：
//
//	go run ./cmd/apidoc -out docs/api-cn35                 # 默认 -service cn35（mercd35 主线）
//	go run ./cmd/apidoc -out <pkg>/api -base-url http://127.0.0.1:18091
//
// 产物：openapi.json（可导入 Apifox/Postman/Swagger UI）、index.html
// （双击即用）、routes.md（速查表）。路由元数据的唯一真源是注册表，
// 新端点只需按常规在 cn35game/admin35 注册，重跑本命令即同步进文档。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Dnchua/mercd-server/server/internal/apidoc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "apidoc:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("apidoc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	service := fs.String("service", "cn35", "导出哪个服务的路由表：仅支持 cn35（mercd35）")
	out := fs.String("out", "docs/api-cn35", "产物输出目录（OpenAPI 3.1/3.0.3、Swagger UI 及 routes.md）")
	baseURL := fs.String("base-url", "", "写入 servers[0].url 与查看器示例的服务地址（空=按 -service 取默认）")
	assetBase := fs.String("asset-base", "", "fixture 示例的相对路径前缀（空=按 -service 取默认）")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	reg, err := apidoc.BuildRegistryFor(*service)
	if err != nil {
		return err
	}
	opt := apidoc.Options{Service: *service, BaseURL: *baseURL, AssetBase: *assetBase}
	if err := apidoc.Write(reg, *out, opt); err != nil {
		return err
	}
	fmt.Printf("apidoc: %s 路由表 %d 条已导出到 %s（OpenAPI 3.1/3.0.3、Swagger UI 及 routes.md）\n", *service, reg.Len(), *out)
	return nil
}
