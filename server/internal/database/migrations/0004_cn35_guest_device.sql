-- cn35_guest_device: 设备唯一键收窄到纯游客行（CN350-12 实机验收修复）。
-- 问题：0003 的 cn35_accounts_device_key 对全部 device_id<>'' 行唯一，
-- 同机 heRegister/SDK 账号写同一 device_id 必撞唯一索引（实机表现为注册
-- 弹窗"登录失败"），违反"同一设备两个独立账号完成 onboarding"的验收判据。
-- 语义：游客身份 = (device_id, account_name='', sdk_account_id='') 每机唯一；
-- HE 注册号以 account_name 为键、UC SDK 号以 sdk_account_id 为键，均可与
-- 游客号共用同一 device_id。QuickLogin 同步按同一谓词过滤（store 代码侧）。
-- Affected routes: /account（heQuickLogin/heRegister/ucLogin/heLogin）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移仅
-- 重建索引，无数据改写，旧二进制在新库上行为退化为"注册撞 device 唯一"。

DROP INDEX cn35_accounts_device_key;
CREATE UNIQUE INDEX cn35_accounts_device_key
    ON cn35_accounts(device_id) WHERE device_id <> '' AND account_name = '' AND sdk_account_id = '';
