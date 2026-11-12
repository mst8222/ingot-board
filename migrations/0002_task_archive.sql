-- Ingot Board 迁移 0002：任务归档 + 时间戳归一
--
-- 两件事一起做，因为它们改的是同一批行：归档列要 ALTER TABLE，
-- 时间戳归一要 UPDATE 全表，分两次迁移等于把全表重写两遍。

-- ---------- 1. 归档 ----------
--
-- approved 是终态且原本没有删除接口，「已完成」列只增不减最终挤满看板。
-- 用归档替代删除：任务仍在库里可查，只是不再出现在默认看板。
ALTER TABLE tasks ADD COLUMN archived_at TEXT;

CREATE INDEX idx_tasks_archived_status ON tasks(archived_at, status);
CREATE INDEX idx_tasks_assignee_status_archived ON tasks(assignee_agent_id, status, archived_at);

-- ---------- 2. 时间戳归一 ----------
--
-- 背景：Go 侧原本用 time.RFC3339Nano 写时间戳，它会**剥掉末尾零**：
--   120ms → "2026-10-11T07:00:00.12Z"
--   1200ms → "2026-10-11T07:00:00.1234Z"
-- 字符串比较时第五位 'Z'(0x5A) > '3'(0x33)，于是 ".12Z" > ".1234Z"，
-- **顺序是反的**。这直接影响 ORDER BY updated_at 与租约超时回收的
-- `lease_until < ?` 判断。
--
-- 而 SQLite 的 DEFAULT 用的是定长 3 位小数（strftime('%Y-%m-%dT%H:%M:%fZ')），
-- 于是同一列混着两种格式：定长的 24 字符 vs 变长的 20~30 字符。
--
-- 统一归一为定长毫秒（24 字符）后，字符串比较等价于时间比较。
-- Go 侧同步把 formatTime 改为 timeLayout = "2006-01-02T15:04:05.000Z"。
--
-- WHERE length(...) <> 24 跳过已是定长格式的行，使本段可重复执行。
-- strftime 能解析带 Z 后缀的纳秒串（已实测），归一后长度恒为 24。

UPDATE tasks      SET created_at   = strftime('%Y-%m-%dT%H:%M:%fZ', created_at)
                   WHERE length(created_at) <> 24;

UPDATE tasks      SET updated_at   = strftime('%Y-%m-%dT%H:%M:%fZ', updated_at)
                   WHERE length(updated_at) <> 24;

UPDATE tasks      SET lease_until  = strftime('%Y-%m-%dT%H:%M:%fZ', lease_until)
                   WHERE lease_until IS NOT NULL AND length(lease_until) <> 24;

UPDATE agents     SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', last_seen_at)
                   WHERE last_seen_at IS NOT NULL AND length(last_seen_at) <> 24;

UPDATE agents     SET created_at   = strftime('%Y-%m-%dT%H:%M:%fZ', created_at)
                   WHERE length(created_at) <> 24;

UPDATE task_events SET created_at  = strftime('%Y-%m-%dT%H:%M:%fZ', created_at)
                   WHERE length(created_at) <> 24;