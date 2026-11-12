package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mst8222/ingot-board/internal/dispatcher"
	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/hosts"
	"github.com/mst8222/ingot-board/internal/routing"
	"github.com/mst8222/ingot-board/internal/store"
)

// UseNetworkRouting 让派发去试本机和云端的决策模型，并探测 DeepSeek 额度。
func (s *Service) UseNetworkRouting() {
	s.classify = func(ctx context.Context, title, description, workdir string) routing.Decision {
		return routing.Decide(ctx, title, description, workdir, routing.NetworkConfig())
	}
	s.probeFloor = routing.ProbeDeepSeek
}

func (s *Service) decide(task *domain.Task) routing.Decision {
	if task.Category != "" && domain.ValidCategory(task.Category) {
		return routing.Decision{Category: task.Category, Tier: domain.DefaultTier(task.Category), Reason: "人类指定类别"}
	}
	if s.classify != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return s.classify(ctx, task.Title, task.Description, task.Workdir)
	}
	return routing.Keywords(task.Title + "\n" + task.Description)
}

func (s *Service) candidates() ([]routing.Candidate, error) {
	agents, err := s.store.ListAgents()
	if err != nil {
		return nil, err
	}
	out := make([]routing.Candidate, 0, len(agents))
	for _, a := range agents {
		out = append(out, routing.Candidate{
			ID: a.ID, Name: a.Name, Enabled: a.Enabled,
			Quota: a.Quota, Serves: a.Serves, CostRank: a.CostRank,
		})
	}
	return out, nil
}

// dispatcherCandidates 给 032 总管补齐 011 候选里没有的信号：
// 静态能力标签、在线状态、doing 负载与数值余额。
func (s *Service) dispatcherCandidates() ([]dispatcher.Candidate, error) {
	agents, err := s.store.ListAgents()
	if err != nil {
		return nil, err
	}
	out := make([]dispatcher.Candidate, 0, len(agents))
	for _, a := range agents {
		var caps []string
		if spec, ok := hosts.Lookup(a.Name); ok {
			caps = spec.Capabilities
		}
		load, err := s.store.CountDoingByAgent(a.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, dispatcher.Candidate{
			ID: a.ID, Name: a.Name, Enabled: a.Enabled, Quota: a.Quota,
			Serves: a.Serves, CostRank: a.CostRank, Capabilities: caps,
			Online: a.Online, Load: load, Balance: a.Balance,
		})
	}
	return out, nil
}

func (s *Service) currentMode() string {
	if s.resolveMode != nil {
		m := s.resolveMode()
		if dispatcher.ModeObserve == m || dispatcher.ModeAuto == m {
			return m
		}
		return dispatcher.ModeObserve
	}
	m, err := s.store.GetDispatchMode()
	if err != nil {
		return dispatcher.ModeObserve
	}
	return m
}

// routeOptions 是一次派发的上下文覆盖，只由 031 调度器使用。
// 零值就是 032 原有行为：读全局双模、决策来源标 manual、不关联计划。
// 覆盖只作用于本次调用，绝不改 dispatch_settings，也不改 032 的函数签名。
type routeOptions struct {
	ModeOverride string
	Source       string
	ScheduleID   *int64
}

func (o routeOptions) mode(s *Service) string {
	if o.ModeOverride == dispatcher.ModeObserve || o.ModeOverride == dispatcher.ModeAuto {
		return o.ModeOverride
	}
	return s.currentMode()
}

func (o routeOptions) source() string {
	if o.Source != "" {
		return o.Source
	}
	return "manual"
}

// routeIdea 在 idea→todo 之前写上类别、档位和指派。
//
// 032 接管后分两路：observe 且未指派时只落一条不可变决策建议并返回
// ErrNotAssigned（任务留在 idea，绝不产生无主 todo）；auto 或已指派时
// 走 011 原有的 ApplyRoute。选人始终复用 routing.Pick，降级规则不回归。
func (s *Service) routeIdea(task *domain.Task) error {
	return s.routeIdeaOpts(task, routeOptions{})
}

func (s *Service) routeIdeaOpts(task *domain.Task, o routeOptions) error {
	d := s.decide(task)
	agents, err := s.candidates()
	if err != nil {
		return err
	}
	s.probeFloorAgent(agents)
	if _, err := s.candidates(); err != nil {
		return err
	}
	pool, err := s.dispatcherCandidates()
	if err != nil {
		return err
	}
	plan := dispatcher.New().Plan(dispatcher.PlanInput{
		Category: d.Category, Tier: d.Tier, Reason: d.Reason, CostBand: d.CostBand,
		Prefer: task.AssigneeAgentID, Candidates: pool,
	})
	if plan.Err != nil || plan.Chosen == nil {
		msg := "没有可用额度，无法指派"
		_, _ = s.store.AppendNote(task.ID, domain.ActorSystem, nil, "system", msg)
		return errors.Join(domain.ErrNotAssigned, errors.New(msg))
	}

	routeReason := plan.Reason
	if plan.PickReason != "" && plan.PickReason != "人类指定" {
		routeReason = plan.Reason + "；" + plan.PickReason
	}
	if plan.PickReason == "人类指定" {
		routeReason = "人类指定"
	}

	if o.mode(s) == dispatcher.ModeObserve && task.AssigneeAgentID == nil {
		snap := store.DecisionSnapshot{
			Enabled: plan.Chosen.Enabled, Quota: plan.Chosen.Quota,
			Balance: plan.Chosen.Balance, CostRank: plan.Chosen.CostRank,
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		scores, err := json.Marshal(plan.Scores)
		if err != nil {
			return err
		}
		observed, expires := store.NewDecisionTimes()
		chosenID := plan.Chosen.ID
		_, err = s.store.RecordDecision(&store.DispatchDecision{
			TaskID: task.ID, Mode: o.mode(s),
			Category: d.Category, Tier: d.Tier, ChosenAgentID: &chosenID,
			CostEstimate: plan.EstimatedCost, ScoreJSON: string(scores),
			SnapshotJSON: string(raw), Reason: routeReason,
			Source: o.source(), ScheduleID: o.ScheduleID,
			ObservedAt: observed, ExpiresAt: expires,
		})
		if err != nil {
			return err
		}
		return errors.Join(domain.ErrNotAssigned,
			errors.New("总管已生成派发建议，请采纳后再派发"))
	}

	if err := s.store.ApplyRoute(task.ID, d.Category, d.Tier, routeReason, plan.Chosen.ID); err != nil {
		return err
	}
	if err := s.store.SetTaskEstimatedCost(task.ID, plan.EstimatedCost); err != nil {
		return err
	}
	if task.AssigneeAgentID != nil && *task.AssigneeAgentID != plan.Chosen.ID {
		ev, err := s.store.AppendNote(task.ID, domain.ActorSystem, nil, "system", "原指派额度不足，改派："+plan.PickReason)
		if err == nil {
			s.publish(task.ID, ev)
		}
	}
	return nil
}

func (s *Service) probeFloorAgent(agents []routing.Candidate) {
	if s.probeFloor == nil {
		return
	}
	need := false
	for _, a := range agents {
		if a.Enabled && a.Serves == domain.ServesFloor && a.Quota == domain.QuotaUnknown {
			need = true
			break
		}
	}
	if !need {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	quota := s.probeFloor(ctx)
	if quota != domain.QuotaOK && quota != domain.QuotaExhausted {
		return
	}
	for _, a := range agents {
		if a.Serves == domain.ServesFloor && a.Quota == domain.QuotaUnknown {
			_ = s.store.SetAgentQuota(a.ID, quota)
			// 032-B：三态探测只有 exhausted 能写余额，且只写 0。
			// ok / unknown 一律不碰数值，避免把额度状态伪装成积分余额。
			if quota == domain.QuotaExhausted {
				_ = s.store.ZeroBalanceOnExhausted(a.ID)
			}
		}
	}
}

// reassignTodo 把待执行任务从额度用尽的 Agent 转给下一名。
func (s *Service) reassignTodo(taskID, fromAgent int64) {
	task, err := s.store.GetTask(taskID)
	if err != nil || task.Status != domain.StatusTodo {
		return
	}
	d := s.decide(task)
	if task.Tier != "" && domain.ValidTier(task.Tier) {
		d.Tier = task.Tier
	}
	if task.Category != "" && domain.ValidCategory(task.Category) {
		d.Category = task.Category
	}
	agents, err := s.candidates()
	if err != nil {
		return
	}
	chosen, reason, err := routing.Pick(d.Category, d.Tier, nil, map[int64]bool{fromAgent: true}, agents)
	if err != nil {
		ev, nerr := s.store.AppendNote(taskID, domain.ActorSystem, nil, "system", "没有可用额度，任务留在待执行")
		if nerr == nil {
			s.publish(taskID, ev)
		}
		return
	}
	ev, err := s.store.Reassign(taskID, chosen.ID, reason)
	if err == nil {
		s.publish(taskID, ev)
	}
}
