-- 0021 CN3.5 物品背包分类修正
--
-- 0017 种子把素材暂定 bag_category='3'（同伴外型页）、觉醒书 '4'
-- （背景页）。客户端本地化表（sharedassets0）证实分类：
--   1=觉醒之书(ITEM_BTN_BOOST) 2=进化素材(ITEM_BTN_POWERUP)
--   3=同伴外型 4=背景 5=音乐 6=其他 7=礼物匣
-- UserItemsButtonsListComponent.cs:51-117 按 user_items_hash 键 1-7 分发。
-- 修正：material→'2'、book→'1'。仅 UPDATE 既有目录行，不改已应用迁移。
-- Affected routes: /user_items/（user_items_hash 分组）、写面回推。
-- Recovery: 单表 UPDATE；停服 → 备份副本恢复到新 state 目录 → 配对旧二进制。

UPDATE cn35_item_catalog SET bag_category='2', updated_at='2026-09-30T15:00:00Z' WHERE kind='material';
UPDATE cn35_item_catalog SET bag_category='1', updated_at='2026-09-30T15:00:00Z' WHERE kind='book';
