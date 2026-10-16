-- cn35_items_resources: 物品目录/账号库存、资源副本与徽章商品运营配置、
-- 发布版本/生效指针、奖励结算台账、徽章发放幂等台账（ITEM350-02/03/06/07）。
--
-- 设计真源：notes/proposed/2026-09-30-cn35-items-resources.md +
-- plans/cn35-items-training-2026-09-30.md。要点：
--   - 物品身份由服务端目录定义（item_key 稳定主键）；客户端 content_id 与
--     icon_asset_name 分列，书籍业务键与 item_1..4 图标键分离。
--   - 副本经济（24 关 AP/掉落/波次/金币 + 4 个徽章商品）作为草稿行入库，
--     发布为不可变快照 + 单行生效指针；战斗会话记 config_version 冻结结算口径。
--   - 数值为「user-design-2026-09-30」首版策划值（用户授权），非原服事实。
--
-- Affected routes: /user_items/（真实读面）、/medal_items/ + exchange、
--   /stages /areas /quests /quests/execute/*（extra_stages 资源副本）、
--   /user_presents/receive/（content_type=medal）、/user_units/awake（暂定契约）、
--   /mercd/cn35/admin/resource/*、/mercd/cn35/admin/medal/grant。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；全部为新表
--   与一列新增，不触碰既有行。

CREATE TABLE cn35_item_catalog (
    item_key TEXT PRIMARY KEY CHECK (length(item_key) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('material','book','consumable')),
    content_id INTEGER NOT NULL UNIQUE CHECK (content_id > 0),
    name TEXT NOT NULL DEFAULT '',
    icon_asset_name TEXT NOT NULL DEFAULT '',
    element INTEGER NOT NULL DEFAULT 0 CHECK (element BETWEEN 0 AND 5),
    form INTEGER NOT NULL DEFAULT 0 CHECK (form BETWEEN 0 AND 3),
    rare INTEGER NOT NULL DEFAULT 0 CHECK (rare BETWEEN 0 AND 5),
    bag_category TEXT NOT NULL DEFAULT '1',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_player_items (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    item_key TEXT NOT NULL REFERENCES cn35_item_catalog(item_key) ON DELETE RESTRICT,
    quantity INTEGER NOT NULL CHECK (quantity >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_key)
) STRICT;

CREATE TABLE cn35_resource_quest_drafts (
    quest_key TEXT PRIMARY KEY CHECK (length(quest_key) > 0),
    quest_id INTEGER NOT NULL UNIQUE CHECK (quest_id > 0),
    area_id INTEGER NOT NULL CHECK (area_id > 0),
    stage_id INTEGER NOT NULL CHECK (stage_id > 0),
    name TEXT NOT NULL DEFAULT '',
    ap INTEGER NOT NULL CHECK (ap > 0),
    exp INTEGER NOT NULL DEFAULT 0 CHECK (exp >= 0),
    sort INTEGER NOT NULL DEFAULT 0,
    open INTEGER NOT NULL DEFAULT 1 CHECK (open IN (0, 1)),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_resource_quest_drops (
    quest_key TEXT NOT NULL REFERENCES cn35_resource_quest_drafts(quest_key) ON DELETE CASCADE,
    seq INTEGER NOT NULL CHECK (seq > 0),
    reward_type TEXT NOT NULL CHECK (reward_type IN ('item','gacha_point')),
    item_key TEXT REFERENCES cn35_item_catalog(item_key) ON DELETE RESTRICT,
    chance_bp INTEGER NOT NULL CHECK (chance_bp BETWEEN 0 AND 10000),
    min_count INTEGER NOT NULL CHECK (min_count >= 0),
    max_count INTEGER NOT NULL CHECK (max_count >= min_count),
    PRIMARY KEY (quest_key, seq),
    CHECK (reward_type <> 'item' OR item_key IS NOT NULL)
) STRICT;

CREATE TABLE cn35_resource_quest_waves (
    quest_key TEXT NOT NULL REFERENCES cn35_resource_quest_drafts(quest_key) ON DELETE CASCADE,
    wave_no INTEGER NOT NULL CHECK (wave_no > 0),
    slot INTEGER NOT NULL CHECK (slot > 0),
    monster_id INTEGER NOT NULL CHECK (monster_id > 0),
    scale_bp INTEGER NOT NULL DEFAULT 1000 CHECK (scale_bp > 0),
    hp_rate_bp INTEGER NOT NULL CHECK (hp_rate_bp > 0),
    at_rate_bp INTEGER NOT NULL CHECK (at_rate_bp > 0),
    PRIMARY KEY (quest_key, wave_no, slot)
) STRICT;

CREATE TABLE cn35_medal_goods_drafts (
    goods_key TEXT PRIMARY KEY CHECK (length(goods_key) > 0),
    goods_id INTEGER NOT NULL UNIQUE CHECK (goods_id > 0),
    item_key TEXT NOT NULL REFERENCES cn35_item_catalog(item_key) ON DELETE RESTRICT,
    name TEXT NOT NULL DEFAULT '',
    price INTEGER NOT NULL CHECK (price > 0),
    currency TEXT NOT NULL DEFAULT 'medal' CHECK (currency = 'medal'),
    purchase_limit INTEGER CHECK (purchase_limit IS NULL OR purchase_limit > 0),
    sort INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_resource_config_versions (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    snapshot_json TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    author TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_resource_config_release (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL REFERENCES cn35_resource_config_versions(version),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_resource_reward_events (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    request_key TEXT NOT NULL CHECK (length(request_key) > 0),
    operation TEXT NOT NULL CHECK (operation IN ('battle_clear','medal_exchange')),
    request_json TEXT NOT NULL DEFAULT '{}',
    quest_key TEXT NOT NULL DEFAULT '',
    config_version INTEGER NOT NULL DEFAULT 0,
    result TEXT NOT NULL DEFAULT '',
    rewards_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, request_key)
) STRICT;

CREATE TABLE cn35_medal_purchase_totals (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    goods_key TEXT NOT NULL,
    total_count INTEGER NOT NULL CHECK (total_count >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, goods_key)
) STRICT;

CREATE TABLE cn35_medal_grants (
    request_key TEXT PRIMARY KEY CHECK (length(request_key) > 0),
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    amount INTEGER NOT NULL CHECK (amount > 0),
    actor TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

ALTER TABLE cn35_battle_sessions
    ADD COLUMN config_version INTEGER NOT NULL DEFAULT 0;

-- 物品目录种子：15 个培养素材（content_id=PowerupItem.cs 已证 1..15，
-- id=element+(form-1)*5）+ 4 本觉醒书（content_id 101..104 为暂定值，
-- 客户端 item_id 待 ITEM350-01 核对后按 catalog 行调整，不动迁移）。
INSERT INTO cn35_item_catalog
    (item_key,kind,content_id,name,icon_asset_name,element,form,rare,bag_category,enabled,created_at,updated_at)
VALUES
    ('powerup_item_1','material',1,'火之种子','common_icon/powerup_item_1',1,1,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_2','material',2,'水之种子','common_icon/powerup_item_2',2,1,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_3','material',3,'风之种子','common_icon/powerup_item_3',3,1,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_4','material',4,'光之种子','common_icon/powerup_item_4',4,1,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_5','material',5,'暗之种子','common_icon/powerup_item_5',5,1,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_6','material',6,'火之花蕾','common_icon/powerup_item_6',1,2,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_7','material',7,'水之花蕾','common_icon/powerup_item_7',2,2,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_8','material',8,'风之花蕾','common_icon/powerup_item_8',3,2,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_9','material',9,'光之花蕾','common_icon/powerup_item_9',4,2,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_10','material',10,'暗之花蕾','common_icon/powerup_item_10',5,2,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_11','material',11,'火之花','common_icon/powerup_item_11',1,3,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_12','material',12,'水之花','common_icon/powerup_item_12',2,3,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_13','material',13,'风之花','common_icon/powerup_item_13',3,3,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_14','material',14,'光之花','common_icon/powerup_item_14',4,3,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('powerup_item_15','material',15,'暗之花','common_icon/powerup_item_15',5,3,0,'3',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('awakening_book_2','book',101,'二星觉醒书','common_icon/item_1',0,0,2,'4',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('awakening_book_3','book',102,'三星觉醒书','common_icon/item_2',0,0,3,'4',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('awakening_book_4','book',103,'四星觉醒书','common_icon/item_3',0,0,4,'4',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z'),
    ('awakening_book_5','book',104,'五星觉醒书','common_icon/item_4',0,0,5,'4',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z');

-- 资源副本草稿种子：5 属性 × 4 难度素材本 + 4 档金币本。
-- quest_id=90000+elem*10+tier；area_id=9001..9005/9006；stage_id=900/901；
-- AP/掉落/金币/波次/敌强倍率取计划 §可后台调整的初始副本值
-- （来源 user-design-2026-09-30，可经 admin 改草稿再发布）。
INSERT INTO cn35_resource_quest_drafts
    (quest_key,quest_id,area_id,stage_id,name,ap,exp,sort,open,updated_at)
VALUES
    ('mat_fire_1',90011,9001,900,'火素材本·初级',5,10,1,1,'2026-09-30T00:00:00Z'),
    ('mat_fire_2',90012,9001,900,'火素材本·中级',8,20,2,1,'2026-09-30T00:00:00Z'),
    ('mat_fire_3',90013,9001,900,'火素材本·上级',12,40,3,1,'2026-09-30T00:00:00Z'),
    ('mat_fire_4',90014,9001,900,'火素材本·特级',16,80,4,1,'2026-09-30T00:00:00Z'),
    ('mat_aqua_1',90021,9002,900,'水素材本·初级',5,10,1,1,'2026-09-30T00:00:00Z'),
    ('mat_aqua_2',90022,9002,900,'水素材本·中级',8,20,2,1,'2026-09-30T00:00:00Z'),
    ('mat_aqua_3',90023,9002,900,'水素材本·上级',12,40,3,1,'2026-09-30T00:00:00Z'),
    ('mat_aqua_4',90024,9002,900,'水素材本·特级',16,80,4,1,'2026-09-30T00:00:00Z'),
    ('mat_wind_1',90031,9003,900,'风素材本·初级',5,10,1,1,'2026-09-30T00:00:00Z'),
    ('mat_wind_2',90032,9003,900,'风素材本·中级',8,20,2,1,'2026-09-30T00:00:00Z'),
    ('mat_wind_3',90033,9003,900,'风素材本·上级',12,40,3,1,'2026-09-30T00:00:00Z'),
    ('mat_wind_4',90034,9003,900,'风素材本·特级',16,80,4,1,'2026-09-30T00:00:00Z'),
    ('mat_light_1',90041,9004,900,'光素材本·初级',5,10,1,1,'2026-09-30T00:00:00Z'),
    ('mat_light_2',90042,9004,900,'光素材本·中级',8,20,2,1,'2026-09-30T00:00:00Z'),
    ('mat_light_3',90043,9004,900,'光素材本·上级',12,40,3,1,'2026-09-30T00:00:00Z'),
    ('mat_light_4',90044,9004,900,'光素材本·特级',16,80,4,1,'2026-09-30T00:00:00Z'),
    ('mat_dark_1',90051,9005,900,'暗素材本·初级',5,10,1,1,'2026-09-30T00:00:00Z'),
    ('mat_dark_2',90052,9005,900,'暗素材本·中级',8,20,2,1,'2026-09-30T00:00:00Z'),
    ('mat_dark_3',90053,9005,900,'暗素材本·上级',12,40,3,1,'2026-09-30T00:00:00Z'),
    ('mat_dark_4',90054,9005,900,'暗素材本·特级',16,80,4,1,'2026-09-30T00:00:00Z'),
    ('gold_1',90601,9006,901,'金币本·初级',5,10,1,1,'2026-09-30T00:00:00Z'),
    ('gold_2',90602,9006,901,'金币本·中级',8,20,2,1,'2026-09-30T00:00:00Z'),
    ('gold_3',90603,9006,901,'金币本·上级',12,40,3,1,'2026-09-30T00:00:00Z'),
    ('gold_4',90604,9006,901,'金币本·特级',16,80,4,1,'2026-09-30T00:00:00Z');

-- 掉落种子：素材本同属性素材独立概率（种子/花蕾/花 = content_id
-- element+(form-1)*5）；金币本固定 gacha_point。
INSERT INTO cn35_resource_quest_drops
    (quest_key,seq,reward_type,item_key,chance_bp,min_count,max_count)
SELECT quest_key,1,'item',
    'powerup_item_'||(element),10000,
    CASE substr(quest_key,-1) WHEN '1' THEN 2 WHEN '2' THEN 3 WHEN '3' THEN 4 ELSE 6 END,
    CASE substr(quest_key,-1) WHEN '1' THEN 3 WHEN '2' THEN 4 WHEN '3' THEN 6 ELSE 8 END
FROM (
    SELECT 'mat_'||e.key||'_'||t AS quest_key, e.elem AS element
    FROM (SELECT 1 AS elem,'fire' AS key UNION ALL SELECT 2,'aqua'
          UNION ALL SELECT 3,'wind' UNION ALL SELECT 4,'light'
          UNION ALL SELECT 5,'dark') e,
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4) tiers
);
INSERT INTO cn35_resource_quest_drops
    (quest_key,seq,reward_type,item_key,chance_bp,min_count,max_count)
SELECT quest_key,2,'item',
    'powerup_item_'||(element+5),
    CASE substr(quest_key,-1) WHEN '1' THEN 2500 WHEN '2' THEN 6000 ELSE 10000 END,
    CASE substr(quest_key,-1) WHEN '1' THEN 1 WHEN '2' THEN 1 WHEN '3' THEN 2 ELSE 3 END,
    CASE substr(quest_key,-1) WHEN '1' THEN 1 WHEN '2' THEN 2 WHEN '3' THEN 3 ELSE 4 END
FROM (
    SELECT 'mat_'||e.key||'_'||t AS quest_key, e.elem AS element
    FROM (SELECT 1 AS elem,'fire' AS key UNION ALL SELECT 2,'aqua'
          UNION ALL SELECT 3,'wind' UNION ALL SELECT 4,'light'
          UNION ALL SELECT 5,'dark') e,
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4) tiers
);
INSERT INTO cn35_resource_quest_drops
    (quest_key,seq,reward_type,item_key,chance_bp,min_count,max_count)
SELECT quest_key,3,'item',
    'powerup_item_'||(element+10),
    CASE substr(quest_key,-1) WHEN '2' THEN 1000 WHEN '3' THEN 4000 WHEN '4' THEN 10000 ELSE 0 END,
    1,
    CASE substr(quest_key,-1) WHEN '4' THEN 2 ELSE 1 END
FROM (
    SELECT 'mat_'||e.key||'_'||t AS quest_key, e.elem AS element
    FROM (SELECT 1 AS elem,'fire' AS key UNION ALL SELECT 2,'aqua'
          UNION ALL SELECT 3,'wind' UNION ALL SELECT 4,'light'
          UNION ALL SELECT 5,'dark') e,
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4) tiers
)
WHERE substr(quest_key,-1) <> '1';
INSERT INTO cn35_resource_quest_drops
    (quest_key,seq,reward_type,item_key,chance_bp,min_count,max_count)
VALUES
    ('gold_1',1,'gacha_point',NULL,10000,500,500),
    ('gold_2',1,'gacha_point',NULL,10000,2000,2000),
    ('gold_3',1,'gacha_point',NULL,10000,6000,6000),
    ('gold_4',1,'gacha_point',NULL,10000,15000,15000);

-- 波次种子：初级 2 波 / 中级·上级 3 波 / 特级 4 波；每波两只同属性怪，
-- HP/AT 倍率（千分比）按难度档 700/1100/1700/2400 与 700/1000/1400/1800。
-- monster_id 取 cardcatalog 已存在模板：火=1 水=23 风=21 光=22 暗=24；
-- 金币本用光属性 22。
INSERT INTO cn35_resource_quest_waves
    (quest_key,wave_no,slot,monster_id,scale_bp,hp_rate_bp,at_rate_bp)
SELECT quest_key,wave_no,slot,monster_id,1000,
    CASE substr(quest_key,-1) WHEN '1' THEN 700 WHEN '2' THEN 1100 WHEN '3' THEN 1700 ELSE 2400 END,
    CASE substr(quest_key,-1) WHEN '1' THEN 700 WHEN '2' THEN 1000 WHEN '3' THEN 1400 ELSE 1800 END
FROM (
    SELECT 'mat_'||e.key||'_'||t AS quest_key, e.mon AS monster_id
    FROM (SELECT 'fire' AS key,1 AS mon UNION ALL SELECT 'aqua',23
          UNION ALL SELECT 'wind',21 UNION ALL SELECT 'light',22
          UNION ALL SELECT 'dark',24) e,
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4) tiers
    UNION ALL
    SELECT 'gold_'||t, 22 FROM
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4)
) q,
(
    SELECT 1 AS wave_no, 200 AS slot UNION ALL SELECT 1,420
    UNION ALL SELECT 2,200 UNION ALL SELECT 2,420
    UNION ALL SELECT 3,200 UNION ALL SELECT 3,420
    UNION ALL SELECT 4,200 UNION ALL SELECT 4,420
) w
WHERE w.wave_no <= CASE substr(q.quest_key,-1) WHEN '1' THEN 2 WHEN '4' THEN 4 ELSE 3 END;

-- 徽章商品草稿种子：2～5★觉醒书常驻不限购；5★ 500 为用户指定值，
-- 2～4★ 50/150/300 为首版提议值。
INSERT INTO cn35_medal_goods_drafts
    (goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,enabled,updated_at)
VALUES
    ('medal_book_2',1,'awakening_book_2','二星觉醒书',50,'medal',NULL,1,1,'2026-09-30T00:00:00Z'),
    ('medal_book_3',2,'awakening_book_3','三星觉醒书',150,'medal',NULL,2,1,'2026-09-30T00:00:00Z'),
    ('medal_book_4',3,'awakening_book_4','四星觉醒书',300,'medal',NULL,3,1,'2026-09-30T00:00:00Z'),
    ('medal_book_5',4,'awakening_book_5','五星觉醒书',500,'medal',NULL,4,1,'2026-09-30T00:00:00Z');

-- 既有账号补 medal=0 余额行（新账号在建号种子中一并写入）。
INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at)
SELECT user_id,'medal',0,'2026-09-30T00:00:00Z' FROM cn35_accounts
WHERE NOT EXISTS (SELECT 1 FROM cn35_player_balances b
    WHERE b.user_id=cn35_accounts.user_id AND b.currency='medal');
