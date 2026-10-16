-- 公会申请/邀请：终态可多条（同用户被拒后可再申请、再被拒），
-- 唯一约束只约束 pending。0027 表级 UNIQUE(guild_id,user_id,kind,status)
-- 在二次拒绝时撞旧 rejected 行 → 重建表并改部分唯一索引。

CREATE TABLE cn35_guild_subscriptions_new (
  sub_id      TEXT PRIMARY KEY,
  guild_id    TEXT NOT NULL REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK(kind IN ('apply','invite')),
  status      TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','cancelled')) DEFAULT 'pending',
  created_at  TEXT NOT NULL,
  decided_at  TEXT
);
INSERT INTO cn35_guild_subscriptions_new
  SELECT sub_id,guild_id,user_id,kind,status,created_at,decided_at
  FROM cn35_guild_subscriptions;
DROP TABLE cn35_guild_subscriptions;
ALTER TABLE cn35_guild_subscriptions_new RENAME TO cn35_guild_subscriptions;

CREATE UNIQUE INDEX cn35_guild_subs_pending
  ON cn35_guild_subscriptions(guild_id, user_id, kind) WHERE status='pending';
CREATE INDEX cn35_guild_subs_user ON cn35_guild_subscriptions(user_id, status);
