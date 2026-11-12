package app

import (
	"github.com/mst8222/ingot-board/internal/dispatcher"
	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/store"
)

// 032 总管对人类端暴露的动作。全部只在 humanRoutes 上，Agent 端零新增端点。

func (s *Service) DispatchMode() (string, error) {
	if s.resolveMode != nil {
		return s.currentMode(), nil
	}
	return s.store.GetDispatchMode()
}

func (s *Service) SetDispatchMode(mode string) error {
	if mode != dispatcher.ModeObserve && mode != dispatcher.ModeAuto {
		return domain.ErrInvalidInput
	}
	return s.store.SetDispatchMode(mode)
}

func (s *Service) Suggestion(taskID int64) (*store.DispatchDecision, error) {
	return s.store.LatestDecision(taskID)
}

// AcceptSuggestion 校验建议属于该任务后，走 store 的单事务采纳并广播 SSE。
func (s *Service) AcceptSuggestion(taskID, decisionID int64) (*domain.Task, error) {
	d, err := s.store.GetDecision(decisionID)
	if err != nil {
		return nil, err
	}
	if d.TaskID != taskID {
		return nil, domain.ErrNotFound
	}
	t, ev, err := s.store.ApplyAcceptedDecision(decisionID)
	if err != nil {
		return nil, err
	}
	if ev != nil {
		s.publish(taskID, ev)
	}
	return t, nil
}

// SetAgentBalance 人类录入 / 校准 / 清空数值余额。清空用 nil 表示「回到未知」。
func (s *Service) SetAgentBalance(agentID int64, balance *float64) (*domain.Agent, error) {
	a, err := s.store.SetAgentBalance(agentID, balance)
	if err != nil {
		return nil, err
	}
	s.publishAgent(a)
	return a, nil
}

// Ledger 返回某执行端的余额流水（倒序）。
func (s *Service) Ledger(agentID int64) ([]store.BudgetEntry, error) {
	return s.store.ListBudgetLedger(agentID, 50)
}
