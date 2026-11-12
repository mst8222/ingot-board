// Package store 负责 SQLite 持久化与原子状态流转。
// 关键并发约束：claim/heartbeat/submit 全部在事务内按当前状态做条件更新，
// 依赖 SQLite 的单写锁保证只有一个 Agent 能领取成功。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/migrations"

	_ "modernc.org/sqlite"
)

// Store SQLite 数据仓储。
type Store struct {
	db *sql.DB
}

// Open 打开数据库并执行未应用的迁移。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// 单连接写，避免 SQLITE_BUSY；读操作很快，本地场景足够。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("应用 pragma 失败 %s: %w", p, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		return err
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var version int64
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			return fmt.Errorf("迁移文件名无法解析版本号 %s: %w", name, err)
		}
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, version).Scan(&exists); err != nil {
			return err
		}
		if exists == 1 {
			continue
		}
		sqlBytes, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移 %s 执行失败: %w", name, err)
		}
		// 迁移文件内自带建表语句，这里补登记；若文件中已插入同名行会被主键忽略。
		if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES(?)`, version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---------- 时间与扫描辅助 ----------

func nowUTC() time.Time { return time.Now().UTC() }

// timeLayout 定长毫秒，与 SQLite 的 strftime('%Y-%m-%dT%H:%M:%fZ') 完全一致，
// 归一后所有时间戳都是 24 字符，**字符串比较等价于时间比较**。
//
// 不要用 time.RFC3339Nano：它会剥掉末尾零，于是
// "2026-10-11T07:00:00.12Z" 与 "2026-10-11T07:00:00.1234Z" 相比，
// 第五位 'Z'(0x5A) > '3'(0x33)，判定前者更大 —— 顺序是反的。
// 这会让 ORDER BY updated_at 排序错误、租约超时回收漏触发。
// 迁移 0002 已把历史数据归一到本格式。
const timeLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime 宽容解析：定长毫秒与 RFC3339Nano 变长纳秒都能读，
// 这样即使迁移前写入的老数据尚未归一，也不会让整个列表加载失败。
func parseTime(raw string) (time.Time, error) {
	if t, err := time.Parse(timeLayout, raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, raw)
}

func scanTask(row interface {
	Scan(dest ...any) error
}) (*domain.Task, error) {
	var t domain.Task
	var assignee sql.NullInt64
	var lease sql.NullString
	var archived sql.NullString
	var status, rawCreated, rawUpdated string
	var projectID sql.NullInt64
	var estimatedCost, costUsed sql.NullFloat64
	if err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Workdir,
		&t.Priority, &status, &assignee, &lease, &t.Version, &archived,
		&t.SortOrder, &rawCreated, &rawUpdated,
		&t.Category, &t.Tier, &t.RouteReason, &projectID, &estimatedCost, &costUsed,
	); err != nil {
		return nil, err
	}
	if estimatedCost.Valid {
		t.EstimatedCost = estimatedCost.Float64
	}
	if costUsed.Valid {
		v := costUsed.Float64
		t.CostUsed = &v
	}
	t.Status = domain.Status(status)
	if assignee.Valid {
		v := assignee.Int64
		t.AssigneeAgentID = &v
	}
	if projectID.Valid {
		v := projectID.Int64
		t.ProjectID = &v
	}
	if lease.Valid && lease.String != "" {
		lt, err := parseTime(lease.String)
		if err != nil {
			return nil, err
		}
		t.LeaseUntil = &lt
	}
	if archived.Valid && archived.String != "" {
		at, err := parseTime(archived.String)
		if err != nil {
			return nil, err
		}
		t.ArchivedAt = &at
	}
	var err error
	if t.CreatedAt, err = parseTime(rawCreated); err != nil {
		return nil, err
	}
	if t.UpdatedAt, err = parseTime(rawUpdated); err != nil {
		return nil, err
	}
	return &t, nil
}

// taskColumns 是所有读任务的 SQL 共享的列清单。
//
// ⚠️ sort_order 必须在这里：漏掉它不会让查询失败，而是让 sort_order
// 恒为 0 —— 表现为"排序功能看起来还在但完全不生效"，极难排查。
// 由 TestTaskColumnsCoversSortOrder 强制。
const taskColumns = `id, title, description, acceptance_criteria, workdir,
	priority, status, assignee_agent_id, lease_until, version, archived_at,
	sort_order, created_at, updated_at, category, tier, route_reason, project_id, estimated_cost, cost_used`

const agentColumns = `id, name, kind, enabled, quota, serves, cost_rank, last_seen_at, created_at, balance, balance_unit, budget_updated_at`

func scanTaskRow(row *sql.Row) (*domain.Task, error) {
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

// ---------- Agent ----------

// CreateAgent 登记一个 Agent，返回实体（token 明文只在应用层出现一次）。
func (s *Store) CreateAgent(name, kind, tokenHash string) (*domain.Agent, error) {
	now := formatTime(nowUTC())
	res, err := s.db.Exec(
		`INSERT INTO agents(name, kind, token_hash, enabled, created_at) VALUES(?,?,?,1,?)`,
		name, kind, tokenHash, now)
	if err != nil {
		return nil, mapWriteError(err)
	}
	id, _ := res.LastInsertId()
	return s.GetAgent(id)
}

// ListAgents 返回全部 Agent，按 ID 排序，并推导在线状态。
func (s *Store) ListAgents() ([]*domain.Agent, error) {
	rows, err := s.db.Query(`SELECT ` + agentColumns + ` FROM agents ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := nowUTC()
	// make 而非 var：nil 切片序列化成 null，前端 .map 直接崩。
	out := make([]*domain.Agent, 0, 8)
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		a.ComputeOnline(now)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAgentResult 删除结果。ReassignedTasks 为被退回灵感箱的任务数，
// 大于 0 时前端需要二次确认，因为它意味着这些任务丢失了指派人。
type DeleteAgentResult struct {
	ReassignedTasks int `json:"reassigned_tasks"`
}

// DeleteAgent 删除 Agent。
//
// ⚠️ 护栏：tasks.assignee_agent_id 是 REFERENCES agents(id) ON DELETE SET NULL，
// 直接删 Agent 会把其名下任务的指派**静默清空**。一旦 Agent 进程消失，
// 这些 todo 任务就永远无法被领取，而 UI 上只显示"未指派"，人根本无从察觉。
//
// 因此默认拒绝；若 force 则先把受影响任务退回灵感箱再删，
// 让它们回到人类手里，而不是变成无主任务。由 TestDeleteAgentRefusesWhenActive 强制。
func (s *Store) DeleteAgent(id int64, force bool) (*DeleteAgentResult, error) {
	if _, err := s.GetAgent(id); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var active int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks
		 WHERE assignee_agent_id=? AND status IN ('todo','doing') AND archived_at IS NULL`,
		id).Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 && !force {
		return nil, errors.Join(domain.ErrInvalidInput,
			fmt.Errorf("该 Agent 仍有 %d 个待执行/执行中的任务，请先改派或归档", active))
	}
	if active > 0 {
		// 批量退回不能走一条 UPDATE —— 那样这些任务的 sort_order
		// 会留在原列（todo/doing）的编号上，而 idea 列里可能已有同值，
		// 造成新列里出现重复 sort_order。
		// 逐条走 execStatusUpdate 才能同时维护两列的编号。
		rows, err := tx.Query(
			`SELECT id FROM tasks
			 WHERE assignee_agent_id=? AND status IN ('todo','doing') AND archived_at IS NULL`,
			id)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for rows.Next() {
			var tid int64
			if err := rows.Scan(&tid); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, tid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, tid := range ids {
			if err := execStatusUpdate(tx, tid, domain.StatusIdea, nil, 1, AppendToEnd); err != nil {
				return nil, err
			}
			// 任务退回人类手里，同时解除指派（与原 UPDATE 的语义一致）。
			if _, err := tx.Exec(
				`UPDATE tasks SET assignee_agent_id=NULL WHERE id=?`, tid); err != nil {
				return nil, err
			}
		}
	}
	res, err := tx.Exec(`DELETE FROM agents WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if err := requireRow(res); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &DeleteAgentResult{ReassignedTasks: active}, nil
}

// GetAgent 按 ID 取 Agent。
func (s *Store) GetAgent(id int64) (*domain.Agent, error) {
	row := s.db.QueryRow(
		`SELECT `+agentColumns+` FROM agents WHERE id=?`, id)
	a, err := scanAgent(row)
	if err == nil {
		a.ComputeOnline(nowUTC())
	}
	return a, err
}

// GetAgentByName 按名称取 Agent。
func (s *Store) GetAgentByName(name string) (*domain.Agent, error) {
	row := s.db.QueryRow(
		`SELECT `+agentColumns+` FROM agents WHERE name=?`, name)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return a, err
}

// AuthenticateAgent 按 token 哈希换取启用中的 Agent。
func (s *Store) AuthenticateAgent(tokenHash string) (*domain.Agent, error) {
	row := s.db.QueryRow(
		`SELECT `+agentColumns+` FROM agents WHERE token_hash=?`, tokenHash)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !a.Enabled {
		return nil, domain.ErrAgentDisabled
	}
	a.ComputeOnline(nowUTC())
	return a, nil
}

// TouchAgent 更新最近出现时间（领取/心跳/认证时调用）。
func (s *Store) TouchAgent(id int64) error {
	_, err := s.db.Exec(`UPDATE agents SET last_seen_at=? WHERE id=?`, formatTime(nowUTC()), id)
	return err
}

// RotateAgentToken 更换 token 哈希。
func (s *Store) RotateAgentToken(id int64, tokenHash string) error {
	res, err := s.db.Exec(`UPDATE agents SET token_hash=? WHERE id=?`, tokenHash, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// SetAgentQuota 记录额度。只接受 unknown、ok、exhausted。
func (s *Store) SetAgentQuota(id int64, quota string) error {
	if !domain.ValidQuota(quota) {
		return errors.Join(domain.ErrInvalidInput, errors.New("额度值不合法"))
	}
	res, err := s.db.Exec(`UPDATE agents SET quota=? WHERE id=?`, quota, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// SetAgentProfile 写入这个 Agent 接哪一档、以及成本排序。
func (s *Store) SetAgentProfile(id int64, serves string, costRank int) error {
	switch serves {
	case domain.ServesSmall, domain.ServesFloor, domain.ServesLarge:
	default:
		return errors.Join(domain.ErrInvalidInput, errors.New("执行档位不合法"))
	}
	res, err := s.db.Exec(`UPDATE agents SET serves=?, cost_rank=? WHERE id=?`, serves, costRank, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// ApplyRoute 在派发前写入分类结果和指派，不改变状态。
func (s *Store) ApplyRoute(id int64, category, tier, reason string, assignee int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := queryTaskTx(tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE tasks SET category=?, tier=?, route_reason=?, assignee_agent_id=?, updated_at=? WHERE id=?`,
		category, tier, reason, assignee, formatTime(nowUTC()), id); err != nil {
		return err
	}
	changed := t.AssigneeAgentID == nil || *t.AssigneeAgentID != assignee
	if changed && t.AssigneeAgentID != nil {
		if err := insertEvent(tx, &domain.TaskEvent{
			TaskID:    id,
			ActorType: domain.ActorSystem,
			Type:      domain.EventReassign,
			Message:   reason,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Reassign 把待执行任务改派给另一名 Agent，并记一条 reassign 事件。
func (s *Store) Reassign(id, agentID int64, reason string) (*domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, err
	}
	if t.Status != domain.StatusTodo {
		return nil, domain.ErrInvalidStatus
	}
	if _, err := tx.Exec(
		`UPDATE tasks SET assignee_agent_id=?, route_reason=?, updated_at=? WHERE id=?`,
		agentID, reason, formatTime(nowUTC()), id); err != nil {
		return nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorSystem,
		Type:      domain.EventReassign,
		Message:   reason,
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ev, nil
}

// SetAgentEnabled 启用/禁用 Agent；禁用后无法再领取任务。
func (s *Store) SetAgentEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE agents SET enabled=? WHERE id=?`, v, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

func scanAgent(row interface {
	Scan(dest ...any) error
}) (*domain.Agent, error) {
	var a domain.Agent
	var enabled int
	var lastSeen, budgetAt sql.NullString
	var balance sql.NullFloat64
	var created string
	if err := row.Scan(&a.ID, &a.Name, &a.Kind, &enabled, &a.Quota, &a.Serves, &a.CostRank,
		&lastSeen, &created, &balance, &a.BalanceUnit, &budgetAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	a.Enabled = enabled == 1
	if balance.Valid {
		v := balance.Float64
		a.Balance = &v
	}
	if lastSeen.Valid && lastSeen.String != "" {
		t, err := parseTime(lastSeen.String)
		if err != nil {
			return nil, err
		}
		a.LastSeenAt = &t
	}
	if budgetAt.Valid && budgetAt.String != "" {
		if t, err := parseTime(budgetAt.String); err == nil {
			a.BudgetUpdatedAt = &t
		}
	}
	var err error
	if a.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------- Task 查询 ----------

// ListTasks 按过滤器列出任务。
//
// ⚠️ 返回的切片**永不为 nil**（用 make 初始化）。var 声明的 nil 切片会被
// JSON 序列化成 null，前端 .map 直接抛 TypeError —— 这是重构前的一个真实缺陷。
func (s *Store) ListTasks(f TaskFilter) ([]*domain.Task, error) {
	if err := f.Normalize(); err != nil {
		return nil, err
	}
	where, args := buildTaskWhere(f)
	q := `SELECT ` + taskColumns + ` FROM tasks WHERE ` + where + ` ORDER BY ` + f.sortExpr()
	if f.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Task, 0, 32)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountTasks 返回过滤器命中的总数，不受 Limit/Offset 影响。
// 分页接口用它填 total，避免前端靠 items.length 猜是否还有下一页。
func (s *Store) CountTasks(f TaskFilter) (int, error) {
	f.Limit, f.Offset = 0, 0
	if err := f.Normalize(); err != nil {
		return 0, err
	}
	where, args := buildTaskWhere(f)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE `+where, args...).Scan(&n)
	return n, err
}

// ArchiveTask 归档任务。归档后不出现在默认看板（archived=exclude），
// 可通过 archived=only 找回。返回归档时受影响的任务与事件。
func (s *Store) ArchiveTask(id int64) (*domain.Task, *domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if t.IsArchived() {
		// 重复归档视为幂等成功，不写重复事件。
		return t, nil, nil
	}
	if t.Status == domain.StatusDoing {
		// Agent 仍持有租约在干活：归档后它的心跳/提交照常成功，任务却从看板消失。
		return nil, nil, errors.Join(domain.ErrInvalidStatus, errors.New("执行中的任务不能归档，等 Agent 提交或退回后再归档"))
	}
	now := formatTime(nowUTC())
	res, err := tx.Exec(
		`UPDATE tasks SET archived_at=?, version=version+1, updated_at=? WHERE id=? AND archived_at IS NULL`,
		now, now, id)
	if err != nil {
		return nil, nil, err
	}
	if err := requireRow(res); err != nil {
		return nil, nil, err
	}
	// 归档后该任务不再参与编号，原位置留下一个空洞。
	// 重编号让列内保持 1..n 连续，避免空洞在反复归档/取消归档后累积成
	// 巨大的 sort_order（那时 MAX(sort_order) 会离"列末"越来越远）。
	ids, err := columnIDsTx(tx, t.Status)
	if err != nil {
		return nil, nil, err
	}
	if err := renumberColumnTx(tx, t.Status, ids); err != nil {
		return nil, nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorHuman,
		ActorName: "human",
		Type:      domain.EventArchived,
		Message:   "归档任务",
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return t, ev, nil
}

// UnarchiveTask 取消归档，把任务放回看板。
func (s *Store) UnarchiveTask(id int64) (*domain.Task, *domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if !t.IsArchived() {
		return t, nil, nil
	}
	now := formatTime(nowUTC())
	// 取消归档的任务要追加到列末，而不是回到它原来的 sort_order ——
	// 那个值可能已被别的任务占用（归档期间该列发生了重排或新建），
	// 直接复用会产生重复 sort_order，之后的 ORDER BY 顺序就不确定了。
	restored, err := nextSortOrderTx(tx, string(t.Status))
	if err != nil {
		return nil, nil, err
	}
	res, err := tx.Exec(
		`UPDATE tasks SET archived_at=NULL, version=version+1, sort_order=?, updated_at=? WHERE id=?`,
		restored, now, id)
	if err != nil {
		return nil, nil, err
	}
	if err := requireRow(res); err != nil {
		return nil, nil, err
	}
	// 写回 sort_order 后要 bump 整列 version：
	// 归档任务回到列表会让**整列**的下标都平移，
	// 其他客户端手上的乐观锁基线已经不准了。
	ids, err := columnIDsTx(tx, t.Status)
	if err != nil {
		return nil, nil, err
	}
	if err := renumberColumnTx(tx, t.Status, ids); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(
		`UPDATE tasks SET version=version+1 WHERE status=? AND archived_at IS NULL`,
		string(t.Status)); err != nil {
		return nil, nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorHuman,
		ActorName: "human",
		Type:      domain.EventUnarchived,
		Message:   "取消归档",
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return t, ev, nil
}

// GetTask 取单个任务。
func (s *Store) GetTask(id int64) (*domain.Task, error) {
	row := s.db.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id=?`, id)
	t, err := scanTaskRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

// CreateTask 创建任务，默认状态 idea。
func (s *Store) CreateTask(t *domain.Task) (*domain.TaskEvent, error) {
	if t.Status == "" {
		t.Status = domain.StatusIdea
	}
	now := formatTime(nowUTC())
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 新任务追加到目标列末尾，而不是留 0。
	//
	// 留 0 的话，sort_order=0 会排在所有正常值（1..n）之前 ——
	// 表现为"新建的任务总是跳到列首"，与"追加"的直觉相反，
	// 而且会让用户刚建的任务被后续重排挤到奇怪的位置。
	nextOrder, err := nextSortOrderTx(tx, string(t.Status))
	if err != nil {
		return nil, err
	}
	t.SortOrder = nextOrder

	// 挂项目前守卫：外键约束虽能拦，但错误会被 mapWriteError 归为参数不合法，
	// 而语义上"项目不存在"更接近 NotFound，显式检查给出准确错误。
	// ⚠️ 在事务内必须用 queryProjectTx，走 s.db 会自死锁（连接池只有 1）。
	if t.ProjectID != nil {
		p, err := queryProjectTx(tx, *t.ProjectID)
		if err != nil {
			return nil, err
		}
		if p.IsArchived() {
			return nil, errors.Join(domain.ErrInvalidInput, errors.New("项目已归档，不能挂载任务"))
		}
	}

	res, err := tx.Exec(
		`INSERT INTO tasks(title, description, acceptance_criteria, workdir, priority,
			status, assignee_agent_id, lease_until, version, sort_order, created_at, updated_at, project_id)
		 VALUES(?,?,?,?,?,?,?,NULL,1,?,?,?,?)`,
		t.Title, t.Description, t.AcceptanceCriteria, t.Workdir, t.Priority,
		string(t.Status), nullableID(t.AssigneeAgentID), nextOrder, now, now, nullableID(t.ProjectID))
	if err != nil {
		return nil, mapWriteError(err)
	}
	id, _ := res.LastInsertId()
	t.ID = id
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorHuman,
		ActorName: "human",
		Type:      domain.EventCreated,
		ToStatus:  ptrStatus(t.Status),
		Message:   "创建任务",
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ev, nil
}

// UpdateTaskMeta 人类编辑任务内容字段，version 乐观锁。
// projectID：nil 表示不过项目；指向值表示挂到该项目（须存在且未归档）。
func (s *Store) UpdateTaskMeta(id int64, title, desc, acceptance, workdir, priority string,
	assignee *int64, version int64, assignNote, category string, projectID *int64) (*domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, err
	}
	if t.Version != version {
		return nil, domain.ErrConflict
	}
	if t.Status == domain.StatusDoing || t.Status == domain.StatusReview {
		// 执行中/评审中的任务不允许改内容，避免与 Agent 产出脱节。
		return nil, domain.ErrInvalidStatus
	}
	if t.IsArchived() {
		// 已归档的任务不再参与日常工作，先取消归档再编辑。
		return nil, errors.Join(domain.ErrInvalidStatus, errors.New("任务已归档，请先取消归档"))
	}
	now := formatTime(nowUTC())
	tier := domain.DefaultTier(category)
	// 与 CreateTask 同理：显式守卫给出 NotFound 而非笼统的约束错误。
	// ⚠️ 事务内必须用 queryProjectTx，走 s.db 会自死锁。
	if projectID != nil {
		p, err := queryProjectTx(tx, *projectID)
		if err != nil {
			return nil, err
		}
		if p.IsArchived() {
			return nil, errors.Join(domain.ErrInvalidInput, errors.New("项目已归档，不能挂载任务"))
		}
	}
	res, err := tx.Exec(
		`UPDATE tasks SET title=?, description=?, acceptance_criteria=?, workdir=?, priority=?,
			assignee_agent_id=?, category=?, tier=?, project_id=?, version=version+1, updated_at=? WHERE id=? AND version=?`,
		title, desc, acceptance, workdir, priority, nullableID(assignee), category, tier, nullableID(projectID), now, id, version)
	if err != nil {
		return nil, err
	}
	if err := requireRow(res); err != nil {
		return nil, err
	}
	var changedAssignee bool
	if !sameNullableID(t.AssigneeAgentID, assignee) {
		changedAssignee = true
	}
	evType := domain.EventUpdated
	msg := "编辑任务内容"
	if changedAssignee {
		evType = domain.EventAssigned
		msg = assignNote
	}
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorHuman,
		ActorName: "human",
		Type:      evType,
		Message:   msg,
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ev, nil
}

// HumanTransition 人类发起的状态流转，同事务写事件。
//
// appendTo：拖拽落位下标，-1 表示追加到目标列末尾（未指定位置时的默认）。
func (s *Store) HumanTransition(id int64, to domain.Status, comment string, appendTo int) (*domain.Task, *domain.TaskEvent, error) {
	return s.statusTransition(id, to, domain.ActorHuman, nil, "human", comment, true, time.Time{}, appendTo)
}

// Claim 原子领取：仅当任务为 todo、指派给该 Agent 且 Agent 启用时成功。
func (s *Store) Claim(id, agentID int64, lease time.Duration) (*domain.Task, *domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if !domain.CanTransition(t.Status, domain.StatusDoing, false) {
		return nil, nil, domain.ErrInvalidStatus
	}
	if t.AssigneeAgentID == nil || *t.AssigneeAgentID != agentID {
		return nil, nil, domain.ErrNotAssigned
	}
	if t.IsArchived() {
		return nil, nil, errors.Join(domain.ErrInvalidStatus, errors.New("任务已归档"))
	}
	enabled, err := agentEnabledTx(tx, agentID)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return nil, nil, domain.ErrAgentDisabled
	}
	var quota string
	if err := tx.QueryRow(`SELECT quota FROM agents WHERE id=?`, agentID).Scan(&quota); err != nil {
		return nil, nil, err
	}
	if quota == domain.QuotaExhausted {
		return nil, nil, errors.Join(domain.ErrInvalidInput, errors.New("额度不足，不能领取"))
	}
	until := nowUTC().Add(lease)
	if err := execStatusUpdate(tx, id, domain.StatusDoing, &until, 1, AppendToEnd); err != nil {
		return nil, nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:     id,
		ActorType:  domain.ActorAgent,
		ActorID:    &agentID,
		Type:       domain.EventClaim,
		FromStatus: ptrStatus(domain.StatusTodo),
		ToStatus:   ptrStatus(domain.StatusDoing),
		Message:    "领取任务",
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(`UPDATE agents SET last_seen_at=? WHERE id=?`, formatTime(nowUTC()), agentID); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return t, ev, nil
}

// Heartbeat Agent 续租；必须是当前 doing 任务的持有者。
func (s *Store) Heartbeat(id, agentID int64, lease time.Duration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return err
	}
	if t.Status != domain.StatusDoing {
		return domain.ErrInvalidStatus
	}
	if t.AssigneeAgentID == nil || *t.AssigneeAgentID != agentID {
		return domain.ErrNotLeaseHolder
	}
	until := nowUTC().Add(lease)
	if _, err := tx.Exec(`UPDATE tasks SET lease_until=?, updated_at=? WHERE id=?`,
		formatTime(until), formatTime(nowUTC()), id); err != nil {
		return err
	}
	if err := insertEvent(tx, &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorAgent,
		ActorID:   &agentID,
		Type:      domain.EventHeartbeat,
		Message:   "心跳续租",
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE agents SET last_seen_at=? WHERE id=?`, formatTime(nowUTC()), agentID); err != nil {
		return err
	}
	return tx.Commit()
}

// Submit Agent 提交成果，doing -> review。不带 cost_used，保持旧调用兼容。
func (s *Store) Submit(id, agentID int64, summary string) (*domain.Task, *domain.TaskEvent, error) {
	return s.SubmitWithCost(id, agentID, summary, nil)
}

// SubmitWithCost 提交成果并可选记录实际成本（032-B）。
func (s *Store) SubmitWithCost(id, agentID int64, summary string, costUsed *float64) (*domain.Task, *domain.TaskEvent, error) {
	if err := ValidateCostUsed(costUsed); err != nil {
		return nil, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if t.Status != domain.StatusDoing {
		return nil, nil, domain.ErrInvalidStatus
	}
	if t.AssigneeAgentID == nil || *t.AssigneeAgentID != agentID {
		return nil, nil, domain.ErrNotLeaseHolder
	}
	if err := execStatusUpdate(tx, id, domain.StatusReview, nil, 1, AppendToEnd); err != nil {
		return nil, nil, err
	}
	var name string
	if err := tx.QueryRow(`SELECT name FROM agents WHERE id=?`, agentID).Scan(&name); err != nil {
		return nil, nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:     id,
		ActorType:  domain.ActorAgent,
		ActorID:    &agentID,
		ActorName:  name,
		Type:       domain.EventSubmit,
		FromStatus: ptrStatus(domain.StatusDoing),
		ToStatus:   ptrStatus(domain.StatusReview),
		Message:    summary,
	}
	if err := setTaskCostUsedTx(tx, id, costUsed); err != nil {
		return nil, nil, err
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return t, ev, nil
}

// Release Agent 放弃任务或执行失败退回，doing -> todo，保留指派人。
func (s *Store) Release(id, agentID int64, reason string) (*domain.Task, *domain.TaskEvent, error) {
	return s.ReleaseWithCost(id, agentID, reason, nil)
}

// ReleaseWithCost 退回任务并可选记录已消耗的实际成本（032-B）。
func (s *Store) ReleaseWithCost(id, agentID int64, reason string, costUsed *float64) (*domain.Task, *domain.TaskEvent, error) {
	if err := ValidateCostUsed(costUsed); err != nil {
		return nil, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if t.Status != domain.StatusDoing {
		return nil, nil, domain.ErrInvalidStatus
	}
	if t.AssigneeAgentID == nil || *t.AssigneeAgentID != agentID {
		return nil, nil, domain.ErrNotLeaseHolder
	}
	if err := execStatusUpdate(tx, id, domain.StatusTodo, nil, 1, AppendToEnd); err != nil {
		return nil, nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:     id,
		ActorType:  domain.ActorAgent,
		ActorID:    &agentID,
		Type:       domain.EventRelease,
		FromStatus: ptrStatus(domain.StatusDoing),
		ToStatus:   ptrStatus(domain.StatusTodo),
		Message:    reason,
	}
	if err := setTaskCostUsedTx(tx, id, costUsed); err != nil {
		return nil, nil, err
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return t, ev, nil
}

// AppendNote 追加备注（人类或 Agent）。
func (s *Store) AppendNote(id int64, actor domain.ActorType, actorID *int64, actorName, message string) (*domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := queryTaskTx(tx, id); err != nil {
		return nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: actor,
		ActorID:   actorID,
		ActorName: actorName,
		Type:      domain.EventNote,
		Message:   message,
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ev, nil
}

// ReapExpiredLeases 回收所有租约超时的 doing 任务，返回受影响任务与事件。
func (s *Store) ReapExpiredLeases() ([]ReapedLease, error) {
	now := formatTime(nowUTC())
	rows, err := s.db.Query(
		`SELECT id, assignee_agent_id FROM tasks
		 WHERE status='doing' AND lease_until IS NOT NULL AND lease_until < ?`, now)
	if err != nil {
		return nil, err
	}
	type pair struct{ id, agentID int64 }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.agentID); err != nil {
			rows.Close()
			return nil, err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []ReapedLease
	for _, p := range pairs {
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		// 上面的扫描在事务外，到这里之间 Agent 可能已提交（doing→review）或心跳续租。
		// 必须在本事务内按当前状态重新判定，否则会把刚提交的成果打回 todo。
		var expired int
		if err := tx.QueryRow(
			`SELECT COUNT(1) FROM tasks
			 WHERE id=? AND status='doing' AND lease_until IS NOT NULL AND lease_until < ?`,
			p.id, formatTime(nowUTC())).Scan(&expired); err != nil {
			tx.Rollback()
			return nil, err
		}
		if expired == 0 {
			tx.Rollback()
			continue
		}
		if err := execStatusUpdate(tx, p.id, domain.StatusTodo, nil, 1, AppendToEnd); err != nil {
			tx.Rollback()
			return nil, err
		}
		ev := &domain.TaskEvent{
			TaskID:     p.id,
			ActorType:  domain.ActorSystem,
			ActorID:    &p.agentID,
			ActorName:  "system",
			Type:       domain.EventLeaseExpire,
			FromStatus: ptrStatus(domain.StatusDoing),
			ToStatus:   ptrStatus(domain.StatusTodo),
			Message:    "租约超时，任务自动回收",
		}
		if err := insertEvent(tx, ev); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		out = append(out, ReapedLease{TaskID: p.id, Event: ev})
	}
	return out, nil
}

// ReapedLease 租约回收结果。
type ReapedLease struct {
	TaskID int64
	Event  *domain.TaskEvent
}

// EventFilter 事件时间线查询参数。
type EventFilter struct {
	TaskID int64
	// Page 从 1 开始。默认按 id DESC（新事件在前），
	// 配合前端"加载更早"的翻页，比 ASC + 无限滚动更符合时间线阅读方向。
	Page int
	Size int
	// Desc true = 新事件在前，false = 旧事件在前。
	Desc bool
	// WithHeartbeat 心跳事件默认折叠不展示；置 true 可查全部（诊断租约续租用）。
	WithHeartbeat bool
}

// MaxEventPageSize 单页事件条数上限。
const MaxEventPageSize = 200

// ListEventsPage 分页取事件时间线，返回当页数据与命中总数。
//
// 为什么要分页：原本 ListEvents 无 LIMIT 地返回全部事件，
// 任务跑久了时间线会有上千条，一次性序列化和前端全量渲染都会拖垮。
func (s *Store) ListEventsPage(f EventFilter) ([]*domain.TaskEvent, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size <= 0 || f.Size > MaxEventPageSize {
		f.Size = MaxEventPageSize
	}

	conds := []string{`task_id=?`}
	args := []any{f.TaskID}
	if !f.WithHeartbeat {
		conds = append(conds, `type != 'heartbeat'`)
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM task_events WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []*domain.TaskEvent{}, 0, nil
	}

	order := " ASC"
	if f.Desc {
		order = " DESC"
	}
	pageArgs := append(append([]any{}, args...), f.Size, (f.Page-1)*f.Size)
	rows, err := s.db.Query(
		`SELECT id, task_id, actor_type, actor_id, actor_name, type, from_status,
		        to_status, message, created_at
		 FROM task_events WHERE `+where+` ORDER BY id`+order+` LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	// make 而非 var：nil 切片序列化成 null。
	out := make([]*domain.TaskEvent, 0, f.Size)
	for rows.Next() {
		var ev domain.TaskEvent
		var actorID sql.NullInt64
		var from, to sql.NullString
		var actorType, createdAt string
		if err := rows.Scan(&ev.ID, &ev.TaskID, &actorType, &actorID, &ev.ActorName,
			&ev.Type, &from, &to, &ev.Message, &createdAt); err != nil {
			return nil, 0, err
		}
		ev.ActorType = domain.ActorType(actorType)
		if actorID.Valid {
			v := actorID.Int64
			ev.ActorID = &v
		}
		if from.Valid && from.String != "" {
			st := domain.Status(from.String)
			ev.FromStatus = &st
		}
		if to.Valid && to.String != "" {
			st := domain.Status(to.String)
			ev.ToStatus = &st
		}
		if ev.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, 0, err
		}
		out = append(out, &ev)
	}
	return out, total, rows.Err()
}

// ListEvents 取任务全部事件（心跳事件折叠不展示）。
// 保留给 MCP / 内部调用；HTTP 端走 ListEventsPage。
func (s *Store) ListEvents(taskID int64) ([]*domain.TaskEvent, error) {
	items, _, err := s.ListEventsPage(EventFilter{TaskID: taskID, Desc: true})
	return items, err
}

// ---------- 事务辅助 ----------

// statusTransition 是状态流转的唯一实现。
//
// appendTo 控制跨列后的落位下标（0 基）：
//   - -1  = 追加到目标列末尾。Agent 领取/提交、系统租约回收走这条，
//     它们没有"用户放在第几位"的概念。
//   - >=0 = 拖拽落位的具体位置。前端拖到某张卡片上时传该卡片的下标。
func (s *Store) statusTransition(id int64, to domain.Status, actor domain.ActorType,
	actorID *int64, actorName, message string, human bool, leaseUntil time.Time, appendTo int,
) (*domain.Task, *domain.TaskEvent, error) {
	if !domain.IsValidStatus(string(to)) {
		return nil, nil, domain.ErrInvalidInput
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	// 先判状态机再判守卫：流转本身非法时返回 ErrInvalidStatus，
	// 而不是让守卫的错误码掩盖真正的原因（"不能这么走" 比 "还差什么" 更优先）。
	if !domain.CanTransition(t.Status, to, human) {
		return nil, nil, domain.ErrInvalidStatus
	}
	if t.IsArchived() {
		// 已归档任务不在 Agent 视野内，流转出去也无人处理。
		return nil, nil, errors.Join(domain.ErrInvalidStatus, errors.New("任务已归档，请先取消归档"))
	}
	claimable := false
	if t.AssigneeAgentID != nil {
		if claimable, err = agentEnabledTx(tx, *t.AssigneeAgentID); err != nil {
			return nil, nil, err
		}
	}
	// 前置条件统一走 domain.CheckTransitionGuards。
	//
	// 原来这里是两段手写的 if（"idea→todo 必须已指派"、"review→todo 必须带原因"），
	// 而 meta 只暴露了 CanTransition 生成的流转图、不含这两条规则，
	// 于是前端看到"idea 可以拖到 todo"，拖过去却被 409 拒绝 —— 拖拽实质失效。
	// 现在守卫与流转图同源生成，契约说的和服务端做的不会再漂移。
	guards := domain.TransitionGuards(t.Status, to, human)
	if g := domain.CheckTransitionGuards(guards, claimable, message); g != "" {
		if g == domain.GuardRequiresAssignee {
			// 指派为空时必须让用户知道下一步做什么，
			// 而不只是"未指派"三个字。
			return nil, nil, errors.Join(domain.ErrNotAssigned,
				errors.New(domain.GuardErrorMessage(g)))
		}
		return nil, nil, errors.Join(domain.ErrInvalidInput,
			errors.New(domain.GuardErrorMessage(g)))
	}
	var lease *time.Time
	if !leaseUntil.IsZero() {
		lease = &leaseUntil
	}
	// appendTo：拖拽落位的具体位置。非拖拽路径传 -1，追加到目标列末尾。
	if err := execStatusUpdate(tx, id, to, lease, 1, appendTo); err != nil {
		return nil, nil, err
	}
	evType := domain.EventNote
	switch {
	case actor == domain.ActorSystem:
		evType = domain.EventLeaseExpire
	case to == domain.StatusApproved:
		evType = domain.EventApprove
	case to == domain.StatusTodo && t.Status == domain.StatusReview:
		evType = domain.EventReject
	case to == domain.StatusTodo && t.Status == domain.StatusIdea:
		evType = domain.EventDispatch
	case to == domain.StatusIdea && t.Status == domain.StatusTodo:
		evType = domain.EventUpdated
	default:
		evType = domain.EventNote
	}
	ev := &domain.TaskEvent{
		TaskID:     id,
		ActorType:  actor,
		ActorID:    actorID,
		ActorName:  actorName,
		Type:       evType,
		FromStatus: ptrStatus(t.Status),
		ToStatus:   ptrStatus(to),
		Message:    message,
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	// 032-B：终审进入 approved 时结算余额。与状态流转同事务，
	// approved 是终态，因此天然幂等，不会重复扣减。
	if to == domain.StatusApproved {
		if err := settleTaskCostTx(tx, id); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return t, ev, nil
}

func queryTaskTx(tx *sql.Tx, id int64) (*domain.Task, error) {
	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id=?`, id)
	t, err := scanTaskRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

// execStatusUpdate 改状态并顺手把 sort_order 归位。
//
// 为什么重编号必须内聚在这里，而不是交给调用方：
// 共有 5 处调用（人类流转、Agent 领取、提交、退回、租约回收），
// 只要有一处忘记，新列里就会留下 sort_order=0 的孤儿 ——
// 它排在所有 1..n 之前，表现为"任务莫名跳到列首"，且极难定位。
// 放在这里，"改状态"与"重新编号"就是一个不可分割的原子操作。
//
// appendTo：新列中的落位下标，-1 表示追加到末尾。
func execStatusUpdate(tx *sql.Tx, id int64, to domain.Status, leaseUntil *time.Time,
	bumpVersion int64, appendTo int,
) error {
	// 先读旧状态：重编号要知道源列是谁。
	var from domain.Status
	if err := tx.QueryRow(`SELECT status FROM tasks WHERE id=?`, id).Scan(&from); err != nil {
		return err
	}
	var lease any
	if leaseUntil != nil {
		lease = formatTime(*leaseUntil)
	}

	if from != to {
		// ⚠️ 顺序不能调换：MoveIntoColumn 依赖"任务还在源列"来定位自己，
		// 先改 status 的话它会被目标列的查询当成既有成员，
		// 结果是目标列凭空多出一条、sort_order 整体错位。
		if err := MoveIntoColumn(tx, from, to, id, appendTo); err != nil {
			return err
		}
	}

	res, err := tx.Exec(
		`UPDATE tasks SET status=?, lease_until=?, version=version+?, updated_at=? WHERE id=?`,
		string(to), lease, bumpVersion, formatTime(nowUTC()), id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

func insertEvent(tx *sql.Tx, ev *domain.TaskEvent) error {
	now := formatTime(nowUTC())
	res, err := tx.Exec(
		`INSERT INTO task_events(task_id, actor_type, actor_id, actor_name, type,
			from_status, to_status, message, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		ev.TaskID, string(ev.ActorType), nullableID(ev.ActorID), ev.ActorName, string(ev.Type),
		nullableStatus(ev.FromStatus), nullableStatus(ev.ToStatus), ev.Message, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	ev.ID = id
	ev.CreatedAt = nowUTC()
	return nil
}

func agentEnabledTx(tx *sql.Tx, id int64) (bool, error) {
	var enabled int
	if err := tx.QueryRow(`SELECT enabled FROM agents WHERE id=?`, id).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, domain.ErrNotFound
		}
		return false, err
	}
	return enabled == 1, nil
}

// ---------- 杂项 ----------

func placeholders(n int) string {
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

func nullableID(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

func sameNullableID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func ptrStatus(s domain.Status) *domain.Status { return &s }

func nullableStatus(s *domain.Status) any {
	if s == nil {
		return nil
	}
	return string(*s)
}

func requireRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func mapWriteError(err error) error {
	// modernc/sqlite 唯一约束错误文本含 SQLITE_CONSTRAINT_UNIQUE。
	if err != nil && (contains(err.Error(), "UNIQUE") || contains(err.Error(), "constraint")) {
		return fmt.Errorf("%w: 名称冲突或字段约束失败", domain.ErrInvalidInput)
	}
	return err
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
