-- cn35_inventory: 账号独立的同伴/魔宠库存与发放台账（PLAY350-06）。
-- 字段语义依 PLAY350-03 handoffs/03/inventory-contract.md：
--   user_units[].id = 模板 ID（一账号一模板一行，duplicated_count 堆叠），
--   同时是资源名/详情参数/leader_unit_id 比对基准；uuid 属实例级链接
--   （passive_skills/monster 装备），列表行不消费。
--   user_monsters[].uuid = 实例 ID（字符串主键）；unit_id = 装备宿主
--   user_units 模板 ID，NULL=未装备。
-- 模板存在性不在此建外键（模板真源是 cardcatalog 静态目录），由领域层
-- 在授予前校验 cardcatalog.Unit/Monster 存在；所有权键 (user_id,unit_id)
-- 可被后续编队复合引用。
-- 发放台账 grant_key 由 08 定（如 five_star_grant.json 的 grant_version），
-- (user_id,grant_key,unit_id) 唯一 → 重复发放自然幂等冲突。
-- Affected routes: 07 的 /user_units、/user_monsters 与 preset_data 注入。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移
-- 只新增空表，不回填既有账号库存（发放归 08，旧库升级步骤在 11）。

CREATE TABLE cn35_player_units (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    unit_id INTEGER NOT NULL CHECK (unit_id > 0),
    level INTEGER NOT NULL CHECK (level >= 1),
    duplicated_count INTEGER NOT NULL DEFAULT 0 CHECK (duplicated_count >= 0),
    protected INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0, 1)),
    newcomer INTEGER NOT NULL DEFAULT 1 CHECK (newcomer IN (0, 1)),
    monster_uuid TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, unit_id)
) STRICT;

CREATE INDEX cn35_player_units_monster_key
    ON cn35_player_units(user_id, monster_uuid) WHERE monster_uuid <> '';

CREATE TABLE cn35_player_monsters (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    uuid TEXT NOT NULL CHECK (length(uuid) > 0),
    monster_id INTEGER NOT NULL CHECK (monster_id > 0),
    unit_id INTEGER CHECK (unit_id IS NULL OR unit_id > 0),
    level INTEGER NOT NULL DEFAULT 1 CHECK (level >= 1),
    protected INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, uuid),
    -- 装备宿主必须是同账号已拥有的单位行（复合所有权键，供 07/编队复用）
    FOREIGN KEY (user_id, unit_id) REFERENCES cn35_player_units(user_id, unit_id)
        ON DELETE RESTRICT
) STRICT;

CREATE INDEX cn35_player_monsters_unit_key
    ON cn35_player_monsters(user_id, unit_id) WHERE unit_id IS NOT NULL;

CREATE TABLE cn35_inventory_grants (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    grant_key TEXT NOT NULL CHECK (length(grant_key) > 0),
    unit_id INTEGER NOT NULL CHECK (unit_id > 0),
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, grant_key, unit_id)
) STRICT;
