-- 0019 CN3.5 新手任务领取态（show_tutorial_tasks 读面修复配套）
--
-- 任务进度由既有台账推导（通关计数/魔宠数/等级/扭蛋/素材库存），
-- 本表只记「已领奖」幂等标记：user_id+task_index 唯一，领取与发奖同事务，
-- 重复领取命中冲突即拒绝，不留半写。
CREATE TABLE IF NOT EXISTS cn35_tutorial_task_claims (
    user_id    TEXT    NOT NULL,
    task_index INTEGER NOT NULL,
    claimed_at TEXT    NOT NULL,
    PRIMARY KEY (user_id, task_index)
);
