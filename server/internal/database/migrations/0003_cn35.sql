-- cn35: 国服 3.5.0 版本域账号/玩家/会话持久化（CN350-09）。
-- 版本隔离决策：cn35_* 独立表不复用 accounts/player_profiles——旧版触发器
-- accounts_player_core_init 会灌入 1.5 种子（leader=152/coin=3/gp=96343），
-- 3.5 需要按本包证据（内嵌 Config/quest0 教程上下文 user：level1、exp50/100、
-- max_ap20、max_bp300、gacha_point302；leader_unit_id=348 取 05 审计内嵌
-- unit_large_ns 约束）初始化，且不能让 3.5 初始化路径触碰 1.5 玩家行。
-- 两版本数据库文件物理分离（server-state-cn35/mercd35.sqlite3），本表集在
-- cn15 库中仅为空表共存，不改变旧库读写语义。
-- 会话持久化取 db-impact §4 强化版：S0/S1 随 cn35_sessions 落库，服务端重启
-- 后客户端无需重走 chooseServer 仍可解密；token 是唯一可复登凭据。
-- Affected routes: /account（登录方法组/chooseServer）、/users/preset_data.json、
-- 后续 users/update、verify_nickname（11 接线）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制。

CREATE TABLE cn35_accounts (
    he_account_id TEXT PRIMARY KEY CHECK (length(he_account_id) > 0),
    user_id TEXT NOT NULL UNIQUE CHECK (length(user_id) > 0),
    token TEXT NOT NULL,
    sk TEXT NOT NULL CHECK (length(sk) > 0),
    user_name TEXT NOT NULL DEFAULT '',
    account_name TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    sdk_account_id TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    is_new_user INTEGER NOT NULL CHECK (is_new_user IN (0, 1)),
    tutorial_finish INTEGER NOT NULL CHECK (tutorial_finish IN (0, 1)),
    last_server_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE UNIQUE INDEX cn35_accounts_device_key
    ON cn35_accounts(device_id) WHERE device_id <> '';
CREATE UNIQUE INDEX cn35_accounts_name_key
    ON cn35_accounts(account_name) WHERE account_name <> '';
CREATE UNIQUE INDEX cn35_accounts_sdk_key
    ON cn35_accounts(sdk_account_id) WHERE sdk_account_id <> '';
CREATE INDEX cn35_accounts_user_name_key
    ON cn35_accounts(user_name) WHERE user_name <> '';

CREATE TABLE cn35_player_profiles (
    user_id TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    level INTEGER NOT NULL CHECK (level >= 1),
    exp INTEGER NOT NULL CHECK (exp >= 0),
    max_exp INTEGER NOT NULL CHECK (max_exp > 0),
    ap INTEGER NOT NULL CHECK (ap >= 0),
    max_ap INTEGER NOT NULL CHECK (max_ap >= 0),
    ap_updated_at TEXT NOT NULL,
    bp INTEGER NOT NULL CHECK (bp >= 0),
    max_bp INTEGER NOT NULL CHECK (max_bp >= 0),
    leader_unit_id TEXT NOT NULL CHECK (length(leader_unit_id) > 0),
    login_bonus_receivable_sec INTEGER NOT NULL CHECK (login_bonus_receivable_sec >= 0),
    guild_name_snapshot TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (ap <= max_ap),
    CHECK (bp <= max_bp)
) STRICT;

CREATE TABLE cn35_player_balances (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    currency TEXT NOT NULL CHECK (length(currency) > 0),
    amount INTEGER NOT NULL CHECK (amount >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, currency)
) STRICT;

CREATE TABLE cn35_sessions (
    user_id TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    sk TEXT NOT NULL CHECK (length(sk) > 0),
    s0 TEXT NOT NULL CHECK (length(s0) = 32),
    s1 TEXT NOT NULL CHECK (length(s1) = 32),
    updated_at TEXT NOT NULL
) STRICT;
