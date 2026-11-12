-- 032-dispatch-governance：总管派发设置、不可变决策快照与预算流水。
-- 一条规格一个编号：0009 归 032 独占，029/030/031 分别用 0010/0012/0011，
-- 避免共用文件后往已应用的 migration 里追加表导致旧库缺表。

CREATE TABLE dispatch_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    mode TEXT NOT NULL DEFAULT 'observe'
);

INSERT INTO dispatch_settings(id, mode) VALUES (1, 'observe');

-- balance 为 NULL 表示「未录入」，此时硬门禁跳过，只在建议里提示。
ALTER TABLE agents ADD COLUMN balance REAL;
ALTER TABLE agents ADD COLUMN balance_unit TEXT NOT NULL DEFAULT 'credit';
ALTER TABLE agents ADD COLUMN budget_updated_at TEXT;
ALTER TABLE tasks ADD COLUMN estimated_cost REAL NOT NULL DEFAULT 0;
-- Agent 在 submit/release 时可选上报的实际成本。NULL = 未上报（与 0 有区别），
-- 人类 approved 时优先按它扣减，否则退回 estimated_cost。
ALTER TABLE tasks ADD COLUMN cost_used REAL;

CREATE TABLE dispatch_decisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    tier TEXT NOT NULL DEFAULT '',
    chosen_agent_id INTEGER,
    cost_estimate REAL NOT NULL DEFAULT 0,
    score_total REAL NOT NULL DEFAULT 0,
    score_json TEXT NOT NULL DEFAULT '{}',
    snapshot_json TEXT NOT NULL DEFAULT '{}',
    reason TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual',
    schedule_id INTEGER,
    status TEXT NOT NULL DEFAULT 'pending',
    observed_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    accepted_at TEXT
);

CREATE INDEX idx_dispatch_decisions_task ON dispatch_decisions(task_id, id);

CREATE TABLE budget_ledger (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id INTEGER NOT NULL REFERENCES agents(id),
    task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    delta REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    actor_type TEXT NOT NULL DEFAULT 'system',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX idx_budget_ledger_agent ON budget_ledger(agent_id, id);
