-- 0007_cn35_quest_battle.sql STORY350-07：正式剧情战斗生命周期持久化。
--
-- 写权归属（接 0006 的边界声明）：
--   - cn35_battle_sessions   execute→ap_use→result/lose→continue 的
--     账号级战斗会话与幂等台账；AP 扣点、结算、续战均在此行上流转。
--   - cn35_battle_logs       /quests/log_battle 的 battle_record 归档
--     （客户端 fire-and-forget；纯审计，不参与结算）。
--   - cn35_player_flags      玩家一次性标志位（首个落地：quest_failed —
--     客户端 user.first_flag.failure → hasQuestFailedOnce，
--     DataCache.cs:229 / Quest.cs:IsTutorialFirstLose 消费）。
--
-- 状态机（status CHECK 白名单）：
--   prepared    execute 已建句柄、AP 未扣；
--   ap_paid     ap_use 已扣 AP（同会话再进 ap_use 幂等空转）；
--   cleared     result 已结算（奖励/通关/进度一次性入账）；
--   lost        lose_url 已上报；
--   continued   lost 后经 /quests/continue 扣钻续战，可再走 result/lose；
--   abandoned   同关同难度重新 execute 被顶替，或滞留超 TTL 回收。
--
-- 幂等键：battle_id 是 execute 签发的不可猜 token，写进三个下发 URL；
-- 全部状态迁移用条件 UPDATE（…AND status=…）天然拦截并发/重放。
CREATE TABLE cn35_battle_sessions (
    battle_id       TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    quest_id        INTEGER NOT NULL CHECK (quest_id >= 1),
    area_id         INTEGER NOT NULL CHECK (area_id >= 1),
    difficulty      TEXT NOT NULL CHECK (difficulty IN ('normal','hard','very_hard')),
    party_id        TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL CHECK (status IN ('prepared','ap_paid','cleared','lost','continued','abandoned')),
    -- execute 签发快照：结算只信快照，不信 result 请求参数。
    ap_cost         INTEGER NOT NULL CHECK (ap_cost >= 0),
    rewards_json    TEXT NOT NULL DEFAULT '[]',
    exp             INTEGER NOT NULL DEFAULT 0 CHECK (exp >= 0),
    next_stage_id   INTEGER NOT NULL CHECK (next_stage_id >= 1),
    next_area_id    INTEGER NOT NULL CHECK (next_area_id >= 1),
    next_quest_id   INTEGER NOT NULL CHECK (next_quest_id >= 1),
    continuable     INTEGER NOT NULL DEFAULT 1 CHECK (continuable IN (0,1)),
    continue_count  INTEGER NOT NULL DEFAULT 0 CHECK (continue_count >= 0),
    escaped         INTEGER NOT NULL DEFAULT 0 CHECK (escaped IN (0,1)),
    report_time     REAL,
    rest_mc         INTEGER,
    damage_json     TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    settled_at      TEXT
) STRICT;

-- 账号维度的会话检索（continue 取最近 lost；清理扫滞留）。
CREATE INDEX cn35_battle_sessions_user_status
    ON cn35_battle_sessions(user_id, status, updated_at);

-- /quests/log_battle 归档。record.battleId 客户端侧为关卡 id（SWFQuestClear.cs），
-- 与服务端 battle_id 不同名同形不混淆：两列分开存。
CREATE TABLE cn35_battle_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    battle_id   TEXT NOT NULL DEFAULT '',
    quest_id    INTEGER NOT NULL DEFAULT 0,
    record_json TEXT NOT NULL,
    created_at  TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_player_flags (
    user_id      TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    quest_failed INTEGER NOT NULL DEFAULT 0 CHECK (quest_failed IN (0,1)),
    updated_at   TEXT NOT NULL
) STRICT;
