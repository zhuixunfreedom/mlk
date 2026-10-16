-- cn35_player_profiles.helper_unit_id：助战同伴模板 ID（P0 写面 /user_units/set_helper）。
--
-- 契约依据（客户端 ilspy-cs 反编译）：
--   UserUnitDetail.cs:459-465 OnSetHelperButton → "/user_units/set_helper?id=<unit id>"；
--   快照 data.user.helper_unit_id 由既有 numericFieldsToNumber(...,"helper_unit_id")
--   消费位以 JSON NUMBER 下发（training_wire.go / business.go 同一修正路径）。
--
-- Affected routes: /user_units/set_helper（unit_ops_wire.go）、/users/preset_data.json 快照。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移只增列、
--   默认 '' 不回填，不触碰既有账号数据；未设置助战的账号快照不下发该键。
ALTER TABLE cn35_player_profiles
    ADD COLUMN helper_unit_id TEXT NOT NULL DEFAULT '';
