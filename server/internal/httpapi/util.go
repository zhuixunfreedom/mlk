package httpapi

import "encoding/base64"

// encodeBase64 把二进制体转成取证日志里的可读形式。
// 用标准 Base64 而非 RawStd，便于与 Python 侧 captured_requests_*.json 直接比对。
func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
