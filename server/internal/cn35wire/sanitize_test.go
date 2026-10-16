// sanitize_test.go 客户端解析兼容清洗的单测。
package cn35wire

import (
	"strings"
	"testing"
)

func TestBalanceClientBrackets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"克劳德", "克劳德"},
		{"[时尚绅士]克劳德", "[时尚绅士]克劳德"},
		{"ID104[灼热弓手]伊妮娅 的哥哥", "ID104[灼热弓手]伊妮娅 的哥哥"},
		{"❤[飞空之翼", "❤[飞空之翼]"},
		{"㈱[侵攻の戦乙女", "㈱[侵攻の戦乙女]"},
		{"㊥❤[焰舞出尘", "㊥❤[焰舞出尘]"},
		{"❤[鸟笼的歌姫]]瑟蕾娜", "❤[鸟笼的歌姫]瑟蕾娜"},
		{"]] stray", " stray"},
		{"{a", "{a}"},
		{"a}", "a"},
		{"[a}b", "[ab]"},
		{"[[x]", "[[x]]"},
	}
	for _, c := range cases {
		if got := balanceClientBrackets(c.in); got != c.want {
			t.Errorf("balance(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestMarshalJSONSanitizesStrings(t *testing.T) {
	out, err := MarshalJSON(map[string]any{
		"data": map[string]any{
			"units": []any{
				map[string]any{"prefix_name": "❤[飞空之翼", "name": "❤[歌姫]]瑟蕾娜"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"❤[飞空之翼]"`) {
		t.Fatalf("未闭合括号未补全: %s", s)
	}
	if !strings.Contains(s, `"❤[歌姫]瑟蕾娜"`) {
		t.Fatalf("孤立闭括号未剔除: %s", s)
	}
	assertClientJSONDepthSafe(t, s)
}

// assertClientJSONDepthSafe 用客户端 JSONObject 解析器的同款计数规则
// 模拟全文解析：strings 内的 [ ] { } 也计入深度（无 flag 门控）。断言
// 解析结束时 num2==-1（根开括号不计入、收尾 } 回到 -1）且根对象
// keys/list 等长——即 GetField("data") 不会越界。
func assertClientJSONDepthSafe(t *testing.T, s string) {
	t.Helper()
	num2, flag, flag2 := 0, false, false
	keys, items := 0, 0
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if c == '"' {
			flag = !flag
		} else if c == '[' || c == '{' {
			num2++
		}
		if num2 == 0 && !flag {
			if c == ':' && !flag2 {
				flag2 = true
				keys++
			}
			if c == ',' {
				flag2 = false
				items++
			}
			if c == ']' || c == '}' {
				items++
			}
		}
		if c == ']' || c == '}' {
			num2--
		}
	}
	if num2 != -1 {
		t.Fatalf("客户端解析器深度未归零: num2=%d（不平衡字符串泄漏）", num2)
	}
	if keys != items {
		t.Fatalf("根对象 keys/list 失同步: keys=%d items=%d", keys, items)
	}
}
