package store

import (
	"database/sql"
	"errors"

	"github.com/mst8222/ingot-board/internal/domain"
)

// ---------- 列内手动排序 ----------
//
// 背景：看板的列内顺序原本完全由 sort 字段决定（默认 id DESC），
// 前端同列拖拽只改本地视图、不发请求 —— 刷新即丢失。
// sort_order 把"手动顺序"变成一等公民，但它带来三个必须处理的问题，
// 本文件集中解决，避免散落在 CRUD 各处。
//
// ## 1. 为什么用连续整数而不是分数（fractional indexing）
//
// 分数方案（往两数之间插中值）写入更少，但会持续退化：
// 同一位置反复插入 eventually 精度耗尽，且无法校验。
// 本项目是单机、列内最多几百条，整列重写的成本可忽略 ——
// 一次 UPDATE ... WHERE status=? 走 idx_tasks_status_sort_order 索引，
// 实测在 500 条量级仍是毫秒级。用可读、可校验、可解释的整数。
//
// ## 2. 归档任务为什么不参与编号
//
// 归档是"已完成"列无限膨胀的解药。归档任务不该影响可见顺序，
// 所以编号只覆盖 archived_at IS NULL 的行。
// 取消归档时任务回到原 sort_order —— 它原本的位置可能被占，
// 因此 Unarchive 必须重编号，见 unarchiveTask。
//
// ## 3. 并发
//
// 重排会写整列，因此同一列的两次并发重排必须串行化。
// db.SetMaxOpenConns(1) 已保证全局单连接，事务本身就是串行的；
// 这里额外用 version 乐观锁防"两个浏览器基于同一快照重排"，
// 后提交的那个会拿到 409 而不是静默覆盖。

// AppendToEnd 传给 execStatusUpdate / HumanTransition，表示"追加到目标列末尾"。
//
// 用具名常量而不是裸 -1：落位下标是 0 基的，-1 天然与"未指定/末尾"绑定，
// 但写在调用点上看不出含义，容易被后人误当成"移到首位"。
const AppendToEnd = -1

// nextSortOrderTx 返回该状态下可用的下一个 sort_order（列末 +1）。
func nextSortOrderTx(tx *sql.Tx, status string) (int, error) {
	var maxOrder sql.NullInt64
	err := tx.QueryRow(
		`SELECT MAX(sort_order) FROM tasks WHERE status=? AND archived_at IS NULL`,
		status).Scan(&maxOrder)
	if err != nil {
		return 0, err
	}
	// 空列 MAX 为 NULL；用 1 起（0 留给"未赋值"的哨兵语义）。
	if !maxOrder.Valid || maxOrder.Int64 < 1 {
		return 1, nil
	}
	return int(maxOrder.Int64) + 1, nil
}

// columnIDsTx 按当前顺序取出该状态下的全部任务 ID（仅未归档）。
//
// 关联的 tx 只能来自调用方已开启的事务：列内重编号必须与其他写入
// 原子生效，否则中途失败会留下重复的 sort_order。
func columnIDsTx(tx *sql.Tx, status domain.Status) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT id FROM tasks
		  WHERE status=? AND archived_at IS NULL
		  ORDER BY sort_order ASC, id ASC`, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// renumberColumnTx 把 ids 按给定顺序重写为 1..n。
//
// 整列重写而不是"只改被移动的那一行"：sort_order 是稠密的连续整数，
// 单独改一行必然产生重复值，之后的 ORDER BY 结果就不确定了。
func renumberColumnTx(tx *sql.Tx, status domain.Status, ids []int64) error {
	stmt, err := tx.Prepare(`UPDATE tasks SET sort_order=? WHERE id=? AND status=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, id := range ids {
		if _, err := stmt.Exec(i+1, id, string(status)); err != nil {
			return err
		}
	}
	return nil
}

// MoveTask 把任务移动到同列的第 toIndex 位（0 基）。
//
// 与 Transition 的分工：
//   - Transition 改 status，涉及状态机、守卫、租约、事件时间线；
//   - MoveTask 只改列内位置，不改状态、不写 task_events
//     （拖一下顺序不是一条值得记录的业务事件，写进去只会污染时间线）。
//
// 为什么不合并成"transition 带 toIndex"：合并会让每次同列拖拽都走完整
// 的状态机与事件路径，而同列拖拽根本不涉及状态变化。
// 分开后两条路径各自简单，出问题时也更容易定位。
//
// version 乐观锁：调用方传入它读到的 version。
// 不匹配返回 ErrConflict —— 多人（或多个标签页）同时重排同一列时，
// 后提交者必须知道自己的顺序已经被覆盖，而不是静默生效。
func (s *Store) MoveTask(id int64, toIndex int, version int64) (*domain.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, err
	}
	if t.IsArchived() {
		// 归档任务不在看板上，重排没有意义。
		// 放行的话 sort_order 会被写到一个永远不被读取的区间里。
		return nil, domain.ErrInvalidStatus
	}
	if t.Version != version {
		return nil, domain.ErrConflict
	}
	if toIndex < 0 {
		return nil, domain.ErrInvalidInput
	}

	ids, err := columnIDsTx(tx, t.Status)
	if err != nil {
		return nil, err
	}
	pos := indexOf(ids, id)
	if pos < 0 {
		return nil, domain.ErrNotFound
	}
	moved := moveTo(ids, pos, toIndex)
	// 位置没变就不写库：否则 version 会白白 +1，
	// 让其他客户端的乐观锁基线失效。
	if sameOrder(ids, moved) {
		return t, nil
	}
	if err := renumberColumnTx(tx, t.Status, moved); err != nil {
		return nil, err
	}
	// bump 整列的 version 还是只 bump 被移动的那条？
	// 只 bump 被移动的：version 的语义是"这条任务被谁改过"，
	// 邻列卡片只是被动换了位置，让它们的 version 全失效会引发
	// 大量无谓的 409。它们的 sort_order 变了由 SSE 广播兜底。
	if _, err := tx.Exec(
		`UPDATE tasks SET version=version+1, updated_at=? WHERE id=?`,
		formatTime(nowUTC()), id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTask(id)
}

// ColumnTaskIDs 返回该状态下的任务 ID 顺序（仅未归档）。
//
// 供应用层广播列顺序用。它是一个独立事务 ——
// 不能复用重排事务里的 tx，否则广播出去的会是未提交的顺序。
func (s *Store) ColumnTaskIDs(status string) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT id FROM tasks
		  WHERE status=? AND archived_at IS NULL
		  ORDER BY sort_order ASC, id ASC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 16)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MoveIntoColumn 把任务插入目标列的指定位置（跨列拖拽用）。
//
// ⚠️ 调用时机很重要：**必须在 status 字段被改成 to 之前调用**。
// 它靠 `WHERE status=to` 读出目标列的现有成员，若 status 已改，
// 查询结果里会**包含它自己**，再插入一次就多出一条 ——
// 表现为该列 sort_order 整体偏移 1、末尾出现空洞。
// 这个顺序依赖由 TestTransitionKeepsTargetColumnDense 锁死。
//
// toIndex >= 目标列长度时追加到末尾（跨列拖拽的常见落点）。
func MoveIntoColumn(tx *sql.Tx, from, to domain.Status, moveID int64, toIndex int) error {
	if from == to {
		// 同列重排走 MoveTask，不该到这里。
		// 保留判断是为了让函数在直接复用时也不会把任务复制一份。
		return nil
	}
	// 先摘源列：此时 moveID 的 status 还是 from，columnIDsTx 找得到它。
	fromIDs, err := columnIDsTx(tx, from)
	if err != nil {
		return err
	}
	pos := indexOf(fromIDs, moveID)
	if pos < 0 {
		return domain.ErrNotFound
	}
	rest := make([]int64, 0, len(fromIDs)-1)
	rest = append(rest, fromIDs[:pos]...)
	rest = append(rest, fromIDs[pos+1:]...)
	if err := renumberColumnTx(tx, from, rest); err != nil {
		return err
	}

	// 再读目标列：此时 moveID 还没进目标列，读到的就是完整的原成员。
	toIDs, err := columnIDsTx(tx, to)
	if err != nil {
		return err
	}
	insertAt := toIndex
	if insertAt < 0 || insertAt > len(toIDs) {
		insertAt = len(toIDs)
	}
	merged := make([]int64, 0, len(toIDs)+1)
	merged = append(merged, toIDs[:insertAt]...)
	merged = append(merged, moveID)
	merged = append(merged, toIDs[insertAt:]...)
	// 先给 moveID 一个占位 sort_order —— renumberColumnTx 只更新
	// WHERE id=? AND status=to 的行，此时它的 status 还是 from，更新不到。
	// 因此最后一并把 status 也改掉。
	if _, err := tx.Exec(
		`UPDATE tasks SET sort_order=? WHERE id=?`, insertAt+1, moveID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET status=? WHERE id=?`, string(to), moveID); err != nil {
		return err
	}
	return renumberColumnTx(tx, to, merged)
}

// ---------- 纯函数（便于测试） ----------

func indexOf(ids []int64, id int64) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

// moveTo 把 from 位置的元素移到 to 位置，其余元素相对顺序不变。
//
// 实现刻意避开分情况切片：早先写成"前移/后移两个分支各切一刀"，
// 前移分支的边界写错（ids[from+1:to] 在 to<from 时反向）会直接 panic。
// 抽掉被移动的元素再在 to 处插入，只有一条路径，无需分类讨论。
func moveTo(ids []int64, from, to int) []int64 {
	if from < 0 || from >= len(ids) {
		return ids
	}
	if to < 0 {
		to = 0
	}
	if to >= len(ids) {
		to = len(ids) - 1
	}
	item := ids[from]
	// 摘掉元素后的剩余序列，长度 len-1
	rest := make([]int64, 0, len(ids)-1)
	rest = append(rest, ids[:from]...)
	rest = append(rest, ids[from+1:]...)

	out := make([]int64, 0, len(ids))
	out = append(out, rest[:to]...)
	out = append(out, item)
	out = append(out, rest[to:]...)
	return out
}

func sameOrder(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ErrOrderConflict 保留位：重排的冲突走 domain.ErrConflict，
// 此处不额外定义错误类型，避免同一语义有两套错误码。
var _ = errors.New
