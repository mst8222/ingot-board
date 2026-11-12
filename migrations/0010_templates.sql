-- 029-task-template：把一次已完成的任务沉淀成可复用的派发素材。
-- 一规格一编号：0009 归 032，本文件只归 029，避免往已应用的 migration 里追加表。

CREATE TABLE task_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    workdir TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT 'normal',
    category TEXT NOT NULL DEFAULT '',
    tier TEXT NOT NULL DEFAULT '',
    -- 刻意不加外键：Agent 被删除后保留原 ID，实例化时能明确报「执行端已不存在」，
    -- 而不是被 ON DELETE SET NULL 悄悄变成「未指定」。
    assignee_agent_id INTEGER,
    project_id INTEGER,
    archived_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_task_templates_archived ON task_templates(archived_at, id);
