package apidoc

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/swaggest/swgui/v5/static"
)

//go:embed viewer.html
var viewerTemplate string

// ViewerHTML 内联 spec，使用 make api 导出的同目录 Swagger UI 资源。
// 可用 file:// 打开；内联 Huma 降级的 3.0.3，避免 Swagger 3.1 resolver
// 对 file:// 文档根的引用解析失败；不请求 CDN 或外部 validator。
func ViewerHTML(reg *httpapi.Registry, opt Options) ([]byte, error) {
	raw, err := Spec(reg, opt).Downgrade()
	if err != nil {
		return nil, err
	}
	return []byte(strings.Replace(viewerTemplate, "/*__SPEC__*/", strings.ReplaceAll(string(raw), "</", "<\\/"), 1)), nil
}

func swaggerAsset(name string) ([]byte, error) {
	compressed, err := static.FS.ReadFile(name + ".gz")
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
