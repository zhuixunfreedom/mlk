package apidoc

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func specPaths(t *testing.T) (map[string]any, *httpapi.Registry) {
	t.Helper()
	reg := docRegistry(t)
	spec := specMap(t, Spec(reg, Options{Service: "cn35"}))
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec.paths 不是对象")
	}
	return paths, reg
}

// 每条已注册路由都必须出现在 OpenAPI paths 里，且方法键与登记一致。
func TestSpecCoversEveryRegisteredRoute(t *testing.T) {
	paths, reg := specPaths(t)
	if reg.Len() == 0 {
		t.Fatal("注册表为空")
	}
	for _, route := range reg.Routes() {
		key := route.Path
		if route.Prefix {
			key = strings.TrimSuffix(route.Path, "/") + "/{filename}"
		}
		item, ok := paths[key].(map[string]any)
		if !ok {
			t.Fatalf("paths 缺少 %s", key)
		}
		op, ok := item[strings.ToLower(route.Method)].(map[string]any)
		if !ok {
			t.Fatalf("%s 缺少 %s 操作", key, route.Method)
		}
		if op["x-model-status"] != route.Status.String() {
			t.Fatalf("%s %s x-model-status=%v，期望 %s",
				route.Method, route.Path, op["x-model-status"], route.Status.String())
		}
	}
}

// 前缀路由必须展开成 {filename} 模板并携带 path 参数说明。
func TestPrefixRouteBecomesTemplatePath(t *testing.T) {
	paths, _ := specPaths(t)
	item, ok := paths["/toto_image_4/unity/{filename}"].(map[string]any)
	if !ok {
		t.Fatal("缺少 /toto_image_4/unity/{filename}")
	}
	get, ok := item["get"].(map[string]any)
	if !ok {
		t.Fatal("缺少 GET 操作")
	}
	if get["x-route-prefix"] != true {
		t.Fatal("x-route-prefix 应为 true")
	}
	params, _ := get["parameters"].([]any)
	found := false
	for _, p := range params {
		pm, _ := p.(map[string]any)
		if pm["name"] == "filename" && pm["in"] == "path" {
			found = true
		}
	}
	if !found {
		t.Fatal("缺少 filename path 参数")
	}
}

// 原始前缀键不应作为 path 出现（避免和一个假「精确路径」混淆）。
func TestRawPrefixNotInPaths(t *testing.T) {
	paths, _ := specPaths(t)
	if _, ok := paths["/toto_image_4/unity/"]; ok {
		t.Fatal("原始前缀不应成为独立 path 键")
	}
}

func TestSpecMetadata(t *testing.T) {
	reg := docRegistry(t)
	raw, err := OpenAPIJSON(reg, Options{Service: "cn35", BaseURL: "http://127.0.0.1:18091"})
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("openapi.json 不是合法 JSON: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("openapi 版本=%v", spec["openapi"])
	}
	servers, _ := spec["servers"].([]any)
	if len(servers) == 0 || servers[0].(map[string]any)["url"] != "http://127.0.0.1:18091" {
		t.Fatalf("servers 未采用 -base-url: %v", servers)
	}
	if spec["x-route-count"] != float64(reg.Len()) {
		t.Fatalf("x-route-count=%v，期望 %d", spec["x-route-count"], reg.Len())
	}
}

// cn35 注册表应与 mercd35 --list-routes 同源，且默认元数据指向 3.5。
func TestCN35Registry(t *testing.T) {
	reg := docRegistry(t)
	if reg.Len() == 0 {
		t.Fatal("cn35 注册表为空")
	}
	spec := specMap(t, Spec(reg, Options{}))
	if spec["info"].(map[string]any)["version"] != "3.5.0-cn350" {
		t.Fatalf("cn35 默认 version=%v", spec["info"].(map[string]any)["version"])
	}
	servers, _ := spec["servers"].([]any)
	if servers[0].(map[string]any)["url"] != "http://127.0.0.1:18091" {
		t.Fatalf("cn35 默认 servers=%v", servers[0])
	}
	paths := spec["paths"].(map[string]any)
	for _, want := range []string{"/account", "/users/preset_data.json", "/users/version_info"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("cn35 paths 缺少 %s", want)
		}
	}
	// 运维端点与内嵌后台应与 mercd35 同源入册。
	for _, want := range []string{"/mercd/health", "/mercd/routes", "/mercd/unknown-endpoints"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("cn35 paths 缺少运维端点 %s", want)
		}
	}

	if reg, err := BuildRegistryFor("bogus"); reg != nil || err == nil {
		t.Fatal("未知 service 应返回 nil")
	}
}

// 二进制帧端点与非确认状态响应要落在文档里。
func TestOctetStreamAnd501(t *testing.T) {
	paths, _ := specPaths(t)
	account := paths["/account"].(map[string]any)["post"].(map[string]any)
	body := account["requestBody"].(map[string]any)["content"].(map[string]any)
	if _, ok := body["application/octet-stream"]; !ok {
		t.Fatal("/account 缺少 octet-stream 请求体")
	}

	reg := httpapi.NewRegistry()
	reg.Handle(http.MethodGet, "/pending", "未确认", nil)
	spec := specMap(t, Spec(reg, Options{}))
	pending := spec["paths"].(map[string]any)["/pending"].(map[string]any)["get"].(map[string]any)
	if _, ok := pending["responses"].(map[string]any)["501"]; !ok {
		t.Fatal("未确认路由应声明 501 诊断响应")
	}
}

func TestViewerEmbedsSpec(t *testing.T) {
	reg := docRegistry(t)
	raw, err := ViewerHTML(reg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if !strings.Contains(html, `"openapi":"3.0.3"`) {
		t.Fatal("index.html 未内联 OpenAPI JSON")
	}
	if strings.Contains(html, "__SPEC__") {
		t.Fatal("占位符未被替换")
	}
}

func TestMarkdownListsRoutes(t *testing.T) {
	reg := docRegistry(t)
	md := string(RoutesMarkdown(reg, Options{}))
	for _, want := range []string{"/account", "/toto_image_4/unity/{filename}", "接口路由表"} {
		if !strings.Contains(md, want) {
			t.Fatalf("routes.md 缺少 %q", want)
		}
	}
}

func specMap(t *testing.T, spec *huma.OpenAPI) map[string]any {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAliasesAndTypedResponse(t *testing.T) {
	paths, _ := specPaths(t)
	first := paths["/user_units/set_leader"].(map[string]any)["get"].(map[string]any)
	alias := paths["/user_units/set_leader/"].(map[string]any)["get"].(map[string]any)
	if first["operationId"] == alias["operationId"] {
		t.Fatal("尾斜杠别名 operationId 冲突")
	}
	a, _ := json.Marshal(first["parameters"])
	b, _ := json.Marshal(alias["parameters"])
	if string(a) != string(b) {
		t.Fatalf("别名参数不同: %s / %s", a, b)
	}
	raw, err := OpenAPIJSON(docRegistry(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"routes_total", "uptime_sec", "mercd_status", "mercd_code", "mercd_model_status"} {
		if !strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("Huma 没有生成实际字段 %s", field)
		}
	}
}

func TestUnknownHasNoSuccess(t *testing.T) {
	reg := httpapi.NewRegistry()
	reg.Handle(http.MethodGet, "/unknown", "未确认", nil)
	spec := Spec(reg, Options{})
	responses := spec.Paths["/unknown"].Get.Responses
	if responses["200"] != nil || responses["501"] == nil {
		t.Fatalf("未确认路由的 responses=%v", responses)
	}
}

func TestDocAssemblyRegistersGatedEndpoints(t *testing.T) {
	reg := docRegistry(t)
	for _, path := range []string{"/user_units/training", "/gachas/execute", "/user_decks/on", "/quests/execute/"} {
		route, ok := reg.Lookup(http.MethodPost, path)
		if !ok || !strings.HasPrefix(route.Note, "CN 3.5.0 ") {
			t.Fatalf("完整依赖文档未登记业务 handler: %s %+v", path, route)
		}
	}
}

func docRegistry(t *testing.T) *httpapi.Registry {
	t.Helper()
	reg, err := BuildRegistryCN35()
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestMissingDocAssetsFailExplicitly(t *testing.T) {
	if _, _, err := docAssets(t.TempDir()); err == nil {
		t.Fatal("缺失静态目录时不得静默生成残缺文档")
	}
}
