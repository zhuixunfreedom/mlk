-- cn35_daily_shop: 每日商店领取台账（SHOP350-01）。
--
-- 需求：每日发放 100 钻石——面包店（RedDiamondShop）上架 0 元购商品，
-- 每账号每日限购 1 次，库存每日东八区 4:00 刷新。
-- period_key 由 wire 按 (now-4h) 的 UTC+8 日期算出（YYYY-MM-DD）；
-- (user_id,goods_key,period_key) 主键 + 条件 UPDATE 兜底并发与限购。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；仅新增表。

CREATE TABLE cn35_daily_shop_claims (
    user_id    TEXT NOT NULL,
    goods_key  TEXT NOT NULL,
    period_key TEXT NOT NULL,
    count      INTEGER NOT NULL CHECK (count >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, goods_key, period_key)
);

CREATE INDEX idx_cn35_daily_shop_claims_user
    ON cn35_daily_shop_claims (user_id, period_key);
