-- cn35_gacha: CN3.5 抽卡持久化（保底进度 / 操作票据 / 逐抽记录 / 消费流水）。
--
-- 设计真源：notes/proposed/2026-09-25-cn35-gacha-restoration.md §5。
-- 事实等级：本表结构是**服务端重建**，不是原服 schema 证据。
--
-- 字段语义：
--   cn35_gacha_pity  每个 (账号, pity_group) 一行的保底进度。pity_group 由
--                    服务端规则决定（钻石常驻 / 限时UP / 无保底池不落行），
--                    稳定且不随 banner 更换；rules_version 记录写入时的规则
--                    版本，跨不兼容规则版本禁止静默迁移计数，需人工决定。
--                    n4 = 连续未得四星及以上次数；n5 = 连续未得五星次数；
--                    featured_guaranteed = UP 大保底态（下一个五星必为 UP）。
--   cn35_gacha_operations 一次抽卡操作。request_digest 是"账号+池+次数+币种+
--                    规则版本"的摘要，用于同键同摘要重放原结果、同键异参拒绝。
--                    结果快照 result_json 一经提交不可变，重试/丢响应时原样回放。
--                    status 首期只有 committed（未提交的操作不落行——整个抽取
--                    在同一事务内完成，失败即回滚，不存在"半个操作"）。
--   cn35_gacha_draws  操作内的逐抽记录，seq 保持**服务端原始抽取顺序**
--                    （结果页按稀有度重排，保底必须按原始序列计算）。
--                    reason 记录触发原因（normal/软保底/硬保底/四星保证/UP/歪）。
--   cn35_gacha_ledger 货币流水，与余额变动同事务写入，供审计与对账。
--                    余额本身仍在 cn35_player_balances，本表只记增量与缘由。
--
-- 幂等说明（重要，勿误读）：客户端请求无法携带服务端可识别的唯一操作号
-- （已取证：URL 硬编码拼接，无 nonce/随机/时间戳进入业务请求）。
-- operation_key 由服务端从"账号+池+次数+币种+规则版本+调用点"派生，
-- 因此它只能识别**同参数重放**，不能区分"网络重试"与"玩家合法再抽同参数"。
-- 这就是抽卡执行入口默认关闭的原因；本表结构支持在该限制下安全回放，
-- 但**不声称**实现了严格幂等。
--
-- Affected routes: /gachas/execute、/users/reload_data?on=gachas_execute、
--   抽卡记录/保底查询读面。
-- Recovery: 停服 → 用新的 state 目录从一致性备份恢复 → 配对旧二进制；
--   本迁移只新增空表，不回填、不改动既有余额与库存，不按现有持卡量反推历史。
--   失败回退时保留问题库，不得就地删除。

CREATE TABLE cn35_gacha_pity (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    pity_group TEXT NOT NULL CHECK (length(pity_group) > 0),
    n4 INTEGER NOT NULL DEFAULT 0 CHECK (n4 >= 0),
    n5 INTEGER NOT NULL DEFAULT 0 CHECK (n5 >= 0),
    featured_guaranteed INTEGER NOT NULL DEFAULT 0 CHECK (featured_guaranteed IN (0, 1)),
    rules_version TEXT NOT NULL CHECK (length(rules_version) > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, pity_group)
) STRICT;

CREATE TABLE cn35_gacha_operations (
    operation_id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    operation_key TEXT NOT NULL CHECK (length(operation_key) > 0),
    request_digest TEXT NOT NULL CHECK (length(request_digest) > 0),
    pool_id TEXT NOT NULL CHECK (length(pool_id) > 0),
    pity_group TEXT NOT NULL DEFAULT '',
    rules_version TEXT NOT NULL CHECK (length(rules_version) > 0),
    draw_count INTEGER NOT NULL CHECK (draw_count >= 1),
    currency TEXT NOT NULL CHECK (length(currency) > 0),
    unit_cost INTEGER NOT NULL CHECK (unit_cost >= 0),
    total_cost INTEGER NOT NULL CHECK (total_cost >= 0),
    status TEXT NOT NULL CHECK (status IN ('committed')),
    -- 结果快照：重试/丢响应时原样回放，不重新随机、不重新结算。
    result_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    -- 同账号下操作键唯一：同键同摘要回放原结果，同键异摘要拒绝。
    UNIQUE (user_id, operation_key)
) STRICT;

CREATE INDEX cn35_gacha_operations_recent
    ON cn35_gacha_operations(user_id, operation_id DESC);

CREATE TABLE cn35_gacha_draws (
    operation_id INTEGER NOT NULL
        REFERENCES cn35_gacha_operations(operation_id) ON DELETE RESTRICT,
    seq INTEGER NOT NULL CHECK (seq >= 1),
    unit_id INTEGER NOT NULL CHECK (unit_id > 0),
    rarity_id INTEGER NOT NULL CHECK (rarity_id > 0),
    reason TEXT NOT NULL CHECK (length(reason) > 0),
    -- 该抽是否首次获得（新人）与觉醒增量，用于重复卡审计。
    newcomer INTEGER NOT NULL CHECK (newcomer IN (0, 1)),
    duplicated_delta INTEGER NOT NULL DEFAULT 0 CHECK (duplicated_delta >= 0),
    -- 保底计数快照（本抽**之后**的值），供复现与公示核对。
    n4_after INTEGER NOT NULL CHECK (n4_after >= 0),
    n5_after INTEGER NOT NULL CHECK (n5_after >= 0),
    compensated_point INTEGER NOT NULL DEFAULT 0 CHECK (compensated_point >= 0),
    created_at TEXT NOT NULL,
    PRIMARY KEY (operation_id, seq)
) STRICT;

CREATE TABLE cn35_gacha_ledger (
    ledger_id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    operation_id INTEGER REFERENCES cn35_gacha_operations(operation_id) ON DELETE RESTRICT,
    seq INTEGER NOT NULL CHECK (seq >= 0),
    currency TEXT NOT NULL CHECK (length(currency) > 0),
    -- delta 为负表示消费，为正表示补偿入账。
    delta INTEGER NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) > 0),
    balance_after INTEGER NOT NULL CHECK (balance_after >= 0),
    created_at TEXT NOT NULL,
    UNIQUE (user_id, operation_id, seq)
) STRICT;

CREATE INDEX cn35_gacha_ledger_recent
    ON cn35_gacha_ledger(user_id, ledger_id DESC);
