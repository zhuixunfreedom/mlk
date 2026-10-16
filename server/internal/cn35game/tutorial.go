// tutorial.go CN 3.5.0 教程写入面（契约 12/13 + Top 轮询）。
//
// 必经性依据（run/c-analysis-02/endpoints.csv E017/E018 + steps.csv S009/S013）：
//   - /users/update：Story1 提交 user_name（客户端仅日志消费）；
//     End 提交 tutorial_finish=true+user_name，须回 status:"success" 字面值，
//     否则客户端弹窗重试循环。
//   - /users/verify_nickname：Story1 InputName 提交前查重，{result:bool}。
//   - /users/messages：Top 页 MessageParts 常驻轮询（实机 08 已观测到 501）。
//     原实现下发空类别字典；CN35-UI-RESTORE 起改为从 cn35_messages 内容池
//     随机抽样（见 messages.go），使底部 footer 显示文字。
//
// 幂等语义：两个写都是状态赋值非增量——重复提交同值天然幂等；
// tutorial_finish 重复置位算成功（中断恢复判据）。
// 排除面见 handoffs/10/tutorial-domain.md：quest0 本地战、task_v2 跳 Gacha1、
// 正式 quests/gacha 端点不属教程必经。
package cn35game

import (
	"errors"
	"math/rand"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// usersUpdateHandler POST /users/update：昵称/教程完成写（契约 12）。
// End 步一次请求同时携带 user_name+tutorial_finish——UsersUpdate 单事务
// 落地两项，昵称被他人占用或账号不存在时整笔回滚并回 status:"error"，
// 客户端弹窗重试语义下可显示；成功恒回 status:"success" 字面值。
func usersUpdateHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		name := req.Form.Get("user_name")
		finish := req.Form.Get("tutorial_finish")
		finishOn := finish == "true" || finish == "1" || finish == "True"
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "users_update",
			"has_name": name != "", "tutorial_finish": finishOn,
		}
		if err := dep.Accounts.UsersUpdate(req.UserID, name, finishOn); err != nil {
			if errors.Is(err, ErrNameTaken) || errors.Is(err, ErrNoAccount) {
				return cn35wire.MarshalJSON(map[string]any{
					"status": "error", "msg": err.Error(),
				})
			}
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"status": "success"})
	})
}

// verifyNicknameHandler POST /users/verify_nickname：{result:bool}（契约 13）。
// 本人已持有同名视为可用，保证"update 成功但响应丢失"后的重试不被自己卡住。
func verifyNicknameHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		name := req.Form.Get("user_name")
		ok := name != "" && dep.Accounts.NicknameAvailable(req.UserID, name)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "verify_nickname",
			"result": ok,
		}
		return cn35wire.MarshalJSON(map[string]any{"result": ok})
	})
}

// usersMessagesHandler /users/messages：Top 常驻轮询的消息面。
//
// CN35-UI-RESTORE：由「恒回空类别」改为「从内容池随机抽样」。
// 客户端 MessageParts.DisplayUpdater 仅在 messagesQueue 非空时写
// TextLabel/RightLabel，故空池必然表现为底部空框。
//
// data.messages 按客户端类别键集组织；只有 user_comment/guild_comment/
// community_comment 会进 footer 队列（MessageParts.UpdateMessageStack 的
// flag2 分支），其余类别维持空数组。temp_user_id 仍附空评论列表。
func usersMessagesHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		// last_read_at 是客户端上报的上次已读时间戳，也是服务端限流的唯一
		// 依据（messages.go MessageIntervalSec：恒定约 1 条/分钟）。
		// 解析失败按 0（未知）处理 —— 首次访问立即给一条。
		lastReadAt, _ := strconv.ParseInt(req.Query.Get("last_read_at"), 10, 64)

		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		now := time.Now()
		messages := BuildMessagesPayload(dep.Messages, now, rng, lastReadAt)

		sampled := 0
		for _, cat := range footerMessageCategories {
			if list, ok := messages[cat].([]any); ok {
				sampled += len(list)
			}
		}

		data := map[string]any{"messages": messages}
		if req.Query.Get("temp_user_id") != "" {
			data["temp_user_comments"] = []any{}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "users_messages",
			"last_read_at":   lastReadAt,
			"footer_samples": sampled,
			"interval_sec":   MessageIntervalSec,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}
