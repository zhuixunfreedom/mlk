-- 0018 CN3.5 资源副本低强度波次（ITEM350-15 追加迁移）
--
-- 0017 种子波次为 初级2波/中级·上级3波/特级4波、HP 倍率至 2400‰，实测超纲。
-- 低强度 v2 已先在既有库经管理面校验发布（digest a4378b80…）；本迁移把草稿
-- 统一为同一低强度形态，保证全新库首次发布即得 quest1 普通战量级：
--   每关 1 波 ×3 怪位（slot 200/420/600，同 quest1），无 Boss；
--   怪物取各属性ラビューン（火=1 水=41 风=42 光=43 暗=44，金币本用 43），
--   rare1 普通怪、attack_ms 2400 与 quest1 持平、attackable_count 1；
--   hp_rate_bp=550 / at_rate_bp=890 → 下发 hp=1146≤1158、at=1015≤1021。
-- 只改写草稿表；cn35_resource_config_versions / release 指针与既有台账不动，
-- 已有库的生效版本需经管理面重新发布后才套用（草稿→校验→发布既有流程）。

DELETE FROM cn35_resource_quest_waves
WHERE quest_key LIKE 'mat_%' OR quest_key LIKE 'gold_%';

INSERT INTO cn35_resource_quest_waves
    (quest_key,wave_no,slot,monster_id,scale_bp,hp_rate_bp,at_rate_bp)
SELECT quest_key,1,slot,monster_id,1000,550,890
FROM (
    SELECT 'mat_'||e.key||'_'||t AS quest_key, e.mon AS monster_id
    FROM (SELECT 'fire' AS key,1 AS mon UNION ALL SELECT 'aqua',41
          UNION ALL SELECT 'wind',42 UNION ALL SELECT 'light',43
          UNION ALL SELECT 'dark',44) e,
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4) tiers
    UNION ALL
    SELECT 'gold_'||t, 43 FROM
         (SELECT 1 AS t UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4)
) q,
(
    SELECT 200 AS slot UNION ALL SELECT 420 UNION ALL SELECT 600
) w;
