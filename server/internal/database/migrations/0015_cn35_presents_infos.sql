-- cn35_infos / cn35_user_presents：真实 app_conf Home.requireData 所需的两张内容表
-- （LEGACY-ASSETS-02）。
--
-- 契约依据（客户端消费侧，ilspy-cs 反编译）：
--   PageBase.WaitForAllDataLoaded（PageBase.cs:782-800）对每个 requireData 键检查
--   IsRequestSuccess()；失败即 ShowErrorModal + CancelShowPage。未注册路由返回 501，
--   客户端判为 ProtocolError（JSONRequester.cs:432）→ 页面被取消。
--   真实 app_conf（v201710121006）Home.requireData 声明：
--     user_presents   → /user_presents/
--     infos           → /infos/index?just_header=true
--     LoginAdventureData → /adventure/            （空态处理，不建表）
--     country_pick_page  → /gachas/choose_attr_page/（空态处理，不建表）
--
--   IconPresentParts.OnLoadOption：cache.Get("user_presents").docList.Count
--     → count>0 才 SetActive(true) 并显示红点；count<=0 → noticeGo 隐藏。
--   IconInfoParts.OnLoadOption：document.Count <= 0 → noticeGo 隐藏。
--   两者都有显式空态分支，故「空集」是合法且安全的取值。
--
-- 为什么入库而非硬编码：公告与礼物是**内容数据**、需可运营增补/停用而无需改代码，
-- 理由与 cn35_messages（0010）一致。
--
-- Affected routes: /user_presents/、/infos/index（requiredata.go）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移只新增空表，
-- 不回填、不触碰既有账号数据；读面在表缺失/未装配时按空集处理并记 warn。

-- 公告：按时间窗过滤后下发给客户端信息图标。
CREATE TABLE cn35_infos (
    id INTEGER PRIMARY KEY CHECK (id > 0),
    category TEXT NOT NULL DEFAULT 'info',
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    -- begin_at/end_at 为 Unix 秒；0 表示该侧不限。
    begin_at INTEGER NOT NULL DEFAULT 0 CHECK (begin_at >= 0),
    end_at INTEGER NOT NULL DEFAULT 0 CHECK (end_at >= 0),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    -- 排序权重：大者靠前（客户端按响应顺序取 most_recently_info）。
    weight INTEGER NOT NULL DEFAULT 0
) STRICT;

-- 读面按 enabled + 时间窗过滤，按 weight DESC, id DESC 取。
CREATE INDEX cn35_infos_window ON cn35_infos(enabled, begin_at, end_at, weight);

-- 账号待领礼物。uuid 为客户端 /user_presents/receive/?uuid= 的键（UserPresentsListRow.cs:86），
-- 必须全局唯一且稳定，故不用自增主键作 uuid 来源。
CREATE TABLE cn35_user_presents (
    uuid TEXT PRIMARY KEY CHECK (length(uuid) > 0),
    user_id TEXT NOT NULL,
    -- content_type/content_id 对齐客户端礼物行消费（type_name/content_id）。
    content_type TEXT NOT NULL DEFAULT 'unit',
    content_id INTEGER NOT NULL CHECK (content_id > 0),
    amount INTEGER NOT NULL DEFAULT 1 CHECK (amount > 0),
    -- 0 表示不过期。
    expire_at INTEGER NOT NULL DEFAULT 0 CHECK (expire_at >= 0),
    received_at INTEGER NOT NULL DEFAULT 0 CHECK (received_at >= 0),
    created_at INTEGER NOT NULL DEFAULT 0 CHECK (created_at >= 0)
) STRICT;

-- 读面：某账号「未领取且未过期」的行。
CREATE INDEX cn35_user_presents_unread
    ON cn35_user_presents(user_id, received_at, expire_at);
