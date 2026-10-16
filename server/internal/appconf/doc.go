// Package appconf 承载 serve_app_conf_android.json 的保序文档模型与
// 下发映射管线。
//
// 为什么不用 map[string]any：客户端 PageBase.SetOption 按 JSONObject 键序
// 实例化 pages.<type>.parts（docs/架构解析/05 §4.1），Go map 编码会按字母序
// 重排键、改变部件创建顺序。本包的对象保留成员顺序，数字保留原始字面量
// （scaleY: 3.0 不会退化成 3），供工作台做外科手术式编辑后原样落盘。
//
// 下发管线（RenderWire）：源文件是带缩进的可读 fixture → 解析成文档树 →
// 剥离编辑器注解（任何以 "_" 开头的对象成员，如部件上的 "_label"）→
// 紧凑序列化返回客户端。文件不再是线路字节的真源，文档树才是。
package appconf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Kind 区分 JSON 值类型。
type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value 是保序 JSON 文档树的节点。数字以原始字面量保存（Raw），
// 避免 3.0/3e2 这类写法在往返中被归一化。
type Value struct {
	Kind Kind
	Bool bool
	Raw  string   // KindNumber 的原始字面量
	Str  string   // KindString
	Arr  []*Value // KindArray
	Obj  *Object  // KindObject
}

// Pair 是对象的一个有序成员。
type Pair struct {
	Key string
	Val *Value
}

// Object 是有序成员集合。Get/Set 保持既有位置，新键追加在末尾。
type Object struct {
	Pairs []Pair
}

// 构造辅助。
func Null() *Value               { return &Value{Kind: KindNull} }
func Bool(b bool) *Value         { return &Value{Kind: KindBool, Bool: b} }
func Str(s string) *Value        { return &Value{Kind: KindString, Str: s} }
func Num(raw string) *Value      { return &Value{Kind: KindNumber, Raw: raw} }
func Arr(items ...*Value) *Value { return &Value{Kind: KindArray, Arr: items} }
func Obj(pairs ...Pair) *Value   { return &Value{Kind: KindObject, Obj: &Object{Pairs: pairs}} }

// Parse 把 JSON 文本解析为保序文档树。
func Parse(data []byte) (*Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("appconf: 文档尾部存在多余内容")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (*Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := &Object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("appconf: 对象键不是字符串: %v", kt)
				}
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				obj.Pairs = append(obj.Pairs, Pair{Key: key, Val: val})
			}
			if _, err := dec.Token(); err != nil { // 消费 '}'
				return nil, err
			}
			return &Value{Kind: KindObject, Obj: obj}, nil
		case '[':
			arr := &Value{Kind: KindArray}
			for dec.More() {
				item, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr.Arr = append(arr.Arr, item)
			}
			if _, err := dec.Token(); err != nil { // 消费 ']'
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("appconf: 非预期分隔符 %q", t)
	case nil:
		return Null(), nil
	case bool:
		return Bool(t), nil
	case string:
		return Str(t), nil
	case json.Number:
		return Num(t.String()), nil
	case float64:
		return Num(fmt.Sprint(t)), nil
	}
	return nil, fmt.Errorf("appconf: 非预期 token %v", tok)
}

// Get 按键取成员，未命中返回 nil。
func (o *Object) Get(key string) *Value {
	for _, p := range o.Pairs {
		if p.Key == key {
			return p.Val
		}
	}
	return nil
}

// Set 替换既有成员（保持位置）或追加新成员。
func (o *Object) Set(key string, val *Value) {
	for i := range o.Pairs {
		if o.Pairs[i].Key == key {
			o.Pairs[i].Val = val
			return
		}
	}
	o.Pairs = append(o.Pairs, Pair{Key: key, Val: val})
}

// Delete 移除成员并报告是否命中。
func (o *Object) Delete(key string) bool {
	for i, p := range o.Pairs {
		if p.Key == key {
			o.Pairs = append(o.Pairs[:i], o.Pairs[i+1:]...)
			return true
		}
	}
	return false
}

// Keys 返回成员键的有序副本。
func (o *Object) Keys() []string {
	keys := make([]string, len(o.Pairs))
	for i, p := range o.Pairs {
		keys[i] = p.Key
	}
	return keys
}

// Marshal 输出紧凑 JSON（下发形态）。
func (v *Value) Marshal() []byte {
	var buf bytes.Buffer
	v.appendJSON(&buf)
	return buf.Bytes()
}

// MarshalJSON 让 *Value 可直接作为 encoding/json 响应字段：
// 序列化结果是该子树的紧凑原文，不是 Go 结构体投影。
func (v *Value) MarshalJSON() ([]byte, error) { return v.Marshal(), nil }

// MarshalIndent 输出两空格缩进的可读 JSON（落盘形态）。
func (v *Value) MarshalIndent() ([]byte, error) {
	compact := v.Marshal()
	var buf bytes.Buffer
	if err := json.Indent(&buf, compact, "", "  "); err != nil {
		return nil, err
	}
	return append(buf.Bytes(), '\n'), nil
}

func (v *Value) appendJSON(buf *bytes.Buffer) {
	switch v.Kind {
	case KindNull:
		buf.WriteString("null")
	case KindBool:
		if v.Bool {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case KindNumber:
		if v.Raw == "" {
			buf.WriteString("0")
		} else {
			buf.WriteString(v.Raw)
		}
	case KindString:
		appendJSONString(buf, v.Str)
	case KindArray:
		buf.WriteByte('[')
		for i, item := range v.Arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			item.appendJSON(buf)
		}
		buf.WriteByte(']')
	case KindObject:
		buf.WriteByte('{')
		for i, p := range v.Obj.Pairs {
			if i > 0 {
				buf.WriteByte(',')
			}
			appendJSONString(buf, p.Key)
			buf.WriteByte(':')
			p.Val.appendJSON(buf)
		}
		buf.WriteByte('}')
	}
}

// appendJSONString 写 JSON 字符串：只转义引号、反斜杠与控制字符，
// 不做 HTML 转义（保留 & < > 原样）也不转义非 ASCII——与包内既有
// fixture 的 UTF-8 字面量形态一致。
func appendJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			buf.WriteByte('\\')
			buf.WriteRune(r)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// AnnotationPrefix 标记编辑器注解键：下发前被 RenderWire 剥离，
// 允许工作台把展示元数据（如部件备注）写进 fixture 而不泄漏给客户端。
const AnnotationPrefix = "_"

// StripAnnotations 递归剥离所有以 "_" 开头的对象成员，返回新树。
func StripAnnotations(v *Value) *Value {
	switch v.Kind {
	case KindArray:
		out := &Value{Kind: KindArray, Arr: make([]*Value, len(v.Arr))}
		for i, item := range v.Arr {
			out.Arr[i] = StripAnnotations(item)
		}
		return out
	case KindObject:
		obj := &Object{}
		for _, p := range v.Obj.Pairs {
			if strings.HasPrefix(p.Key, AnnotationPrefix) {
				continue
			}
			obj.Pairs = append(obj.Pairs, Pair{Key: p.Key, Val: StripAnnotations(p.Val)})
		}
		return &Value{Kind: KindObject, Obj: obj}
	default:
		return v
	}
}

// RenderWire 是 app_conf 的下发映射：解析源 JSON → 剥离注解 → 紧凑输出。
// 源文件缩进/换行只是编辑形态，线路字节由此函数唯一生成。
func RenderWire(src []byte) ([]byte, error) {
	doc, err := Parse(src)
	if err != nil {
		return nil, fmt.Errorf("appconf 源 JSON 解析失败: %w", err)
	}
	return StripAnnotations(doc).Marshal(), nil
}
