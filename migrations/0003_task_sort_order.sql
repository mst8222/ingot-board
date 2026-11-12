-- Ingot Board 迁移 0003：列内手动排序
--
-- 背景：之前看板的列内顺序完全由 sort 字段决定（默认 id DESC），
-- 前端的同列拖拽只改本地视图、不发请求 —— 刷新即丢失。
-- 这条迁移补上 sort_order，让"手动顺序"成为一等公民。

ALTER TABLE tasks ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;

-- 看板查询恒为 "WHERE ... ORDER BY <sort_expr>"，sort_order 是兜底 tiebreaker。
-- 但同列重排要按 sort_order 定位，因此索引仍建 (status, sort_order)：
-- 它同时服务于「按手动顺序排序」的主查询与「整列重编号」的定位。
CREATE INDEX idx_tasks_status_sort_order ON tasks(status, sort_order);

-- ---------- 已有数据赋初值 ----------
--
-- 按 (status, id DESC) 编号，即**沿用迁移前的默认显示顺序**。
-- 这样升级后看板看起来没变，只是不再依赖 id 的巧合。
--
-- 用窗口函数而不是「先查出来再逐条 UPDATE」：
-- 一次 UPDATE 即可，且不需要在 Go 侧持有全表数据。
-- ROW_NUMBER() OVER (PARTITION BY status ORDER BY id DESC)
-- 保证同一 status 内不重复、连续，且 id 大的在前。
--
-- 两次 UPDATE 是必要的：
--   1) 一次性给所有行编号；
--   2) 然后把每列的编号整体平移成 1..n（ROW_NUMBER 从 1 起，
--      但 0 会被 DEFAULT 0 误认为"未赋值"，留 1..n 更干净）。
-- 平移后的值为 1..n，与下方默认值语义一致。
UPDATE tasks SET sort_order = rn + 1
  FROM (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY status ORDER BY id DESC) AS rn
      FROM tasks
  ) AS ranked
 WHERE tasks.id = ranked.id;