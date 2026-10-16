-- 0024 CN3.5 物品使用配套 + AP 上限随等级增长
--
-- 用户口径（2026-10-01）：
--  1) 物品使用按钮灰置：客户端 UserItemsListRow.cs 证实可用条件是
--     stock_count>0 + code 分派，服务端 wire 层补齐（纯协议侧）。
--  2) AP 药水应是红瓶 icon：0023 下发的 activity_icon 是蓝色星瓶、
--     bp_potion 的 common_icon/bp 是蓝色徽章，均不符。改用
--     potion_<content_id> 素材族（UIUtil.cs IconAssetName "potion"→
--     common_icon/potion_<n>）：ap_potion→potion_2（红橙瓶）、
--     bp_potion→potion_1（蓝瓶）。素材库无纯红瓶，potion_2 为最接近
--     候选，实机验收为准。
--  3) 升级自动补满 AP 且 max_ap=level+19：存储层升级事务已同步刷新
--     （cn35store/battle.go addExpTx），此处回填既有档案列值保持
--     口径唯一；ap>新上限的档案一并钳制（CHECK ap<=max_ap）。
--
-- Affected routes: /user_items/、/user_items/use/、/user_units/awake、
--   结算/快照 user.max_ap。
-- Recovery: 仅 UPDATE 存量行；停服 → 备份副本恢复到新 state 目录 →
--   配对旧二进制。

-- AP/BP 药水图标换为 potion 素材族。
UPDATE cn35_item_catalog
SET icon_asset_name='common_icon/potion_2', updated_at='2026-10-01T00:00:00Z'
WHERE item_key='ap_potion';
UPDATE cn35_item_catalog
SET icon_asset_name='common_icon/potion_1', updated_at='2026-10-01T00:00:00Z'
WHERE item_key='bp_potion';

-- 既有档案 AP 上限回填 level+19 并钳制存量 ap。
UPDATE cn35_player_profiles
SET max_ap=level+19, ap=MIN(ap, level+19), updated_at='2026-10-01T00:00:00Z';
