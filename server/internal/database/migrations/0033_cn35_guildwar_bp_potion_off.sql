-- 0033 CN3.5 公会战修正批：徽章交换所下架 BP 药水
--
-- 需求（2026-10-09）：商店下架 BP 药水。0023 引入的
-- medal_potion_bp 草稿在此删除；生效快照由 app.go 启动补发逻辑
-- （migrate-0033 作者标记）重发一版不含该商品的配置。
-- ap_potion/medal_potion_ap 保留；bp_potion 物品本体保留在
-- cn35_item_catalog（玩家背包内已持有的仍可正常使用）。
--
-- Affected routes: /medal_items/ + exchange、/mercd/cn35/admin/resource/*。
-- Recovery: 回滚需在恢复库中重插 0023 的草稿行并对旧版配置点回滚。

DELETE FROM cn35_medal_goods_drafts WHERE goods_key = 'medal_potion_bp';
