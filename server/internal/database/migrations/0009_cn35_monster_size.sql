-- cn35_player_monsters 增加魔宠 Size（scale）相关列（STAT350-04）。
--
-- 语义与证据（plans/cn35-stat-system-2026-09-23/README.md C0.3）：
--   客户端 Monster.GetScaleCategory 读 scale/real_scale/scale_limit/is_rare，
--   MonsterPowerupModal 以 real_scale 推进 Size（+0.01/0.02）并以
--   hp = floor(base_hp×size)、at = floor(base_seed_at×size^2.36) 显示面板。
--   现状：catalog 1052 条魔宠中 0 条含 scale（模板无初始值），故初始一律为
--   「显式 Size=1.000 降级」，缺口登记在 07 验收矩阵。
--
-- 精度方案：小数以 TEXT 十进制字符串持久化（不用 REAL/float64 存储），
-- 避免二进制浮点累加漂移；读取时精确解析。列可空，NULL = 未证/降级，
-- 读面在 NULL 时按显式降级处理而**不写伪值**。
--
-- Affected routes: /user_monsters、/users/preset_data.json（inventoryDataFor）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移只
-- 追加可空列，不回填任何值，旧行读取语义与迁移前一致（NULL → Size=1.0 降级）。

ALTER TABLE cn35_player_monsters ADD COLUMN scale TEXT
    CHECK (scale IS NULL OR (scale <> '' AND scale NOT GLOB '*[^0-9.]*'));

ALTER TABLE cn35_player_monsters ADD COLUMN real_scale TEXT
    CHECK (real_scale IS NULL OR (real_scale <> '' AND real_scale NOT GLOB '*[^0-9.]*'));

ALTER TABLE cn35_player_monsters ADD COLUMN scale_limit TEXT
    CHECK (scale_limit IS NULL OR (scale_limit <> '' AND scale_limit NOT GLOB '*[^0-9.]*'));

-- is_rare：客户端 ScaleCategory 的 newMonster 标志（决定 7 档/4 档档位表）。
-- NULL = 未证，不下发伪布尔。
ALTER TABLE cn35_player_monsters ADD COLUMN is_rare INTEGER
    CHECK (is_rare IS NULL OR is_rare IN (0, 1));
