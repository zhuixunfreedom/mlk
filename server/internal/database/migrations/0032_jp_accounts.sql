-- JP 适配分支（feature/jp-adapt）首个域表：日服账号。
--
-- 协议依据（GameAssembly.dll 静态取证）：
--   POST /user_accounts/create        入参 temporal_id → 回 auth.user_id/auth.token
--   POST /user_accounts/generate_token 入参 user_id/token → 回 auth.token
--   POST /user_accounts/confirm_token  入参 user_id/token[/request_password/has_old_password]
--   GET  /users/init_app              入参 user_id（仅已持 EncryptedUUID 的老用户）
--   POST /users/check_auth            入参 user_id（会话头）
--
-- user_id 即客户端持久化的 UUID 字符串（TokenRequester.SaveUuidAndToken）；
-- token 为登录会话凭据。nickname/玩家资料字段待 preset_data 协议确认后
-- 按 expand→回填 流程追加列，本表只先承载登录链路必需状态。

CREATE TABLE jp_accounts (
    -- 客户端可见的用户身份串；同时也是 APP_ID_1 DES 载荷的明文 uuid。
    user_id       TEXT PRIMARY KEY,
    -- 当前会话 token（generate_token/confirm_token 轮换）。
    token         TEXT NOT NULL,
    -- create 时的 temporal_id（客户端本地 Guid），用于幂等建号：
    -- 同一 temporal_id 重放返回既有账号而不重复建行。
    temporal_id   TEXT UNIQUE,
    nickname      TEXT NOT NULL DEFAULT '',
    tutorial_done INTEGER NOT NULL DEFAULT 0,
    platform_data TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
) STRICT;
