package apidoc

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func TestHumaDocsUseLiveRegistryAndPreserveProtocol(t *testing.T) {
	reg := httpapi.NewRegistry()
	reg.HandleWithStatus(http.MethodPost, "/binary", "协议回放", httpapi.ModelConfirmed, func(c *httpapi.Ctx) error {
		c.W.Header().Set("Content-Type", "application/octet-stream")
		_, err := c.W.Write(c.Body)
		return err
	})
	Register(reg, "")
	dir := t.TempDir()
	reqlog, eventlog := filepath.Join(dir, "requests.ndjson"), filepath.Join(dir, "events.ndjson")
	srv, err := httpapi.NewServer(httpapi.Options{RequestLog: reqlog, EventLog: eventlog}, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h := srv.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	w := get(Prefix + "openapi.json")
	if w.Code != http.StatusOK {
		t.Fatalf("spec: %d %s", w.Code, w.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	if paths["/binary"] == nil || paths["/account"] != nil {
		t.Fatalf("在线文档未使用运行实例: %v", paths)
	}
	if spec["x-route-count"] != float64(reg.Len()) || spec["openapi"] != "3.1.0" {
		t.Fatalf("在线元数据=%v", spec)
	}
	for _, path := range []string{Prefix, Prefix + "swagger-ui-bundle.js", Prefix + "swagger-ui.css", Prefix + "openapi.yaml", Prefix + "openapi-3.0.json", Prefix + "openapi-3.0.yaml", Prefix + "swagger-ui-standalone-preset.js"} {
		w := get(path)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("%s: %d %d", path, w.Code, w.Body.Len())
		}
	}
	w = get(Prefix)
	if strings.Contains(w.Body.String(), "unpkg.com") || !strings.Contains(w.Body.String(), `validatorUrl: null`) {
		t.Fatal("查看器依赖外部资源或 validator 未关闭")
	}
	w = get(Prefix + "openapi-3.0.json")
	if !strings.Contains(w.Body.String(), `"openapi":"3.0.3"`) {
		t.Fatal("Huma 3.0 降级不可用")
	}
	if w := get(strings.TrimSuffix(Prefix, "/")); w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != Prefix {
		t.Fatal("文档入口重定向错误")
	}
	if w := get(Prefix + "missing.js"); w.Code != http.StatusNotFound {
		t.Fatal("不存在的文档资源应该返回 404")
	}
	for _, path := range []string{reqlog, eventlog} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) != 0 {
			t.Fatalf("文档请求污染客户端取证日志: %s", path)
		}
	}
	binary := "\x00\xff\x18game-protocol"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/binary", strings.NewReader(binary)))
	if w.Code != http.StatusOK || w.Body.String() != binary {
		t.Fatal("文档接入改变原始协议字节")
	}
	raw, err := os.ReadFile(reqlog)
	if err != nil || !strings.Contains(string(raw), "/binary") {
		t.Fatal("原游戏请求取证丢失")
	}
}

func TestHumaDocsConcurrentFirstRequest(t *testing.T) {
	reg := httpapi.NewRegistry()
	Register(reg, "")
	srv, err := httpapi.NewServer(httpapi.Options{}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h := srv.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ext := []string{".json", ".yaml", "-3.0.json", "-3.0.yaml"}[i%4]
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, Prefix+"openapi"+ext, nil))
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Errorf("并发 spec 请求失败: %d", w.Code)
			}
		}(i)
	}
	wg.Wait()
}

func TestOfflineExportContainsLocalSwaggerAssets(t *testing.T) {
	dir := t.TempDir()
	if err := Write(docRegistry(t), dir, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "openapi.json", "openapi-3.0.json", "routes.md", "swagger-ui-bundle.js", "swagger-ui.css"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Size() == 0 {
			t.Fatalf("导出缺少 %s: %v", name, err)
		}
	}
}
