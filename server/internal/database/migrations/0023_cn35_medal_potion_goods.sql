-- 0023 CN3.5 徽章交换所第 4 页签：AP/BP 药水免费兑换
--
-- 客户端 ExchangeListParts.cs:82-103 已证：medal_right_hash 文档按
-- item_list/powerup_item_list/story_unit_list/rare_medal_item_list/
-- friend_medal_item_list 五组切页签；rare_medal_item_list 非空即切
-- 四按钮模式，多出第 4 个页签。ExchangeImageButton.cs:144-155 仅在
-- price/p_price/friend_price 其一 >0 时才发起 /medal_items/exchange，
-- 故「消耗 0」只能是服务端实扣 0，下发面 price 需兜底为 1
-- （items_wire.go medalGoodsDoc 处理），存档价格仍为 0。
--
-- 1) cn35_item_catalog 追加 2 条 consumable 药水（content_id 201/202
--    避开已用 1..15/101..104；图标用内嵌 common_icon/activity_icon
--    与 common_icon/bp；bag_category='6' 其他页）。
-- 2) cn35_medal_goods_drafts 重建：price CHECK 放宽为 >=0（0=免费商品），
--    新增 group_key 列（''=按物品 kind 归组、'rare_medal'=稀有徽章页签、
--    'friend_medal'=友情徽章页签、'story_unit'=同伴页签）。
-- 3) 草稿上架 AP/BP 药水：price=0、不限购、group_key='rare_medal'。
--    发布仍走既有「草稿→校验→发布」流程。
--
-- Affected routes: /medal_items/ + exchange、/mercd/cn35/admin/resource/*。
-- Recovery: 草稿表重建只复制既有行；目录/草稿只追加；停服 → 备份副本
--   恢复到新 state 目录 → 配对旧二进制。

-- 物品目录追加：AP/BP 药水。
INSERT INTO cn35_item_catalog
    (item_key,kind,content_id,name,icon_asset_name,element,form,rare,bag_category,enabled,created_at,updated_at)
VALUES
    ('ap_potion','consumable',201,'AP药水','common_icon/activity_icon',0,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
    ('bp_potion','consumable',202,'BP药水','common_icon/bp',0,0,0,'6',1,'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z');

-- 徽章商品草稿表重建：price 允许 0（免费商品）+ 新增 group_key 页签分组列。
CREATE TABLE cn35_medal_goods_drafts_new (
    goods_key TEXT PRIMARY KEY CHECK (length(goods_key) > 0),
    goods_id INTEGER NOT NULL UNIQUE CHECK (goods_id > 0),
    item_key TEXT NOT NULL REFERENCES cn35_item_catalog(item_key) ON DELETE RESTRICT,
    name TEXT NOT NULL DEFAULT '',
    price INTEGER NOT NULL CHECK (price >= 0),
    currency TEXT NOT NULL DEFAULT 'medal' CHECK (currency = 'medal'),
    purchase_limit INTEGER CHECK (purchase_limit IS NULL OR purchase_limit > 0),
    sort INTEGER NOT NULL DEFAULT 0,
    group_key TEXT NOT NULL DEFAULT '' CHECK (group_key IN ('','item','powerup','story_unit','rare_medal','friend_medal')),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    updated_at TEXT NOT NULL
) STRICT;

INSERT INTO cn35_medal_goods_drafts_new
    (goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,group_key,enabled,updated_at)
SELECT goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,'',enabled,updated_at
FROM cn35_medal_goods_drafts;

DROP TABLE cn35_medal_goods_drafts;
ALTER TABLE cn35_medal_goods_drafts_new RENAME TO cn35_medal_goods_drafts;

-- 药水商品草稿：免费（price=0）、常驻不限购、归入稀有徽章页签。
INSERT INTO cn35_medal_goods_drafts
    (goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,group_key,enabled,updated_at)
VALUES
    ('medal_potion_ap',20,'ap_potion','AP药水',0,'medal',NULL,20,'rare_medal',1,'2026-10-01T00:00:00Z'),
    ('medal_potion_bp',21,'bp_potion','BP药水',0,'medal',NULL,21,'rare_medal',1,'2026-10-01T00:00:00Z');
