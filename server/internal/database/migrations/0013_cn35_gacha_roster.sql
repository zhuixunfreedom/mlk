-- cn35_gacha_roster: CN3.5 名册资源证据 + 模板发布值 + 双卡池版本化治理
-- (ROSTER350-01/03)。
--
-- 设计真源：plans/cn350-gacha-roster-2026-09-26/README.md 与
-- notes/proposed/2026-09-26-cn35-gacha-resource-gated-pools.md。
-- 事实等级：本表结构是服务端重建，不是原服 schema 证据。
--
-- 边界：
--   - 只新增表，不改 0001—0012 已应用结构；余额/库存/保底/操作/流水原样保留。
--   - cn35_unit_templates 保存 wiki 原文：可选字段缺失保持 NULL 并记入
--     missing JSON 数组，绝不把"缺失"伪造成 0。
--   - cn35_unit_assets 保留逐族资源证据（APK zip 内 sha256 + served 索引 +
--     asset_sha256 对照 + source_id 溯源），供复核重算。
--   - cn35_unit_roster 的 enabled 是唯一"管理员显式启用"位；导入只填
--     resource_ok/wiki/missing_required 证据位，不自动启用。
--   - cn35_gacha_pools 是**草稿面**池定义（含旧槽 1/4 的显式停用行）；
--     运行时抽卡只读 cn35_gacha_release 指向的已发布版本快照，草稿改动
--     在发布前不影响游戏端。
--   - cn35_gacha_pool_versions 的 snapshot 一经写入不可变（应用层不更新）；
--     生效指针在 cn35_gacha_release 单行，回滚=指针移回旧版本，不改历史。
--   - cn35_admin_audit 只追加：名册导入、启用切换、池草稿编辑、发布、
--     激活切换全部落审计行。
--
-- Recovery: 停服 → 一致性备份 → 配对二进制 + 新 state 目录恢复；
--   本迁移不回填玩家数据，失败回退保留问题库，不得就地删除。

CREATE TABLE cn35_roster_sources (
    source_id INTEGER PRIMARY KEY AUTOINCREMENT,
    -- 来源类别：apk_manifest（逐族资源清单）/ wiki（units.json 快照）/ admin 手工。
    kind TEXT NOT NULL CHECK (length(kind) > 0),
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL DEFAULT '',
    -- 附加溯源（APK 路径/SHA、units.json 版本、served 条目数等），JSON 对象。
    meta TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta)),
    created_at TEXT NOT NULL,
    UNIQUE (kind, sha256)
) STRICT;

CREATE TABLE cn35_unit_templates (
    unit_id INTEGER PRIMARY KEY CHECK (unit_id > 0),
    -- 必需字段（units.json fatal 级）：缺失的整行不导入。
    name TEXT NOT NULL,
    rare INTEGER NOT NULL CHECK (rare BETWEEN 1 AND 9),
    element INTEGER NOT NULL,
    weapon INTEGER NOT NULL,
    unit_type INTEGER NOT NULL,
    life INTEGER NOT NULL,
    atk INTEGER NOT NULL,
    aarea INTEGER NOT NULL,
    -- 可选字段：缺失为 NULL + missing JSON 列名登记（与 cardcatalog.Missing 同口径）。
    anum INTEGER,
    mspd INTEGER,
    aspd REAL,
    hits INTEGER,
    tenacity REAL,
    fire REAL,
    aqua REAL,
    wind REAL,
    light REAL,
    dark REAL,
    gender INTEGER,
    age TEXT,
    name_jp TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    title_jp TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    career TEXT NOT NULL DEFAULT '',
    interest TEXT NOT NULL DEFAULT '',
    nature TEXT NOT NULL DEFAULT '',
    obtain TEXT NOT NULL DEFAULT '',
    remark TEXT NOT NULL DEFAULT '',
    -- wiki 卡池标记（历史参考，决定种子候选，不等于可抽资格）。
    wiki_gacha TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(wiki_gacha)),
    contributors TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(contributors)),
    -- 缺失/占位字段名列表（如 ["mspd","tenacity"]）。
    missing TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(missing)),
    -- 原始 wiki 记录 JSON（无损审计面）。
    raw_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(raw_json)),
    source_id INTEGER NOT NULL REFERENCES cn35_roster_sources(source_id) ON DELETE RESTRICT,
    source_version TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
) STRICT;

-- 模板行每次内容变化的追加式修订史（发布审计+回滚核对用）。
CREATE TABLE cn35_unit_template_revisions (
    unit_id INTEGER NOT NULL,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    -- 修订后整行快照（与 cn35_unit_templates 同列名 JSON）。
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot)),
    actor TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (unit_id, revision)
) STRICT;

-- 逐 unit_id × 资源族的 APK 证据（ROSTER350-01 清单落库）。
CREATE TABLE cn35_unit_assets (
    unit_id INTEGER NOT NULL CHECK (unit_id >= 0),
    family TEXT NOT NULL CHECK (family IN
        ('unit','btn','gray_btn','list','square','large','rectangle','silhouette','large_ns')),
    bundle TEXT NOT NULL CHECK (length(bundle) > 0),
    size INTEGER NOT NULL CHECK (size >= 0),
    crc32 TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL,
    served INTEGER NOT NULL DEFAULT 0 CHECK (served IN (0, 1)),
    index_sha256 TEXT NOT NULL DEFAULT '',
    -- NULL=索引缺该键未核对；0=不一致（证据异常）；1=一致。
    index_match INTEGER CHECK (index_match IS NULL OR index_match IN (0, 1)),
    source_id INTEGER NOT NULL REFERENCES cn35_roster_sources(source_id) ON DELETE RESTRICT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (unit_id, family)
) STRICT;

-- 名册状态：资源证据位（导入写）与启用位（管理员写）分列。
CREATE TABLE cn35_unit_roster (
    unit_id INTEGER PRIMARY KEY CHECK (unit_id >= 0),
    -- 管理员显式启用（可抽资格的人审位；导入永不自动置位）。
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    -- 导入计算的证据位：必需八族全部在场且（有索引时）sha256 一致。
    resource_ok INTEGER NOT NULL DEFAULT 0 CHECK (resource_ok IN (0, 1)),
    -- 缺失的必需族名 JSON（如 ["unit"]；401/402 即此情形）。
    missing_required TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(missing_required)),
    wiki INTEGER NOT NULL DEFAULT 0 CHECK (wiki IN (0, 1)),
    -- 管理员手工阻断（入池前尚待解决的数值/资源问题）。
    blocked INTEGER NOT NULL DEFAULT 0 CHECK (blocked IN (0, 1)),
    notes TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
) STRICT;

-- 草稿面卡池定义。运行时抽卡不读本表——读已发布版本快照。
-- 旧槽 1（限时）与 4（高级金币）保留为 enabled=0 的显式停用行：
-- "停用"是数据事实而非"行不存在"，管理端可展示其状态。
CREATE TABLE cn35_gacha_pools (
    pool_id TEXT PRIMARY KEY CHECK (length(pool_id) > 0),
    -- 客户端槽位契约：list_type_id<4 → 槽号=list_type_id+1（>=4 全落槽1，禁止）。
    slot INTEGER NOT NULL CHECK (slot BETWEEN 1 AND 4),
    list_type_id INTEGER NOT NULL CHECK (list_type_id BETWEEN 0 AND 3),
    code TEXT NOT NULL UNIQUE CHECK (length(code) > 0), -- diamond / gold / legacy_limited / legacy_gold_adv
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    price_point INTEGER NOT NULL DEFAULT 0 CHECK (price_point >= 0),
    price_coin INTEGER NOT NULL DEFAULT 0 CHECK (price_coin >= 0),
    max_exec INTEGER NOT NULL DEFAULT 10 CHECK (max_exec BETWEEN 1 AND 10),
    max_bonus INTEGER NOT NULL DEFAULT 0 CHECK (max_bonus = 0),
    limited INTEGER NOT NULL DEFAULT 0 CHECK (limited IN (0, 1)),
    begin_at INTEGER NOT NULL DEFAULT 0,
    end_at INTEGER NOT NULL DEFAULT 0,
    -- UP 五星集合 JSON 数组（字符串 unit_id）；空=非 UP 池。
    featured_ids TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(featured_ids)),
    -- 无保底池的星级分布 JSON 对象 {"10":70,"20":25,...}（rare_type_id→权重）。
    distribution TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(distribution)),
    -- 保底参数 JSON 对象（enabled/ur_base/soft_start/soft_step/hard/sr_pity/
    -- up_guarantee/non_ur_weights）；enabled=false 时整池不推进保底。
    pity TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(pity)),
    pity_group TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    UNIQUE (slot),
    UNIQUE (list_type_id)
) STRICT;

-- 草稿面池成员（发布前的可编辑名单+权重）。
CREATE TABLE cn35_gacha_pool_draft_members (
    pool_id TEXT NOT NULL REFERENCES cn35_gacha_pools(pool_id) ON DELETE CASCADE,
    unit_id INTEGER NOT NULL CHECK (unit_id > 0),
    weight INTEGER NOT NULL DEFAULT 1 CHECK (weight > 0),
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (pool_id, unit_id)
) STRICT;

-- 已发布配置版本：snapshot 是执行唯一真源，写入后不可变。
CREATE TABLE cn35_gacha_pool_versions (
    version INTEGER PRIMARY KEY AUTOINCREMENT,
    -- 内容指纹（规范 JSON 的 sha256），同内容重复发布去重。
    rules_digest TEXT NOT NULL UNIQUE CHECK (length(rules_digest) > 0),
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot)),
    actor TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

-- 版本成员明细（从 snapshot 冗余展开，供管理端过滤/核对/展示）。
CREATE TABLE cn35_gacha_pool_version_members (
    version INTEGER NOT NULL
        REFERENCES cn35_gacha_pool_versions(version) ON DELETE RESTRICT,
    pool_id TEXT NOT NULL,
    unit_id INTEGER NOT NULL,
    weight INTEGER NOT NULL CHECK (weight > 0),
    rare INTEGER NOT NULL,
    resource_ok INTEGER NOT NULL CHECK (resource_ok IN (0, 1)),
    PRIMARY KEY (version, pool_id, unit_id)
) STRICT;

CREATE INDEX cn35_gacha_pool_version_members_pool
    ON cn35_gacha_pool_version_members(version, pool_id);

-- 当前生效版本指针（单行）。发布=插入版本+移动指针同事务；
-- 回滚=指针移回旧版本（审计行记录）。
CREATE TABLE cn35_gacha_release (
    release_id INTEGER PRIMARY KEY CHECK (release_id = 1),
    version INTEGER NOT NULL
        REFERENCES cn35_gacha_pool_versions(version) ON DELETE RESTRICT,
    actor TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
) STRICT;

-- 管理操作审计（只追加）。actor 来自管理令牌身份或 mercdb -actor。
CREATE TABLE cn35_admin_audit (
    audit_id INTEGER PRIMARY KEY AUTOINCREMENT,
    actor TEXT NOT NULL CHECK (length(actor) > 0),
    action TEXT NOT NULL CHECK (length(action) > 0),
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail)),
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX cn35_admin_audit_recent ON cn35_admin_audit(audit_id DESC);
