package store

import (
	"database/sql"
	"errors"
	"math"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 032-B 数值预算闭环。
//
// 流水只在「余额真实变化」时写：人类录入 / 校准 / 清空、终审结算、探测归零。
// 采纳派发建议不改变余额，因此不写流水 —— 避免流水里塞满 delta=0 的噪声。
// balance 为 NULL 表示未知：硬门禁跳过、结算跳过，任何路径都不得把它伪造为 0。
const (
	ReasonHumanSet       = "human_set"
	ReasonHumanClear     = "human_clear"
	ReasonTaskSettled    = "task_settled"
	ReasonProbeExhausted = "probe_exhausted"
)

// BudgetEntry 是一笔余额变动的不可变记录。
type BudgetEntry struct {
	ID        int64   `json:"id"`
	AgentID   int64   `json:"agent_id"`
	TaskID    *int64  `json:"task_id"`
	Delta     float64 `json:"delta"`
	Reason    string  `json:"reason"`
	ActorType string  `json:"actor_type"`
	CreatedAt string  `json:"created_at"`
}

// ValidateCostUsed 校验 Agent 可选上报的成本。nil 表示未上报，合法。
func ValidateCostUsed(cost *float64) error {
	if cost == nil {
		return nil
	}
	if *cost < 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) {
		return errors.Join(domain.ErrInvalidInput, errors.New("cost_used 必须是不小于 0 的有限数"))
	}
	return nil
}

func insertBudgetTx(tx *sql.Tx, e *BudgetEntry) error {
	_, err := tx.Exec(
		`INSERT INTO budget_ledger(agent_id, task_id, delta, reason, actor_type, created_at)
		 VALUES(?,?,?,?,?,?)`,
		e.AgentID, nullableID(e.TaskID), e.Delta, e.Reason, e.ActorType, formatTime(nowUTC()))
	return err
}

// ListBudgetLedger 倒序返回某执行端的流水。
func (s *Store) ListBudgetLedger(agentID int64, limit int) ([]BudgetEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, agent_id, task_id, delta, reason, actor_type, created_at
		 FROM budget_ledger WHERE agent_id=? ORDER BY id DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BudgetEntry, 0)
	for rows.Next() {
		var e BudgetEntry
		var tid sql.NullInt64
		if err := rows.Scan(&e.ID, &e.AgentID, &tid, &e.Delta, &e.Reason, &e.ActorType, &e.CreatedAt); err != nil {
			return nil, err
		}
		if tid.Valid {
			v := tid.Int64
			e.TaskID = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetAgentBalance 人类录入 / 校准余额；balance 为 nil 表示清回未知。
// delta 是「相对旧值的变化」：首次录入按全额记，清空记 0 并留一条审计行。
func (s *Store) SetAgentBalance(agentID int64, balance *float64) (*domain.Agent, error) {
	if balance != nil {
		if err := ValidateCostUsed(balance); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var old sql.NullFloat64
	err = tx.QueryRow(`SELECT balance FROM agents WHERE id=?`, agentID).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	delta, reason := 0.0, ReasonHumanClear
	if balance != nil {
		reason = ReasonHumanSet
		delta = *balance
		if old.Valid {
			delta = *balance - old.Float64
		}
	}
	if _, err := tx.Exec(
		`UPDATE agents SET balance=?, budget_updated_at=? WHERE id=?`,
		balance, formatTime(nowUTC()), agentID); err != nil {
		return nil, err
	}
	if err := insertBudgetTx(tx, &BudgetEntry{
		AgentID: agentID, Delta: delta, Reason: reason,
		ActorType: string(domain.ActorHuman),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAgent(agentID)
}

// ZeroBalanceOnExhausted 是 ProbeDeepSeek 探测到 exhausted 时的唯一余额写入。
// ok / unknown 不得调用它，避免把三态额度伪装成数值余额。
func (s *Store) ZeroBalanceOnExhausted(agentID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var old sql.NullFloat64
	err = tx.QueryRow(`SELECT balance FROM agents WHERE id=?`, agentID).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if old.Valid && old.Float64 == 0 {
		return nil
	}
	delta := 0.0
	if old.Valid {
		delta = -old.Float64
	}
	zero := 0.0
	if _, err := tx.Exec(
		`UPDATE agents SET balance=?, budget_updated_at=? WHERE id=?`,
		&zero, formatTime(nowUTC()), agentID); err != nil {
		return err
	}
	if err := insertBudgetTx(tx, &BudgetEntry{
		AgentID: agentID, Delta: delta, Reason: ReasonProbeExhausted,
		ActorType: string(domain.ActorSystem),
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// settleTaskCostTx 在人类 approved 的同一事务里结算。
//
// 顺序很重要：状态机与守卫先通过，事件写完再结算，任何一步失败整笔回滚。
// 余额未知时直接返回，既不扣减也不写流水；成本为 0 时同理。
func settleTaskCostTx(tx *sql.Tx, taskID int64) error {
	var assignee sql.NullInt64
	var costUsed, estimated sql.NullFloat64
	if err := tx.QueryRow(
		`SELECT assignee_agent_id, cost_used, estimated_cost FROM tasks WHERE id=?`, taskID,
	).Scan(&assignee, &costUsed, &estimated); err != nil {
		return err
	}
	if !assignee.Valid {
		return nil
	}
	cost := 0.0
	if costUsed.Valid {
		cost = costUsed.Float64
	} else if estimated.Valid {
		cost = estimated.Float64
	}
	if cost <= 0 {
		return nil
	}
	var balance sql.NullFloat64
	err := tx.QueryRow(`SELECT balance FROM agents WHERE id=?`, assignee.Int64).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !balance.Valid {
		return nil // 未知余额：不伪造、不扣减
	}
	if _, err := tx.Exec(
		`UPDATE agents SET balance=?, budget_updated_at=? WHERE id=?`,
		balance.Float64-cost, formatTime(nowUTC()), assignee.Int64); err != nil {
		return err
	}
	tid := taskID
	return insertBudgetTx(tx, &BudgetEntry{
		AgentID: assignee.Int64, TaskID: &tid, Delta: -cost,
		Reason: ReasonTaskSettled, ActorType: string(domain.ActorHuman),
	})
}

// setTaskCostUsedTx 记录 Agent 可选上报的实际成本。nil = 未上报，保留旧值。
func setTaskCostUsedTx(tx *sql.Tx, taskID int64, cost *float64) error {
	if cost == nil {
		return nil
	}
	_, err := tx.Exec(`UPDATE tasks SET cost_used=? WHERE id=?`, *cost, taskID)
	return err
}
