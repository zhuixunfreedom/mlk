// sanitize.go CN3.5 下发字符串的客户端解析兼容清洗。
//
// 客户端 JSONObject 解析器（ilspy-fp JSONObject.cs:180-253）用 num2 计数
// { } [ ] 深度，但该计数**不受字符串态（flag）门控**——字符串值内的
// 方/花括号同样增减深度。一旦某字符串净不平衡（如 prefix_name 的
// "❤[飞空之翼" 缺右括号，gacha350 实机 /gachas Now-Loading 事故），
// 顶层收尾的 ,/} 全部被跳过 → 根对象 keys/list 失同步 →
// Document.GetField("data") ArgumentOutOfRange → 页面永久 Now Loading。
//
// 因此所有经 MarshalJSON 下发的字符串值在此统一配平：孤立的闭括号丢弃、
// 未闭合的开括号在串尾补对端；本就平衡的文本零改动（含 description 中
// "ID104[灼热弓手]伊妮娅" 这类成对引用，属合法内容）。
package cn35wire

import "strings"

// sanitizeClientStrings 递归清洗 JSON 值树中的字符串。map 键是协议字段名
// （客户端按键名查 Document），只清洗值；键若被改写会破坏契约。
func sanitizeClientStrings(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = sanitizeClientStrings(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitizeClientStrings(val)
		}
		return out
	case string:
		return balanceClientBrackets(t)
	default:
		return v
	}
}

// balanceClientBrackets 配平字符串内的 [ ] 与 { }（两组各自独立配对）。
// 规则：闭括号仅在栈顶开括号同型时配对写出，否则丢弃；串尾按栈序反向
// 补闭括号。按字节遍历是安全的——目标字符均为 ASCII，多字节 UTF-8 的
// 续字节不可能等于它们。
//
// 例：
//
//	"❤[飞空之翼"     → "❤[飞空之翼]"   （未闭合补全）
//	"❤[歌姫]]瑟蕾娜" → "❤[歌姫]瑟蕾娜" （孤立闭括号丢弃）
//	"ID104[x]y"     → 原样            （已平衡）
func balanceClientBrackets(s string) string {
	if !strings.ContainsAny(s, "[]{}") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	var st []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '[', '{':
			st = append(st, c)
			b.WriteByte(c)
		case ']', '}':
			open := byte('[')
			if c == '}' {
				open = '{'
			}
			if len(st) > 0 && st[len(st)-1] == open {
				st = st[:len(st)-1]
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	for i := len(st) - 1; i >= 0; i-- {
		if st[i] == '[' {
			b.WriteByte(']')
		} else {
			b.WriteByte('}')
		}
	}
	return b.String()
}
