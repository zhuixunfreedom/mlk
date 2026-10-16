package appconf

import (
	"encoding/json"
	"strings"
	"testing"
)

// 保序是包的核心契约：客户端按 JSONObject 键序实例化 parts，
// map[string]any 的字母序重排会改变部件创建顺序。
func TestParseMarshalPreservesOrder(t *testing.T) {
	src := `{"z":1,"a":{"y":2,"b":3},"m":[4,{"k2":5,"k1":6}]}`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := string(doc.Marshal()); got != src {
		t.Fatalf("Marshal = %s，期望原样 %s", got, src)
	}
	if keys := doc.Obj.Keys(); strings.Join(keys, ",") != "z,a,m" {
		t.Fatalf("顶层键序 = %v", keys)
	}
}

// 数字保留原始字面量：scaleY:3.0 不能退化成 3。
func TestNumberRawLiteralPreserved(t *testing.T) {
	src := `{"a":3.0,"b":0.51,"c":-125,"d":1e2}`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := string(doc.Marshal()); got != src {
		t.Fatalf("Marshal = %s，期望 %s", got, src)
	}
}

// UTF-8 与 & 不转义：fixture 内含中文文案与 "name=X&method=Stack" 消息串。
func TestStringEscapingKeepsUTF8AndAmpersand(t *testing.T) {
	src := `{"msg":"name=CoinShop&method=Stack","t":"名字：旅人\n公会"}`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := string(doc.Marshal())
	if !strings.Contains(got, `"name=CoinShop&method=Stack"`) || !strings.Contains(got, "旅人") {
		t.Fatalf("Marshal = %s，期望保留 & 与 UTF-8 字面量", got)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("Marshal 输出非法 JSON: %s", got)
	}
}

func TestStripAnnotations(t *testing.T) {
	src := `{"_note":"x","parts":{"A":{"_label":"备注","x":1},"B":{"y":2}},"arr":[{"_k":1,"v":2}]}`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := string(StripAnnotations(doc).Marshal())
	want := `{"parts":{"A":{"x":1},"B":{"y":2}},"arr":[{"v":2}]}`
	if got != want {
		t.Fatalf("StripAnnotations = %s，期望 %s", got, want)
	}
}

func TestRenderWire(t *testing.T) {
	src := "{\n  \"version\": 3,\n  \"parts\": {\"A\": {\"_label\": \"x\", \"x\": 1}}\n}"
	wire, err := RenderWire([]byte(src))
	if err != nil {
		t.Fatalf("RenderWire: %v", err)
	}
	want := `{"version":3,"parts":{"A":{"x":1}}}`
	if string(wire) != want {
		t.Fatalf("RenderWire = %s，期望 %s", wire, want)
	}
}

func TestObjectSetGetDelete(t *testing.T) {
	obj := &Object{}
	obj.Set("a", Num("1"))
	obj.Set("b", Str("x"))
	obj.Set("a", Num("2")) // 原位替换
	if keys := obj.Keys(); strings.Join(keys, ",") != "a,b" {
		t.Fatalf("Set 后键序 = %v", keys)
	}
	if obj.Get("a").Raw != "2" {
		t.Fatalf("Get(a).Raw = %s", obj.Get("a").Raw)
	}
	if !obj.Delete("a") || obj.Delete("c") {
		t.Fatalf("Delete 语义错误")
	}
	if obj.Get("a") != nil {
		t.Fatalf("删除后 Get(a) 应为 nil")
	}
}

func TestMarshalIndentValidJSON(t *testing.T) {
	doc, err := Parse([]byte(`{"a":{"b":[1,{"c":"x"}]},"d":3.0}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pretty, err := doc.MarshalIndent()
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if !json.Valid(pretty) {
		t.Fatalf("MarshalIndent 非法 JSON:\n%s", pretty)
	}
	if !strings.Contains(string(pretty), "\n  \"a\": {") {
		t.Fatalf("MarshalIndent 未缩进:\n%s", pretty)
	}
}
