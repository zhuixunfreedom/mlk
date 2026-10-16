-- cn35_quest_progress: 剧情关卡账号进度与通关标记（STORY350-03）。
-- 读面消费：mercd35 的 /stages、/areas、/quests 目录端点与
-- /users/preset_data.json 的 data.user 进度字段（stage_id/area_id/quest_id/
-- hard_mode_stage_id）。写面（通关推进、clear_count 递增）归 STORY350-07
-- 的战斗结算事务；本迁移只建表 + 确定性回填，业务代码不写这两张表。
--
-- 字段语义锚定客户端内嵌 Config/quest0 教程上下文 user
-- （artifacts/tasks/toto-cn-3.5.0-onboarding/run/c-assets-05/extracted/
-- quest0.json：stage_id=1、area_id=1、quest_id=1；无 hard_mode_stage_id
-- → 客户端 i() 读 0）。初始进度即"第一个 stage/area/quest 已解锁未通关"：
-- 关目录解锁规则为 id <= 账号进度指针（StageSelectListParts.cs:187-210
-- 客户端过滤同式），故新账号恒 (1,1,1,0)。
--
-- cn35_quest_clears 按 (user_id,quest_id,difficulty) 记通关次数，映射关卡
-- 文档的 normal_clear/hard_clear/very_hard_clear（客户端 int.Parse 硬消费，
-- 缺席即崩——读面必须对每关恒下发，无行=0）。difficulty 白名单对齐
-- use_ap_map 键集（impossible 难度客户端键存在但本批样本未启用，不入约束）。
--
-- Affected routes: /stages、/areas、/quests（STORY350-03 新增读面）、
-- /users/preset_data.json（data.user 注入）；
-- 写路径归属：/quests/execute|result 链（STORY350-06/07 落地）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制。

CREATE TABLE cn35_quest_progress (
    user_id TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    stage_id INTEGER NOT NULL CHECK (stage_id >= 1),
    area_id INTEGER NOT NULL CHECK (area_id >= 1),
    quest_id INTEGER NOT NULL CHECK (quest_id >= 1),
    hard_mode_stage_id INTEGER NOT NULL DEFAULT 0 CHECK (hard_mode_stage_id >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_quest_clears (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    quest_id INTEGER NOT NULL CHECK (quest_id > 0),
    difficulty TEXT NOT NULL CHECK (difficulty IN ('normal', 'hard', 'very_hard')),
    clear_count INTEGER NOT NULL CHECK (clear_count >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, quest_id, difficulty)
) STRICT;

-- 既有账号确定性回填：全部账号从未玩过剧情 → 初始进度 (1,1,1,0)，
-- 与 createAccount 建号同事务插入的种子值一致。
INSERT INTO cn35_quest_progress (user_id, stage_id, area_id, quest_id, hard_mode_stage_id, created_at, updated_at)
SELECT user_id, 1, 1, 1, 0, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM cn35_accounts;
