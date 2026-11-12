-- 031-scheduled-dispatch：人把 029 模板挂到计划上，服务进程内按 next_run_at 扫描。
-- 一规格一编号：0009 归 032、0010 归 029，本文件只归 031。

CREATE TABLE schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    -- 刻意不加外键：模板被删后调度器要明确报「模板已不存在」，而不是被悄悄置空。
    template_id INTEGER NOT NULL,
    -- inherit 跟随全局 dispatch_settings；observe 只落 idea+建议；auto 才无人值守定人。
    dispatch_mode TEXT NOT NULL DEFAULT 'inherit',
    -- interval_minutes 与 daily_at 恰选其一，由 store 层校验。
    interval_minutes INTEGER,
    daily_at TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    next_run_at TEXT NOT NULL,
    last_fired_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_schedules_due ON schedules(enabled, next_run_at);

-- 幂等锚点：一次 tick 先抢 (schedule_id, next_run_at) 触发槽，抢到才实例化。
-- 与任务表完全解耦，所以 008 的租约回收把任务放回 todo 也不会造成重复派发。
CREATE TABLE schedule_fires (
    schedule_id INTEGER NOT NULL,
    next_run_at TEXT NOT NULL,
    task_id INTEGER,
    fired_at TEXT NOT NULL,
    PRIMARY KEY (schedule_id, next_run_at)
);
