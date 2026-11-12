-- Ingot Board 初始结构：agents / tasks / task_events
-- 单机本地服务，SQLite WAL 模式，不做多用户。

CREATE TABLE agents (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL UNIQUE,
    kind          TEXT    NOT NULL DEFAULT 'mcp',
    token_hash    TEXT    NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_seen_at  TEXT,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE tasks (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    title                TEXT    NOT NULL,
    description          TEXT    NOT NULL DEFAULT '',
    acceptance_criteria  TEXT    NOT NULL DEFAULT '',
    workdir              TEXT    NOT NULL DEFAULT '',
    priority             TEXT    NOT NULL DEFAULT 'normal',
    status               TEXT    NOT NULL DEFAULT 'idea',
    assignee_agent_id    INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    lease_until          TEXT,
    version              INTEGER NOT NULL DEFAULT 1,
    created_at           TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at           TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_tasks_status          ON tasks(status);
CREATE INDEX idx_tasks_assignee_status ON tasks(assignee_agent_id, status);

CREATE TABLE task_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id      INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_type   TEXT    NOT NULL,
    actor_id     INTEGER,
    actor_name   TEXT    NOT NULL DEFAULT '',
    type         TEXT    NOT NULL,
    from_status  TEXT,
    to_status    TEXT,
    message      TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_task_events_task ON task_events(task_id, id);

