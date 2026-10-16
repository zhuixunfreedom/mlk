-- 0026 cn35 符文商品修正：Devin Review 复审后收紧商店面。
-- 1) 下架无服务端生效通路的符文商品：25 ガッツ（开战初始 Guts，纯战斗
--    内效果）、30 ルーン收集（符文掉落管线未实现）、31 シード收集（魔宠
--    掉落加成，无 wiki 公式）。草稿删除后发布快照不再上架；目录锚点
--    skill_rune_25/30/31 保留（玩家已持有的符文仍可装备/出售）。
DELETE FROM cn35_medal_goods_drafts
WHERE goods_key IN ('medal_skill_25', 'medal_skill_30', 'medal_skill_31');

-- 2) 免费符文无限购会放大刷金（购买0扣费→按公式出售得金币），改为每
--    账号限购 1 次：purchase_limit 由 NULL 改 1，与既有
--    cn35_medal_purchase_totals 条件计数通路一致。
UPDATE cn35_medal_goods_drafts SET purchase_limit = 1
WHERE goods_key LIKE 'medal_skill_%';
