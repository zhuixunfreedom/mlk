package cn35store

import (
	"path/filepath"
	"testing"
)

// TestMessagesSeedApplied：0011 播种后内容池应有 1000 条启用条目，
// 且全部属于客户端会显示在 footer 的三个类别。
func TestMessagesSeedApplied(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var total int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_messages WHERE enabled = 1`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1000 {
		t.Fatalf("种子条目 = %d，期望 1000", total)
	}

	// 内容池不得含客户端永不显示的类别（下发即无效数据）。
	var bad int
	if err := s.DB().QueryRow(`
		SELECT count(*) FROM cn35_messages
		 WHERE category NOT IN ('user_comment','guild_comment','community_comment')`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("含不可显示类别 %d 条", bad)
	}

	// user_comment 是 footer 唯一无门控类别，必须有足量内容。
	var uc int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_messages WHERE category='user_comment'`).Scan(&uc); err != nil {
		t.Fatal(err)
	}
	if uc == 0 {
		t.Fatal("user_comment 为 0")
	}
}

// TestRandomMessagesShape：抽样返回至多 limit 条、字段非空、且不越界。
func TestRandomMessagesShape(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	recs, err := s.RandomMessages(6)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 || len(recs) > 6 {
		t.Fatalf("抽样 %d 条，期望 1..6", len(recs))
	}
	ids := map[int64]bool{}
	for _, r := range recs {
		if r.ID == 0 || r.Body == "" {
			t.Fatalf("条目字段缺失: %+v", r)
		}
		if ids[r.ID] {
			t.Fatalf("同一次抽样出现重复 id %d", r.ID)
		}
		ids[r.ID] = true
		switch r.Category {
		case "user_comment", "guild_comment", "community_comment":
		default:
			t.Fatalf("抽样含不可显示类别 %q", r.Category)
		}
	}
}

// TestRandomMessagesZeroLimit：limit<=0 直接回空，不查库。
func TestRandomMessagesZeroLimit(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, n := range []int{0, -1} {
		recs, err := s.RandomMessages(n)
		if err != nil {
			t.Fatalf("limit=%d: %v", n, err)
		}
		if len(recs) != 0 {
			t.Fatalf("limit=%d 期望空，得到 %d 条", n, len(recs))
		}
	}
}

// TestRandomMessagesVaries：多次抽样应出现不同组合（随机性生效）。
func TestRandomMessagesVaries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	seen := map[int64]bool{}
	for i := 0; i < 20; i++ {
		recs, err := s.RandomMessages(6)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			seen[r.ID] = true
		}
	}
	// 120 次抽取中若只见到 <=6 个不同 id，说明 ORDER BY RANDOM() 未生效。
	if len(seen) <= 6 {
		t.Fatalf("抽取结果不随机：20 轮仅 %d 个不同条目", len(seen))
	}
}
