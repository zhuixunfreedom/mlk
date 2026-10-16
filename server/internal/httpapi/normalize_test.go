package httpapi

import "testing"

// 可选路径前缀归一化保持路由查找可测试；CN 客户端默认不带此前缀。
func TestNormalizeAPIPath(t *testing.T) {
	cases := []struct {
		in       string
		wantPath string
		wantPFX  string
	}{
		{"/api123/account", "/account", "/api123"},
		{"/api123/users/preset_data.json", "/users/preset_data.json", "/api123"},
		{"/api2/account", "/account", "/api2"},
		{"/api123", "/", "/api123"},
		{"/api123/", "/", "/api123"},
		// 不匹配的形态必须原样返回，避免误剥：
		{"/account", "/account", ""},
		{"/apiabc/users/show", "/apiabc/users/show", ""},
		{"/api/users/show", "/api/users/show", ""},
		{"/api1234users/show", "/api1234users/show", ""},
		{"/", "/", ""},
		{"/mercd/health", "/mercd/health", ""},
	}
	for _, tc := range cases {
		gotPath, gotPFX := NormalizeAPIPath(tc.in)
		if gotPath != tc.wantPath || gotPFX != tc.wantPFX {
			t.Errorf("NormalizeAPIPath(%q) = (%q, %q)，期望 (%q, %q)",
				tc.in, gotPath, gotPFX, tc.wantPath, tc.wantPFX)
		}
	}
}

// 归一化后的路径必须能命中已登记路由——这正是本次修复的目的。
func TestNormalizedPathResolvesToRegisteredRoute(t *testing.T) {
	reg := NewRegistry()
	reg.HandleWithStatus("POST", "/account", "CN 启动链账号路由", ModelUnknown, nil)

	norm, pfx := NormalizeAPIPath("/api123/account")
	if pfx != "/api123" {
		t.Fatalf("前缀未剥离: %q", pfx)
	}
	rt, ok := reg.Lookup("POST", norm)
	if !ok {
		t.Fatalf("归一化路径 %q 未命中已登记路由", norm)
	}
	if rt.Path != "/account" {
		t.Errorf("命中错误路由: %q", rt.Path)
	}
	// 反面：不归一化就必然落空（这就是修复前的行为）。
	if _, ok := reg.Lookup("POST", "/api123/account"); ok {
		t.Error("未归一化路径不应命中路由——若命中，说明测试前提已变")
	}
}
