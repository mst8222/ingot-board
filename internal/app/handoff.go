package app

import (
	"errors"
	"strings"

	"github.com/mst8222/ingot-board/internal/dispatcher"
	"github.com/mst8222/ingot-board/internal/domain"
)

// 028 通用转交：总管只给排序建议，最终目标始终由人选定。

// HandoffCandidates 返回按能力与额度排序的推荐目标（纯查询，不改任务）。
func (s *Service) HandoffCandidates(taskID int64) ([]dispatcher.HandoffCandidate, error) {
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	pool, err := s.dispatcherCandidates()
	if err != nil {
		return nil, err
	}
	return dispatcher.RecommendHandoff(t.Category, t.Tier, t.AssigneeAgentID, pool), nil
}

// Handoff 人类显式转交。人选与总管推荐第一名不同时只在事件里留痕，
// 不阻止、不覆盖人的决定。
func (s *Service) Handoff(taskID, targetAgentID int64, reason string) (*domain.Task, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("转交原因不能为空"))
	}
	if targetAgentID <= 0 {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("目标执行端不能为空"))
	}
	note := ""
	recs, err := s.HandoffCandidates(taskID)
	if err != nil {
		return nil, err
	}
	if len(recs) > 0 && recs[0].AgentID != targetAgentID {
		note = "；人选与总管推荐第一名不同"
	}
	t, ev, err := s.store.Handoff(taskID, targetAgentID, reason+note)
	if err != nil {
		return nil, err
	}
	if ev != nil {
		s.publish(taskID, ev)
	}
	return t, nil
}
