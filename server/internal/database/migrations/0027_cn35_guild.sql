-- GUILD350-02..09：公会与公会战域。
-- 设计要点：
--  1) 全部写路径走 cn35_guild_ops 唯一键幂等（user_id+op_key），事务内
--     先判已决再落库，并发/重放/重试安全。
--  2) 入会/邀请双链：subscriptions(kind=apply|invite)+status 状态机，
--     条件 UPDATE 保证只能 pending→终态单次迁移。
--  3) 会战会话按日场（date_key+field_index）建，参加方在参与者表中固定；
--     应援/祈祷/出战经 cn35_gw_actions 操作键幂等并累计公会 GP 与个人 GRP。
--  4) 结算台账 cn35_gw_settlements 保证同一场次奖励只发一次。

CREATE TABLE IF NOT EXISTS cn35_guilds (
  guild_id        TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  play_type_id    TEXT NOT NULL DEFAULT '',
  approval_type_id TEXT NOT NULL DEFAULT '',
  recruit_text    TEXT NOT NULL DEFAULT '',
  memo            TEXT NOT NULL DEFAULT '',
  shifts          INTEGER NOT NULL DEFAULT 127,  -- 曜日参战位掩码(0..6)
  battle_setting_text TEXT NOT NULL DEFAULT '',
  rating          INTEGER NOT NULL DEFAULT 0,
  rank_id         TEXT NOT NULL DEFAULT '1',
  gp              INTEGER NOT NULL DEFAULT 0,    -- 本日场累计 GP
  gp_total        INTEGER NOT NULL DEFAULT 0,    -- 历史累计
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cn35_guild_members (
  guild_id    TEXT NOT NULL REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  role        INTEGER NOT NULL CHECK(role IN (1,2,3)), -- 1 成员 2 副长 3 会长
  joined_at   TEXT NOT NULL,
  PRIMARY KEY (user_id)
);
CREATE INDEX IF NOT EXISTS cn35_guild_members_guild ON cn35_guild_members(guild_id);

-- 申请/邀请与操作幂等记录（含自建行外的邀请）。
CREATE TABLE IF NOT EXISTS cn35_guild_subscriptions (
  sub_id      TEXT PRIMARY KEY,
  guild_id    TEXT NOT NULL REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK(kind IN ('apply','invite')),
  status      TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','cancelled')) DEFAULT 'pending',
  created_at  TEXT NOT NULL,
  decided_at  TEXT,
  UNIQUE (guild_id, user_id, kind, status)
);
CREATE INDEX IF NOT EXISTS cn35_guild_subs_user ON cn35_guild_subscriptions(user_id, status);

-- 写路径幂等：每用户每操作一行；payload_json 快照入参，result_json 快照回执。
CREATE TABLE IF NOT EXISTS cn35_guild_ops (
  user_id     TEXT NOT NULL,
  op_key      TEXT NOT NULL,
  op          TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '',
  result_json  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  PRIMARY KEY (user_id, op_key)
);

-- 防卫卡组：每成员独立 5 槽。
CREATE TABLE IF NOT EXISTS cn35_guild_defense (
  user_id    TEXT NOT NULL,
  slot       INTEGER NOT NULL CHECK(slot BETWEEN 0 AND 4),
  monster_uuid TEXT NOT NULL,
  batch      INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, slot)
);

-- 设施投资：公会行级累计 + 个人投资明细（台账）。
CREATE TABLE IF NOT EXISTS cn35_guild_equipment (
  guild_id   TEXT NOT NULL REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  type_id    TEXT NOT NULL,
  level      INTEGER NOT NULL DEFAULT 0,
  point      INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (guild_id, type_id)
);
CREATE TABLE IF NOT EXISTS cn35_guild_invest_log (
  guild_id  TEXT NOT NULL,
  user_id   TEXT NOT NULL,
  type_id   TEXT NOT NULL,
  amount    INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS cn35_guild_invest_user ON cn35_guild_invest_log(user_id, created_at);

-- 会战会话：date_key(Asia/Shanghai)+field_index 唯一；rank_ids_json 存匹配分组。
CREATE TABLE IF NOT EXISTS cn35_gw_sessions (
  session_id   TEXT PRIMARY KEY,
  date_key     TEXT NOT NULL,
  field_index  INTEGER NOT NULL,
  group_key    TEXT NOT NULL,           -- 匹配组（同组公会互见）
  status       TEXT NOT NULL CHECK(status IN ('open','closed','settled')) DEFAULT 'open',
  winner_guild_id TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  UNIQUE (date_key, field_index, group_key)
);

CREATE TABLE IF NOT EXISTS cn35_gw_participants (
  session_id  TEXT NOT NULL REFERENCES cn35_gw_sessions(session_id) ON DELETE CASCADE,
  guild_id    TEXT NOT NULL REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  color       TEXT NOT NULL DEFAULT '',
  gp          INTEGER NOT NULL DEFAULT 0,
  rank_in_group INTEGER NOT NULL DEFAULT 0,
  seed_powerup_rate REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, guild_id)
);

-- 应援/祈祷/出战动作幂等 + 个人贡献。
CREATE TABLE IF NOT EXISTS cn35_gw_actions (
  session_id  TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  op_key      TEXT NOT NULL,
  action      TEXT NOT NULL CHECK(action IN ('help','pray','attack')),
  target_guild_id TEXT NOT NULL DEFAULT '',
  bp_cost     INTEGER NOT NULL DEFAULT 0,
  gp_delta    INTEGER NOT NULL DEFAULT 0,
  result      TEXT NOT NULL DEFAULT 'ok',
  payload_json TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  PRIMARY KEY (user_id, op_key)
);
CREATE INDEX IF NOT EXISTS cn35_gw_actions_sess ON cn35_gw_actions(session_id);

CREATE TABLE IF NOT EXISTS cn35_gw_member_scores (
  session_id  TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  guild_id    TEXT NOT NULL,
  attack_count INTEGER NOT NULL DEFAULT 0,
  win_count   INTEGER NOT NULL DEFAULT 0,
  gp_earned   INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (session_id, user_id)
);

-- 公会战战斗会话（独立于剧情 cn35_battle_sessions：扣 BP、敌人=防卫魔宠）。
CREATE TABLE IF NOT EXISTS cn35_gw_attacks (
  battle_id    TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL,
  user_id      TEXT NOT NULL,
  guild_id     TEXT NOT NULL,           -- 攻击方公会
  target_guild_id TEXT NOT NULL,
  difficulty   TEXT NOT NULL CHECK(difficulty IN ('normal','hard','very_hard')),
  status       TEXT NOT NULL CHECK(status IN ('prepared','bp_paid','cleared','lost','abandoned')),
  bp_cost      INTEGER NOT NULL,
  snapshot_json TEXT NOT NULL DEFAULT '',  -- 目标防卫魔宠+设施快照
  party_json   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  settled_at   TEXT
);

-- 场次结算台账：同一 session 对同一公会只发一次奖励。
CREATE TABLE IF NOT EXISTS cn35_gw_settlements (
  session_id TEXT NOT NULL,
  guild_id   TEXT NOT NULL,
  rewards_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY (session_id, guild_id)
);
