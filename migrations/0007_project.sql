-- 项目实体：任务可选归属；project_workdirs 是项目与工作目录的关联表，
-- 024 只建表不提供接口，读写接口与消费方见 025 与 017。

CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    color       TEXT    NOT NULL DEFAULT '',
    archived_at TEXT,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE project_workdirs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    workdir    TEXT    NOT NULL,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE(project_id, workdir)
);

ALTER TABLE tasks ADD COLUMN project_id INTEGER REFERENCES projects(id);

CREATE INDEX idx_tasks_project ON tasks(project_id);
