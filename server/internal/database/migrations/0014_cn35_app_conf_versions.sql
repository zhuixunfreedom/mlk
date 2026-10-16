-- cn35_app_conf_versions: app_conf GUI 调试编辑器的旧版本文档归档
--（PAGE-EDITOR，notes/proposed/2026-09-26-cn35-page-conf-gui-editor.md）。
--
-- 设计真源：用户要求「每次保存配置前需要在数据库存下旧版本的json」。
-- 事实等级：本表是服务端管理面结构，不是原服 schema 证据。
--
-- 边界：
--   - doc 存「保存前的完整旧文档」（含 _ 注解键的源 fixture 形态），不是
--     RenderWire 出包字节；doc_sha256 唯一约束去重——同一旧文档只归档一次。
--   - 归档与 cn35_admin_audit 审计行在保存路径的同一事务写入，且都发生在
--     源文件被覆盖之前；行一经写入不更新（append-only 版本史）。
--   - 本表独立于玩家状态域；不含 user_id 外键，不参与账号隔离。
--   - 恢复口径同既有约定：停服 → 一致性备份 → 配对二进制 + 新 state 目录。

CREATE TABLE cn35_app_conf_versions (
    version INTEGER PRIMARY KEY AUTOINCREMENT,
    -- 归档文档的 sha256 hex（规范来源字节原文），同内容去重。
    doc_sha256 TEXT NOT NULL UNIQUE CHECK (length(doc_sha256) = 64),
    -- 保存前的旧 serve_app_conf_android.json 完整字节（含注解键）。
    doc TEXT NOT NULL CHECK (json_valid(doc)),
    actor TEXT NOT NULL CHECK (length(actor) > 0),
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX cn35_app_conf_versions_recent ON cn35_app_conf_versions(version DESC);
