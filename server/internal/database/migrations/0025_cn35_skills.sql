-- 0024 CN3.5 符文系统：账号符文库存 + 符文物品目录 + 交易所基础符文商品
--
-- 客户端契约（ilspy-cs 取证，详见 notes/proposed/2026-10-01-cn35-skills.md）：
--   - user_skills 行键 uuid/skill_id/level/base_level/price/protected/unit_id；
--     写面 /user_skills/{set_all,off,sell,powerup,composite,protect}。
--   - 装备宿主引用 cn35_player_units（user_id,unit_id）复合所有权键，
--     与 cn35_player_monsters.unit_id 同形；NULL=未装备。
--   - level 为十进制定标值（品质阈值见 gamerch 510636：5/15/22.5/27/30/33/35/36），
--     存 REAL 便于直读；写入路径（powerup/composite）按 +0.5 步长推进，
--     读出时统一直读（客户端 Document.dec 只认 JSON NUMBER）。
--   - 交易所符文商品：物品目录新增符文锚点行
--     （item_key=skill_rune_<skill_id>，icon=common/pic-skill_<id>）。
--     cn35_item_catalog.kind CHECK 为 0017 既有约束，重建被引用表在
--     foreign_keys=ON 下不可行（0023 表重建模式只适用于无子表场景）；
--     故锚点行用 kind='consumable'，兑换走 item_key 前缀识别改发符文
--     库存行（grantSkillTx），不经 cn35_player_items。
--   - 商品 price=0 → wire 层下发 price=1+price_str="免费"（0023 既定
--     下限），服务端实扣 0。
-- Affected routes: /user_skills/*、/medal_items/ + exchange、
--   /user_units（buildUnitDoc 附 skills/skill_slot_count）、战斗 execute。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；
--   全部为新表与新行（无表重建）；物品目录只追加锚点行。

-- 符文库存：账号作用域 (user_id,uuid) 主键；unit_id 为装备宿主
-- （复合外键与 cn35_player_monsters 同形）。
CREATE TABLE cn35_player_skills (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    uuid TEXT NOT NULL CHECK (length(uuid) > 0),
    skill_id INTEGER NOT NULL CHECK (skill_id BETWEEN 1 AND 37),
    level REAL NOT NULL CHECK (level >= 5 AND level <= 36),
    base_level REAL NOT NULL DEFAULT 5 CHECK (base_level >= 0),
    protected INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0, 1)),
    unit_id INTEGER CHECK (unit_id IS NULL OR unit_id > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, uuid),
    FOREIGN KEY (user_id, unit_id) REFERENCES cn35_player_units(user_id, unit_id)
        ON DELETE RESTRICT
) STRICT;

CREATE INDEX cn35_player_skills_unit_key
    ON cn35_player_skills(user_id, unit_id) WHERE unit_id IS NOT NULL;

-- 符文商品锚点行（kind='consumable' 避开 CHECK 重建；kind 仅参与
-- 商品归组/校验，符文锚点不入账 cn35_player_items；content_id=
-- 1000+skill_id 避开已用段；element 按 Skill.cs 属性映射 1..5）。
INSERT INTO cn35_item_catalog
    (item_key,kind,content_id,name,icon_asset_name,element,form,rare,bag_category,enabled,created_at,updated_at)
VALUES
    ('skill_rune_1','consumable',1001,'生命符文','common/pic-skill_1',2,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_2','consumable',1002,'攻击符文','common/pic-skill_2',1,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_3','consumable',1003,'迅捷符文','common/pic-skill_3',3,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_4','consumable',1004,'火之符文','common/pic-skill_4',1,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_5','consumable',1005,'水之符文','common/pic-skill_5',2,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_6','consumable',1006,'风之符文','common/pic-skill_6',3,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_7','consumable',1007,'光之符文','common/pic-skill_7',4,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_8','consumable',1008,'暗之符文','common/pic-skill_8',5,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_9','consumable',1009,'速度符文','common/pic-skill_9',4,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_10','consumable',1010,'韧性符文','common/pic-skill_10',5,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_11','consumable',1011,'火之守护','common/pic-skill_11',1,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_12','consumable',1012,'水之守护','common/pic-skill_12',2,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_13','consumable',1013,'风之守护','common/pic-skill_13',3,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_14','consumable',1014,'光之守护','common/pic-skill_14',4,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_15','consumable',1015,'暗之守护','common/pic-skill_15',5,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_16','consumable',1016,'骑士符文','common/pic-skill_16',1,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_17','consumable',1017,'穿透符文','common/pic-skill_17',5,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_18','consumable',1018,'暴击符文','common/pic-skill_18',4,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_19','consumable',1019,'治疗符文','common/pic-skill_19',2,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_20','consumable',1020,'狂战士符文','common/pic-skill_20',3,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_21','consumable',1021,'防御符文','common/pic-skill_21',4,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_22','consumable',1022,'魔法符文','common/pic-skill_22',5,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_23','consumable',1023,'回避符文','common/pic-skill_23',3,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_24','consumable',1024,'根性符文','common/pic-skill_24',2,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_25','consumable',1025,'士气符文','common/pic-skill_25',1,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_27','consumable',1027,'复活符文','common/pic-skill_27',5,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_28','consumable',1028,'经验符文','common/pic-skill_28',1,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_29','consumable',1029,'金币符文','common/pic-skill_29',4,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_30','consumable',1030,'收集符文','common/pic-skill_30',3,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('skill_rune_31','consumable',1031,'魔宠符文','common/pic-skill_31',2,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z');
-- 说明：skill_id 26（イクリーズ系，客户端筛选表无 CN 名）与 32-37（强化用/
-- 上位合成素材，无 pic-skill_32/33 图标资源）本批不上架；目录仅覆盖
-- 有图标且效果已取证的基础符文 1-25/27-31。

-- 符文商品草稿：price=0 免费、常驻不限购、道具页签（group_key='item'），
-- goods_id=100+skill_id。发布仍走既有「草稿→校验→发布」流程。
INSERT INTO cn35_medal_goods_drafts
    (goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,group_key,enabled,updated_at)
SELECT 'medal_skill_' || CAST(content_id - 1000 AS TEXT),
    100 + (content_id - 1000),
    item_key, name, 0, 'medal', NULL, 100 + (content_id - 1000), 'item', 1,
    '2026-10-01T00:00:00Z'
FROM cn35_item_catalog WHERE item_key LIKE 'skill_rune_%';
