package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mst8222/ingot-board/internal/domain"
)

// Handoff 是人类显式发起的通用转交（028）。
//
// 与 011 的 Reassign 刻意分开：Reassign 只在额度耗尽时由系统调用、
// 只作用 todo、actor 为 system；Handoff 由人发起、覆盖 todo/review、
// 校验目标状态、递增 version，并在事件里写清来源与目标。
// 两者共用 EventReassign 事件类型，但 011 的行为一行未改。
func (s *Store) Handoff(id, targetAgentID int64, reason string) (*domain.Task, *domain.TaskEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if t.Status != domain.StatusTodo && t.Status != domain.StatusReview {
		return nil, nil, errors.Join(domain.ErrHandoffNotAllowed,
			errors.New("只有待执行与待评审的任务可以转交"))
	}
	if t.IsArchived() {
		return nil, nil, errors.Join(domain.ErrHandoffNotAllowed,
			errors.New("已归档任务不能转交"))
	}

	var targetName string
	var enabled int
	err = tx.QueryRow(`SELECT name, enabled FROM agents WHERE id=?`, targetAgentID).
		Scan(&targetName, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if enabled != 1 {
		return nil, nil, errors.Join(domain.ErrAgentDisabled,
			errors.New("目标执行端已禁用"))
	}

	fromName := "未指派"
	if t.AssigneeAgentID != nil {
		if *t.AssigneeAgentID == targetAgentID {
			return nil, nil, errors.Join(domain.ErrInvalidInput,
				errors.New("目标执行端与当前指派相同"))
		}
		if err := tx.QueryRow(`SELECT name FROM agents WHERE id=?`, *t.AssigneeAgentID).
			Scan(&fromName); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
	}

	if _, err := tx.Exec(
		`UPDATE tasks SET assignee_agent_id=?, route_reason=?, version=version+1, updated_at=? WHERE id=?`,
		targetAgentID, reason, formatTime(nowUTC()), id); err != nil {
		return nil, nil, err
	}
	ev := &domain.TaskEvent{
		TaskID:    id,
		ActorType: domain.ActorHuman,
		ActorName: "human",
		Type:      domain.EventReassign,
		Message:   fmt.Sprintf("转交 %s → %s：%s", fromName, targetName, reason),
	}
	if err := insertEvent(tx, ev); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	task, err := s.GetTask(id)
	if err != nil {
		return nil, nil, err
	}
	return task, ev, nil
}
