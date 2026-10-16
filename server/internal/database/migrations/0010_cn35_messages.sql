-- cn35_messages: 底部消息条（MessageParts footer）内容池（CN35-UI-RESTORE）。
--
-- 契约依据（客户端消费侧，ilspy-cs 反编译）：
--   MessageParts.UpdateMessageStack 遍历 data.messages 的类别键，逐条构造
--   Message(text, doc)；只有 flag2=true 的类别会进 messagesQueue：
--     user_comment      → flag2 恒 true（唯一无门控类别）
--     guild_comment     → showGuildBalloonMessages（默认 true）
--     community_comment → showCommunityBalloonMessages（默认 true）
--   其余（user_help/guild_help/battle_activity/community_help/tmp_user_comment）
--   只进 messages 桶，**永不显示在 footer**。
--   DisplayUpdater 出队后写：
--     TextLabel  ← text（ArrangeMessageText 截 27 字）
--     RightLabel ← [<ColorTag>]<user_name> <NiceTime(now-created_at)>[-]
--   Message 构造器读：id/text/user_name/user_id/created_at/small_text/
--   unit_id/help_id/raid_id/addition/private/stamp/team_id/entry_url；
--   IsMine() 比较 user_id 与 cache.user.id。
--
-- 为什么入库而非硬编码：用户要求「AI 生成近似的游戏讨论（1000 条入库），
-- 随机选取推送到客户端」。池子需可增补/停用而无需改代码，且条目本身是
-- 内容数据而非逻辑常量。
--
-- 设计：
--   * body 为消息正文；author_name 为署名（RightLabel 显示）。
--   * created_at 存 Unix 秒。**注意**：客户端 RightLabel 显示
--     NiceTime(now - created_at)，若存固定历史时间戳，所有条目都会显示成
--     「约N天前」的陈旧值。故读面按「距当前服务器时间随机偏移」动态生成
--     created_at（见 messages.go MessagesForUser），本列仅作回退基准。
--   * enabled 支持停用单条而不删行（内容池治理）。
--   * 不使用 AUTOINCREMENT：条目由种子按内容哈希去重，id 稳定。
--
-- 随机推送语义与幂等：读面每次请求随机抽样若干条返回；客户端按 id 去重
-- （MessageParts 的 ids.Contains 集合），故同一 id 重复出现不会重复入队，
-- 无需在服务端记录「已推送」状态 —— 这是无损幂等，不引入写面。
--
-- Affected routes: /users/messages（tutorial.go usersMessagesHandler）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移只
-- 新增空表，不回填、不触碰既有账号数据，旧二进制读不到该表亦不受影响
-- （读面在表缺失时按空池处理并记 warn）。

CREATE TABLE cn35_messages (
    id INTEGER PRIMARY KEY CHECK (id > 0),
    category TEXT NOT NULL
        CHECK (category IN ('user_comment', 'guild_comment', 'community_comment')),
    body TEXT NOT NULL CHECK (length(body) > 0),
    author_name TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL DEFAULT 0 CHECK (created_at >= 0)
) STRICT;

-- 读面按类别随机抽样，仅取 enabled 行。
CREATE INDEX cn35_messages_category_enabled
    ON cn35_messages(category, enabled);
