// messages.go CN 3.5.0 底部消息条内容池读取面（CN35-UI-RESTORE）。
//
// 背景：MessageParts 的 DisplayUpdater 只有在 messagesQueue 非空时才写
// TextLabel/RightLabel。服务端长期只回空数组（见 tutorial.go 旧注释），
// 故 footer 一直是空框。本文件提供「从内容池随机抽样」的读面。
//
// 只回 user_comment/guild_comment/community_comment 三类 —— 这是客户端
// MessageParts.UpdateMessageStack 中 flag2=true（会进 footer 队列）的全部
// 类别；其余类别客户端只入桶、永不显示，回空数组。
//
// 幂等：随机抽样不落任何「已推送」状态。客户端 MessageParts 用 ids 集合按
// id 去重，同一条目重复出现不会重复入队，故无需服务端去重，属无损幂等。
package cn35game

import (
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// MessageRecord 内容池条目（migrations/0010_cn35_messages.sql）。
type MessageRecord struct {
	ID       int64
	Category string
	Body     string
	Author   string
}

// MessageStore 消息内容池读取面。nil 时读面回空类别（footer 保持空框）。
type MessageStore interface {
	// RandomMessages 按类别随机抽取至多 limit 条（仅 enabled 行）。
	// 池为空时返回空切片而非错误。
	RandomMessages(limit int) ([]MessageRecord, error)
}

// ErrNoMessageStore 未装配消息池时可选返回（当前实现回空列表，不报错）。
var ErrNoMessageStore = errors.New("cn35game: 未装配消息内容池")

// footerMessageCategories 客户端会显示在 footer 的三个类别。
// 依据 MessageParts.UpdateMessageStack：user_comment 恒 true；
// guild_comment/community_comment 由 show*BalloonMessages 门控（默认 true）。
var footerMessageCategories = []string{
	"user_comment", "guild_comment", "community_comment",
}

// notFooteredCategories 客户端只入桶、永不显示的类别。回空数组即合法。
var notFooteredCategories = []string{
	"user_help", "guild_help", "battle_activity",
	"community_help", "tmp_user_comment",
}

// AllMessageCategories 客户端消费的类别全集（footer + 非 footer），
// 保持与 tutorial.go 的历史键集一致。
func AllMessageCategories() []string {
	out := make([]string, 0, len(footerMessageCategories)+len(notFooteredCategories))
	out = append(out, footerMessageCategories...)
	out = append(out, notFooteredCategories...)
	return out
}

// MessageIntervalSec 相邻两条消息的最小间隔（用户要求：1 分钟推一条）。
//
// 为什么必须服务端限流 —— 客户端 MessageParts 的节奏控制会**反向加速**：
//
//	displayUpdateInterval = max(-0.2857*queueCount + 3.8571, 2.0)
//
// 队列越长显示越快，最快 2s/条 = 0.5 条/秒。而客户端每 10s 轮询一次
// （consts.message_request_interval_sec 缺省 10），若每次都给新 id，
// 入队速率 = N条/10s。N=6 时入 0.6 条/秒 > 出 0.5 条/秒 ⇒ 队列单调增长，
// 涨到 80 条上限后被截断，表现为底部消息疯滚 + 客户端卡顿（已实测复现）。
//
// 因此限流必须在**服务端**做：按经过时间发放，与轮询频率解耦。
// 客户端 ids 去重只保证「同一条不重复入队」，不能替代限流。
const MessageIntervalSec = 60

// MessagePerPush 单次响应最多下发的条数（恒为 1）。
//
// 客户端 displayUpdateInterval 随队列长度**反向下降**（见 MessageIntervalSec
// 注释），多给条目会直接把队列推高。限流期间每条消息都应在新一条到来前
// 被消费掉，故恒为 1。
const MessagePerPush = 1

// BuildMessagesPayload 按**经过时间**决定本次是否发放消息。
//
// lastReadAt 为客户端上报的上次已读时间戳（Unix 秒；0/负/未来值视为未知）。
// 只有距上次已读已过 MessageIntervalSec 才发 1 条；否则该类别回空数组。
// 这样无论客户端多频繁轮询，消息恒定约 1 条/分钟。
//
// store 为 nil 或读取失败时全部类别回空数组 —— footer 退回空框，
// 不因内容池故障阻断页面。
func BuildMessagesPayload(store MessageStore, now time.Time, rng *rand.Rand, lastReadAt int64) map[string]any {
	messages := make(map[string]any, len(AllMessageCategories()))
	for _, cat := range AllMessageCategories() {
		messages[cat] = []any{}
	}
	if store == nil {
		return messages
	}

	// 限流：未到间隔则本次不发。lastReadAt 为 0（首次）时立即发一条，
	// 让玩家进页面就能看到内容，随后转为 1 条/分钟。
	//
	// 时钟回拨/异常（lastReadAt 在未来）时按「已到间隔」处理而非永久沉默：
	// 宁可多发一条也不能因为一个坏值把功能整体卡死。
	if lastReadAt > 0 && lastReadAt <= now.Unix() {
		if now.Unix()-lastReadAt < MessageIntervalSec {
			return messages
		}
	}

	recs, err := store.RandomMessages(MessagePerPush)
	if err != nil || len(recs) == 0 {
		return messages
	}

	// 按类别分桶；未知类别丢弃（防御：内容池被手工塞入非法类别）。
	//
	// emitted 硬上限 MessagePerPush：RandomMessages 的 limit 只是**请求**
	// 约束，实现方（含测试替身）可能返回多于 limit 的条目。限流是防止
	// 客户端卡顿的关键不变量，故在领域层再兜一道，不依赖存储层守约。
	buckets := map[string][]any{}
	emitted := 0
	for _, r := range recs {
		if emitted >= MessagePerPush {
			break
		}
		if !isFooterCategory(r.Category) {
			continue
		}
		buckets[r.Category] = append(buckets[r.Category], messageDoc(r, now, rng))
		emitted++
	}
	for cat, list := range buckets {
		messages[cat] = list
	}
	return messages
}

func isFooterCategory(cat string) bool {
	for _, c := range footerMessageCategories {
		if c == cat {
			return true
		}
	}
	return false
}

// messageDoc 生成单条消息文档，字段名以客户端 Message 构造器读取的键为准
// （ilspy-cs/Message.cs）：id/text/user_name/user_id/created_at 为 footer
// 显示所必需；其余键给安全默认值。
//
// created_at 取「当前时间 − 一个很小的抖动」。两点考虑：
//  1. 客户端以 lastReadAt = max(created_at) 回传，下轮据此限流，故必须贴近
//     当前时间，不能用固定历史值 —— 否则 last_read_at 永远追不上 now，
//     限流判据失效。
//  2. 极小的负偏移让 NiceTime 显示成「约N秒前」而不是「刚刚」或未来时间。
//
// user_id 故意设为非本人 id：Message.IsMine() 为 false 时
// ShouldPlaySE() 才可能返回 true（有提示音），且不会把玩家自己的发言
// 当成自己的消息。
func messageDoc(r MessageRecord, now time.Time, rng *rand.Rand) map[string]any {
	jitter := 0
	if rng != nil {
		// 0..14 秒：仍在「刚刚/N秒前」区间，且不会晚于 now。
		jitter = rng.Intn(15)
	}
	createdAt := now.Unix() - int64(jitter)

	return map[string]any{
		"id":         fmt.Sprintf("srv-%d", r.ID),
		"text":       r.Body,
		"user_name":  r.Author,
		"user_id":    fmt.Sprintf("npc-%d", r.ID%1000),
		"created_at": createdAt,
		"small_text": "",
		"unit_id":    "",
		"help_id":    "",
		"raid_id":    "",
		"addition":   "",
		"stamp":      "",
		"private":    false,
	}
}
