package cn35game

import (
	"math/rand"
	"testing"
	"time"
)

// fakeMessages 固定返回一批条目，用于断言 payload 形状。
type fakeMessages struct {
	recs []MessageRecord
	err  error
}

func (f fakeMessages) RandomMessages(int) ([]MessageRecord, error) { return f.recs, f.err }

func newTestRNG() *rand.Rand { return rand.New(rand.NewSource(1)) }

// oneRec 单条可用内容，供限流用例复用。
func oneRec() fakeMessages {
	return fakeMessages{recs: []MessageRecord{
		{ID: 1, Category: "user_comment", Body: "甲", Author: "A"},
	}}
}

// sampleCount 统计 payload 中 footer 三类别的总条数。
func sampleCount(payload map[string]any) int {
	n := 0
	for _, cat := range footerMessageCategories {
		if list, ok := payload[cat].([]any); ok {
			n += len(list)
		}
	}
	return n
}

// TestBuildMessagesPayloadNilStore：未装配内容池时全类别回空数组，
// footer 退回空框但不报错（装配前的既有行为必须保持）。
func TestBuildMessagesPayloadNilStore(t *testing.T) {
	got := BuildMessagesPayload(nil, time.Now(), newTestRNG(), 0)

	if len(got) != len(AllMessageCategories()) {
		t.Fatalf("类别数 = %d，期望 %d", len(got), len(AllMessageCategories()))
	}
	for _, cat := range AllMessageCategories() {
		list, ok := got[cat].([]any)
		if !ok {
			t.Fatalf("类别 %s 不是数组：%T", cat, got[cat])
		}
		if len(list) != 0 {
			t.Fatalf("类别 %s 期望空数组，得到 %d 条", cat, len(list))
		}
	}
}

// TestBuildMessagesPayloadEmptyPool：池为空（如未播种）不得 panic。
func TestBuildMessagesPayloadEmptyPool(t *testing.T) {
	got := BuildMessagesPayload(fakeMessages{}, time.Now(), newTestRNG(), 0)
	if n := sampleCount(got); n != 0 {
		t.Fatalf("空池下应回 0 条，得到 %d 条", n)
	}
}

// TestBuildMessagesPayloadFooterCategories：只有三个 footer 类别被填充，
// 非 footer 类别恒空（客户端永不显示，下发即无效数据）。
func TestBuildMessagesPayloadFooterCategories(t *testing.T) {
	store := fakeMessages{recs: []MessageRecord{
		{ID: 2, Category: "community_comment", Body: "乙", Author: "B"},
		// 非 footer 类别：即使内容池给出也必须被丢弃。
		{ID: 4, Category: "user_help", Body: "丁", Author: "D"},
		{ID: 5, Category: "battle_activity", Body: "戊", Author: "E"},
	}}
	got := BuildMessagesPayload(store, time.Now(), newTestRNG(), 0)

	if n := len(got["community_comment"].([]any)); n != 1 {
		t.Fatalf("community_comment = %d，期望 1", n)
	}
	for _, cat := range notFooteredCategories {
		if n := len(got[cat].([]any)); n != 0 {
			t.Fatalf("非 footer 类别 %s 应恒空，得到 %d 条", cat, n)
		}
	}
}

// TestRateLimitSuppressesWithinInterval 是本次修复的核心用例：
// 客户端每 10s 轮询一次，若每次都发新消息会压垮客户端（队列只增不减，
// 客户端 displayUpdateInterval 反向下探到 2s 下限）。故服务端必须按
// last_read_at 限流，间隔内一律不发。
func TestRateLimitSuppressesWithinInterval(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	// 刚读过（间隔内）→ 不发。
	for _, elapsed := range []int64{1, 10, 30, 59} {
		got := BuildMessagesPayload(oneRec(), now, newTestRNG(), now.Unix()-elapsed)
		if n := sampleCount(got); n != 0 {
			t.Fatalf("距上次已读 %ds（< %ds）应不发，却发了 %d 条",
				elapsed, MessageIntervalSec, n)
		}
	}

	// 达到或超过间隔 → 发 1 条。
	for _, elapsed := range []int64{60, 61, 120, 3600} {
		got := BuildMessagesPayload(oneRec(), now, newTestRNG(), now.Unix()-elapsed)
		if n := sampleCount(got); n != 1 {
			t.Fatalf("距上次已读 %ds（>= %ds）应发 1 条，发了 %d 条",
				elapsed, MessageIntervalSec, n)
		}
	}
}

// TestRateLimitFirstVisit：last_read_at 未知（0/负数）时立即给一条，
// 让玩家进页面就能看到内容。
func TestRateLimitFirstVisit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, v := range []int64{0, -1} {
		got := BuildMessagesPayload(oneRec(), now, newTestRNG(), v)
		if n := sampleCount(got); n != 1 {
			t.Fatalf("lastReadAt=%d 首次访问应发 1 条，发了 %d 条", v, n)
		}
	}
}

// TestRateLimitFutureClock：lastReadAt 落在未来（时钟回拨/坏值）时
// 应照常发放，而不是永久沉默 —— 一个坏值不能把功能整体卡死。
func TestRateLimitFutureClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	got := BuildMessagesPayload(oneRec(), now, newTestRNG(), now.Unix()+99999)
	if n := sampleCount(got); n != 1 {
		t.Fatalf("未来 lastReadAt 应发 1 条，发了 %d 条", n)
	}
}

// TestRateLimitNeverExceedsOnePerRequest：单次响应最多 1 条。
// 多条会把客户端队列推高，触发 displayUpdateInterval 下探。
func TestRateLimitNeverExceedsOnePerRequest(t *testing.T) {
	store := fakeMessages{recs: []MessageRecord{
		{ID: 1, Category: "user_comment", Body: "a", Author: "A"},
		{ID: 2, Category: "user_comment", Body: "b", Author: "B"},
		{ID: 3, Category: "guild_comment", Body: "c", Author: "C"},
		{ID: 4, Category: "community_comment", Body: "d", Author: "D"},
		{ID: 5, Category: "user_comment", Body: "e", Author: "E"},
		{ID: 6, Category: "user_comment", Body: "f", Author: "F"},
	}}
	now := time.Unix(1_700_000_000, 0)
	// 即使到了间隔、即使池中类别多样，单次也只发 1 条。
	got := BuildMessagesPayload(store, now, newTestRNG(), 0)
	if n := sampleCount(got); n != 1 {
		t.Fatalf("单次响应应最多 1 条，得到 %d 条", n)
	}
}

// TestQueueDrainExceedsInflow 断言限流后客户端队列会收敛（不再单调增长）。
//
// 客户端模型（MessageParts.UpdateMessageStack / DisplayUpdater）：
//
//	入队：每次轮询若拿到新 id 则入队
//	出队：displayUpdateInterval = max(-0.2857*n + 3.8571, 2.0) 秒一条
//
// 修复前每 10s 入 6 条（0.6 条/秒）> 最快出队 0.5 条/秒 ⇒ 队列无界增长。
// 修复后每 60s 入 1 条（≈0.0167 条/秒）< 最慢出队 1/3.8571≈0.259 条/秒。
// 本用例把该不等式固化为回归测试，防止有人再把单次条数调大。
func TestQueueDrainExceedsInflow(t *testing.T) {
	const pollSec = 10.0 // 客户端 message_request_interval_sec 缺省值
	perRequest := 1.0    // 限流后单次最多 1 条
	inflowPerSec := perRequest / pollSec

	// 客户端最慢出队速率出现在队列为空时（间隔最大）。
	slowestDrain := 1.0 / clientDisplayInterval(0)
	if inflowPerSec >= slowestDrain {
		t.Fatalf("入队 %.4f 条/秒 >= 最慢出队 %.4f 条/秒：队列会无界增长（客户端卡顿）",
			inflowPerSec, slowestDrain)
	}

	// 再断言：一条消息应在下一次推送前就被消费掉。
	if clientDisplayInterval(1) > MessageIntervalSec {
		t.Fatalf("单条消息显示耗时 %.2fs 超过推送间隔 %ds：队列会堆积",
			clientDisplayInterval(1), MessageIntervalSec)
	}
}

// clientDisplayInterval 复刻客户端 displayUpdateInterval 公式。
func clientDisplayInterval(queueCount float64) float64 {
	v := -0.2857*queueCount + 3.8571
	if v < 2.0 {
		return 2.0
	}
	return v
}

// TestMessageDocContract 断言传给客户端的字段名与类型。
// 键名真源为 ilspy-cs/Message.cs 构造器与 MessageParts.DisplayUpdater
// （TextLabel←text，RightLabel←user_name+NiceTime(created_at)）。
func TestMessageDocContract(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	doc := messageDoc(MessageRecord{ID: 42, Category: "user_comment", Body: "内容", Author: "某旅人"}, now, newTestRNG())

	for _, k := range []string{"id", "text", "user_name", "user_id", "created_at"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("缺少客户端必需键 %s", k)
		}
	}
	if doc["text"] != "内容" {
		t.Fatalf("text = %v", doc["text"])
	}
	if doc["user_name"] != "某旅人" {
		t.Fatalf("user_name = %v（RightLabel 署名）", doc["user_name"])
	}
	if doc["id"] != "srv-42" {
		t.Fatalf("id = %v，期望 srv-42（客户端按 id 去重）", doc["id"])
	}

	createdAt, ok := doc["created_at"].(int64)
	if !ok {
		t.Fatalf("created_at 类型 = %T，期望 int64（Message.cs 用 _doc.i）", doc["created_at"])
	}
	// 必须贴近当前时间：客户端以 lastReadAt = max(created_at) 回传，
	// 若偏离过大会让限流判据失效或显示成未来。
	if createdAt > now.Unix() {
		t.Fatalf("created_at=%d 晚于当前时间 %d：footer 会显示未来且破坏限流", createdAt, now.Unix())
	}
	if now.Unix()-createdAt > MessageIntervalSec {
		t.Fatalf("created_at 落后 %ds，超过一个推送间隔：last_read_at 会追不上 now",
			now.Unix()-createdAt)
	}

	// user_id 必须非本人，否则 IsMine() 为真会改变提示音与着色语义。
	if doc["user_id"] == "" {
		t.Fatal("user_id 为空：IsMine() 可能误判")
	}
}

// TestAllMessageCategoriesCoversClientSet：类别全集须与客户端消费的键集一致。
func TestAllMessageCategoriesCoversClientSet(t *testing.T) {
	want := []string{
		"user_help", "guild_help", "battle_activity", "community_help",
		"tmp_user_comment", "guild_comment", "community_comment", "user_comment",
	}
	got := map[string]bool{}
	for _, c := range AllMessageCategories() {
		got[c] = true
	}
	if len(got) != len(want) {
		t.Fatalf("类别数 = %d，期望 %d（%v）", len(got), len(want), AllMessageCategories())
	}
	for _, c := range want {
		if !got[c] {
			t.Fatalf("缺少客户端消费的类别 %s", c)
		}
	}
}
