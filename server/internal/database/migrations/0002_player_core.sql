-- player_core: player profile/balances truth source for data.user / data.user_coin.
-- Phase 2 (docs/后端数据库与Go工作流.md): serve_preset_data.json loses its static
-- player sections; every response that carries user/user_coin reads these tables.
-- Affected routes: /users/preset_data.json, /users/show, /users/reload_data,
-- /user_units/set_leader plus all data.user/data.user_coin mirror endpoints.
-- Backfill seeds the approved fixture values for accounts that already exist at
-- 0001; the AFTER INSERT trigger gives every later account the same initial
-- profile and balances inside the account-creating transaction. Recovery: stop
-- the service, restore the pre-0002 backup into a new state dir, pair old binary.

CREATE TABLE player_profiles (
    user_id TEXT PRIMARY KEY REFERENCES accounts(user_id) ON DELETE RESTRICT,
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

CREATE TABLE player_balances (
    user_id TEXT NOT NULL REFERENCES accounts(user_id) ON DELETE RESTRICT,
    currency TEXT NOT NULL CHECK (length(currency) > 0),
    amount INTEGER NOT NULL CHECK (amount >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, currency)
) STRICT;

INSERT INTO player_profiles (user_id, level, exp, max_exp, ap, max_ap, ap_updated_at, bp, max_bp, leader_unit_id, login_bonus_receivable_sec, guild_name_snapshot, created_at, updated_at)
SELECT user_id, 1, 5, 100, 30, 30, strftime('%Y-%m-%dT%H:%M:%fZ','now'), 600, 600, '152', 0, '小溪的大宇宙', strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now')
FROM accounts;

INSERT INTO player_balances (user_id, currency, amount, updated_at)
SELECT user_id, 'coin', 3, strftime('%Y-%m-%dT%H:%M:%fZ','now') FROM accounts;

INSERT INTO player_balances (user_id, currency, amount, updated_at)
SELECT user_id, 'gacha_point', 96343, strftime('%Y-%m-%dT%H:%M:%fZ','now') FROM accounts;

CREATE TRIGGER accounts_player_core_init
AFTER INSERT ON accounts
FOR EACH ROW
BEGIN
    INSERT INTO player_profiles (user_id, level, exp, max_exp, ap, max_ap, ap_updated_at, bp, max_bp, leader_unit_id, login_bonus_receivable_sec, guild_name_snapshot, created_at, updated_at)
    VALUES (NEW.user_id, 1, 5, 100, 30, 30, strftime('%Y-%m-%dT%H:%M:%fZ','now'), 600, 600, '152', 0, '小溪的大宇宙', strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'));
    INSERT INTO player_balances (user_id, currency, amount, updated_at)
    VALUES (NEW.user_id, 'coin', 3, strftime('%Y-%m-%dT%H:%M:%fZ','now'));
    INSERT INTO player_balances (user_id, currency, amount, updated_at)
    VALUES (NEW.user_id, 'gacha_point', 96343, strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
