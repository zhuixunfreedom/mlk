-- 0020 CN3.5 物品发放台账 + 徽章交易所上架全部进化素材
--
-- 1) cn35_item_grants：admin 物品发放幂等台账（与 cn35_medal_grants 同形），
--    支撑「后台为用户下发 2-5★觉醒书与全部进化素材」验证物品列表。
-- 2) cn35_medal_goods_drafts 追加 15 个进化素材商品（goods_id=5..19），
--    草稿→校验→发布既有流程生效；价格为首版策划值（可经管理面改草稿）。
-- Affected routes: /medal_items/ + exchange（material 商品）、
--   /mercd/cn35/admin/item/grant、/mercd/adminapi/item/grant。
-- Recovery: 全部为新表与新草稿行，不触碰既有行；停服 → 备份副本恢复到新
--   state 目录 → 配对旧二进制。

CREATE TABLE cn35_item_grants (
    request_key TEXT PRIMARY KEY CHECK (length(request_key) > 0),
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    item_key TEXT NOT NULL REFERENCES cn35_item_catalog(item_key) ON DELETE RESTRICT,
    count INTEGER NOT NULL CHECK (count > 0),
    actor TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

-- 徽章商品草稿追加：全部 15 个进化素材常驻不限购。
-- goods_id=4+content_id（5..19）不与 0017 的 4 本觉醒书冲突；
-- 价格按形态档位：种子 20 / 花蕾 60 / 花 150（首版策划值）。
INSERT INTO cn35_medal_goods_drafts
    (goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,enabled,updated_at)
SELECT 'medal_mat_'||content_id, 4+content_id, item_key, name,
    CASE form WHEN 1 THEN 20 WHEN 2 THEN 60 ELSE 150 END,
    'medal', NULL, 4+content_id, 1, '2026-09-30T00:00:00Z'
FROM cn35_item_catalog
WHERE kind='material' AND enabled=1;
