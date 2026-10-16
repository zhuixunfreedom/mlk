package httpapi

import (
	"encoding/json"
	"net/http"
)

// diagnostic 是未确认 CN 路由返回给开发者的诊断体。
//
// 它不是游戏客户端认可的响应格式；字段命名刻意使用本地前缀，避免误用。
type diagnostic struct {
	Status  string `json:"mercd_status"`
	Code    int    `json:"mercd_code"`
	Route   string `json:"mercd_route"`
	Stage   string `json:"mercd_model_status"`
	Note    string `json:"mercd_note"`
	Blocked string `json:"mercd_blocked_by,omitempty"`
}

// Diagnostic 供文档库反射实际诊断字段，复用出包类型。
type Diagnostic = diagnostic

const (
	BlockProtocol = "CN 1.5.0 响应模型未确认"
)

// WriteJSON 写一个 JSON 响应。这是骨架期唯一允许的输出方式。
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteDiagnostic 写一个骨架期诊断响应。
func WriteDiagnostic(w http.ResponseWriter, code int, d diagnostic) {
	if d.Status == "" {
		d.Status = "not_implemented"
	}
	d.Code = code
	WriteJSON(w, code, d)
}
