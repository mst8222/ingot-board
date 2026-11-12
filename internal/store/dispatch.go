package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
)

const (
	DispatchModeObserve = "observe"
	DispatchModeAuto    = "auto"

	DecisionPending    = "pending"
	DecisionAccepted   = "accepted"
	DecisionStale      = "stale"
	DecisionSuperseded = "superseded"
)

// DispatchDecision 是一次总管决策的不可变快照（032）。
// 采纳前会拿 SnapshotJSON 与执行端当前状态重验，避免派给已经变脏的目标。
type DispatchDecision struct {
	ID            int64   `json:"id"`
	TaskID        int64   `json:"task_id"`
	Mode          string  `json:"mode"`
	Category      string  `json:"category"`
	Tier          string  `json:"tier"`
	ChosenAgentID *int64  `json:"chosen_agent_id"`
	CostEstimate  float64 `json:"cost_estimate"`
	ScoreTotal    float64 `json:"score_total"`
	ScoreJSON     string  `json:"score_json"`
	SnapshotJSON  string  `json:"snapshot_json"`
	Reason        string  `json:"reason"`
	Source        string  `json:"source"`
	ScheduleID    *int64  `json:"schedule_id"`
	Status        string  `json:"status"`
	ObservedAt    string  `json:"observed_at"`
	ExpiresAt     string  `json:"expires_at"`
	AcceptedAt    string  `json:"accepted_at"`
}

// DecisionSnapshot 记录生成建议那一刻执行端的关键状态。
type DecisionSnapshot struct {
	Enabled  bool     `json:"enabled"`
	Quota    string   `json:"quota"`
	Balance  *float64 `json:"balance"`
	CostRank int      `json:"cost_rank"`
}

func ValidDispatchMode(mode string) bool {
	return mode == DispatchModeObserve || mode == DispatchModeAuto
}

// GetDispatchMode 读全局双模开关。缺省行缺失时按 observe 兜底，永不报错阻断派发。
func (s *Store) GetDispatchMode() (string, error) {
	var mode string
	err := s.db.QueryRow(`SELECT mode FROM dispatch_settings WHERE id=1`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return DispatchModeObserve, nil
	}
	if err != nil {
		return DispatchModeObserve, err
	}
	if !ValidDispatchMode(mode) {
		return DispatchModeObserve, nil
	}
	return mode, nil
}

func (s *Store) SetDispatchMode(mode string) error {
	if !ValidDispatchMode(mode) {
		return errors.Join(domain.ErrInvalidInput, errors.New("派发模式只能是 observe 或 auto"))
	}
	res, err := s.db.Exec(`UPDATE dispatch_settings SET mode=? WHERE id=1`, mode)
	if err != nil {
		return err
	}
	return requireRow(res)
}

const decisionColumns = `id, task_id, mode, category, tier, chosen_agent_id, cost_estimate,
	score_total, score_json, snapshot_json, reason, source, schedule_id, status,
	observed_at, expires_at, accepted_at`

func scanDecision(row interface{ Scan(dest ...any) error }) (*DispatchDecision, error) {
	var d DispatchDecision
	var chosen, schedule sql.NullInt64
	var accepted sql.NullString
	if err := row.Scan(
		&d.ID, &d.TaskID, &d.Mode, &d.Category, &d.Tier, &chosen, &d.CostEstimate,
		&d.ScoreTotal, &d.ScoreJSON, &d.SnapshotJSON, &d.Reason, &d.Source, &schedule,
		&d.Status, &d.ObservedAt, &d.ExpiresAt, &accepted,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if chosen.Valid {
		v := chosen.Int64
		d.ChosenAgentID = &v
	}
	if schedule.Valid {
		v := schedule.Int64
		d.ScheduleID = &v
	}
	if accepted.Valid {
		d.AcceptedAt = accepted.String
	}
	return &d, nil
}

// RecordDecision 落一条 pending 建议，并把同一任务上更早的 pending 标为 superseded。
func (s *Store) RecordDecision(d *DispatchDecision) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE dispatch_decisions SET status=? WHERE task_id=? AND status=?`,
		DecisionSuperseded, d.TaskID, DecisionPending); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO dispatch_decisions
		(task_id, mode, category, tier, chosen_agent_id, cost_estimate, score_total,
		 score_json, snapshot_json, reason, source, schedule_id, status, observed_at, expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.TaskID, d.Mode, d.Category, d.Tier, d.ChosenAgentID, d.CostEstimate,
		d.ScoreTotal, d.ScoreJSON, d.SnapshotJSON, d.Reason, d.Source, d.ScheduleID,
		DecisionPending, d.ObservedAt, d.ExpiresAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) GetDecision(id int64) (*DispatchDecision, error) {
	return scanDecision(s.db.QueryRow(`SELECT `+decisionColumns+` FROM dispatch_decisions WHERE id=?`, id))
}

// LatestDecision 返回该任务最新一条仍有效（未过期）的 pending 建议。
func (s *Store) LatestDecision(taskID int64) (*DispatchDecision, error) {
	return scanDecision(s.db.QueryRow(
		`SELECT `+decisionColumns+` FROM dispatch_decisions
		 WHERE task_id=? AND status=? AND expires_at>? ORDER BY id DESC LIMIT 1`,
		taskID, DecisionPending, formatTime(nowUTC())))
}

// CountDoingByAgent 总管打分用的负载信号。
func (s *Store) CountDoingByAgent(agentID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE status=? AND assignee_agent_id=?`,
		string(domain.StatusDoing), agentID).Scan(&n)
	return n, err
}

// ApplyAcceptedDecision 是「一键采纳并派发」的单事务实现：
// 重验建议 → 写分类与指派 → idea→todo 与 dispatch 事件 → 标记建议已接受。
// 事务内不做任何网络调用；任一步失败全部回滚。
func (s *Store) ApplyAcceptedDecision(decisionID int64) (*domain.Task, *domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	d, err := scanDecision(tx.QueryRow(`SELECT `+decisionColumns+` FROM dispatch_decisions WHERE id=?`, decisionID))
	if err != nil {
		return nil, nil, err
	}
	t, err := queryTaskTx(tx, d.TaskID)
	if err != nil {
		return nil, nil, err
	}
	// 幂等：已经采纳过就直接返回当前任务，不重复写事件。
	if d.Status != DecisionPending {
		return t, nil, nil
	}
	if d.ExpiresAt <= formatTime(nowUTC()) {
		return nil, nil, domain.ErrSuggestionStale
	}
	if d.ChosenAgentID == nil {
		return nil, nil, domain.ErrSuggestionStale
	}
	if t.Status == domain.StatusTodo && t.AssigneeAgentID != nil {
		return t, nil, nil
	}
	if t.Status != domain.StatusIdea || t.AssigneeAgentID != nil {
		return nil, nil, domain.ErrSuggestionStale
	}

	// 重验执行端：启用状态、额度、余额与生成建议时的快照必须一致。
	var enabled int
	var quota string
	var balance sql.NullFloat64
	var costRank int
	err = tx.QueryRow(
		`SELECT enabled, quota, balance, cost_rank FROM agents WHERE id=?`, *d.ChosenAgentID,
	).Scan(&enabled, &quota, &balance, &costRank)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, domain.ErrSuggestionStale
	}
	if err != nil {
		return nil, nil, err
	}
	current := DecisionSnapshot{Enabled: enabled == 1, Quota: quota, CostRank: costRank}
	if balance.Valid {
		v := balance.Float64
		current.Balance = &v
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return nil, nil, err
	}
	if string(raw) != d.SnapshotJSON || !current.Enabled || current.Quota == domain.QuotaExhausted {
		return nil, nil, domain.ErrSuggestionStale
	}
	if current.Balance != nil && *current.Balance < d.CostEstimate {
		return nil, nil, domain.ErrInsufficientBudget
	}

	if _, err := tx.Exec(
		`UPDATE tasks SET category=?, tier=?, route_reason=?, assignee_agent_id=?,
		 estimated_cost=?, updated_at=? WHERE id=?`,
		d.Category, d.Tier, d.Reason, *d.ChosenAgentID, d.CostEstimate,
		formatTime(nowUTC()), d.TaskID); err != nil {
		return nil, nil, err
	}
	if err := execStatusUpdate(tx, d.TaskID, domain.StatusTodo, nil, 1, -1); err != nil {
		return nil, nil, err
	}
	from, to := domain.StatusIdea, domain.StatusTodo
	ev := &domain.TaskEvent{
		TaskID: d.TaskID, ActorType: domain.ActorHuman, ActorName: "human",
		Type: domain.EventDispatch, FromStatus: &from, ToStatus: &to,
		Message: fmt.Sprintf("总管建议已采纳：%s", d.Reason),
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	res, err := tx.Exec(
		`UPDATE dispatch_decisions SET status=?, accepted_at=? WHERE id=? AND status=?`,
		DecisionAccepted, formatTime(nowUTC()), decisionID, DecisionPending)
	if err != nil {
		return nil, nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, nil, domain.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	task, err := s.GetTask(d.TaskID)
	if err != nil {
		return nil, nil, err
	}
	return task, ev, nil
}

// SetTaskEstimatedCost 记录总管预估成本。auto 派发时由 routeIdea 调用，
// observe 采纳时由 ApplyAcceptedDecision 在同一事务里写入。
func (s *Store) SetTaskEstimatedCost(id int64, cost float64) error {
	res, err := s.db.Exec(`UPDATE tasks SET estimated_cost=? WHERE id=?`, cost, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// DecisionExpiresIn 建议的有效期。短到不会跨过一次人工确认，长到够人读完评分卡。
const DecisionExpiresIn = 10 * time.Minute

// NewDecisionTimes 返回建议的观察时间与过期时间，保持与 SQLite 时间字符串同格式。
func NewDecisionTimes() (observed, expires string) {
	now := nowUTC()
	return formatTime(now), formatTime(now.Add(DecisionExpiresIn))
}
