package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/mst8222/ingot-board/internal/dispatcher"
	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/store"
)

// 029 任务模板：只复用派发期素材，实例化仍走既有 CreateTask → routeIdea →
// HumanTransition 链路，不绕过 GuardRequiresAssignee 与 CanTransition。

func (s *Service) Templates(includeArchived bool) ([]store.Template, error) {
	return s.store.ListTemplates(includeArchived)
}

func (s *Service) Template(id int64) (*store.Template, error) {
	return s.store.GetTemplate(id)
}

func (s *Service) SaveAsTemplate(taskID int64, name string) (*store.Template, error) {
	return s.store.CreateTemplateFromTask(taskID, name)
}

func (s *Service) UpdateTemplate(id int64, p store.TemplatePatch) (*store.Template, error) {
	return s.store.UpdateTemplate(id, p)
}

func (s *Service) DeleteTemplate(id int64) error {
	return s.store.DeleteTemplate(id)
}

func templateInput(tpl *store.Template) CreateTaskInput {
	return CreateTaskInput{
		Title:              tpl.Title,
		Description:        tpl.Description,
		AcceptanceCriteria: tpl.AcceptanceCriteria,
		Workdir:            tpl.Workdir,
		Priority:           tpl.Priority,
		AssigneeAgentID:    tpl.AssigneeAgentID,
		ProjectID:          tpl.ProjectID,
	}
}

// InstantiateTemplate 从模板生成一条 idea 任务，再按既有派发链路推向 todo。
//
// 模板若绑定了执行端，CreateTask 会先按 003 的 checkAssignable 校验启用状态；
// 因此目标被禁用或已不存在时，连 idea 任务都不会产生，更不会留下无主 todo。
// 未绑定执行端时由总管双模决定：observe 只落 idea + 建议，auto 才自动定人。
func (s *Service) InstantiateTemplate(ctx context.Context, id int64) (*domain.Task, error) {
	tpl, err := s.store.GetTemplate(id)
	if err != nil {
		return nil, err
	}
	if tpl.IsArchived() {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("已归档模板不能实例化"))
	}
	created, err := s.CreateTask(ctx, templateInput(tpl))
	if err != nil {
		return nil, err
	}
	// 空值合法（交给 routeIdea 分类）；陈旧模板里的非法值直接忽略，不污染派发。
	if err := s.store.SetTaskTemplateMeta(created.ID,
		sanitizeCategory(tpl.Category), sanitizeTier(tpl.Tier)); err != nil {
		return nil, err
	}
	t, err := s.HumanTransition(created.ID, domain.StatusTodo, "", store.AppendToEnd)
	if err != nil {
		return created, errors.Join(err,
			errors.New("模板已生成灵感箱任务，请按总管建议派发"))
	}
	return t, nil
}

// InstantiateTemplateForSchedule 由 031 调度器调用，mode 已由计划解析为 observe/auto。
//
// observe 刻意丢掉模板的指派，让 routeIdeaOpts 一定走「只落建议」分支：任务留在
// idea 等人采纳，绝不因为模板绑了执行端就无人值守地派发出去。auto 才走完整
// HumanTransition 定人进 todo，这是人在建计划时显式勾选的授权。
func (s *Service) InstantiateTemplateForSchedule(ctx context.Context, tplID int64, mode string, scheduleID int64) (*domain.Task, error) {
	tpl, err := s.store.GetTemplate(tplID)
	if err != nil {
		return nil, err
	}
	if tpl.IsArchived() {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("已归档模板不能实例化"))
	}
	in := templateInput(tpl)
	if mode == dispatcher.ModeObserve {
		in.AssigneeAgentID = nil
	}
	created, err := s.CreateTask(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetTaskTemplateMeta(created.ID,
		sanitizeCategory(tpl.Category), sanitizeTier(tpl.Tier)); err != nil {
		return created, err
	}
	o := routeOptions{
		ModeOverride: mode, Source: "schedule", ScheduleID: &scheduleID,
	}
	switch mode {
	case dispatcher.ModeObserve:
		// 重新读一次，让 decide 看到刚写上的模板分类 / 档位。
		cur, err := s.store.GetTask(created.ID)
		if err != nil {
			return created, err
		}
		// observe 分支用 ErrNotAssigned 表示「已落建议、留在 idea」，对调度器是成功。
		if err := s.routeIdeaOpts(cur, o); err != nil && !errors.Is(err, domain.ErrNotAssigned) {
			return created, err
		}
	case dispatcher.ModeAuto:
		if _, err := s.humanTransitionOpts(created.ID, domain.StatusTodo, "", store.AppendToEnd, o); err != nil {
			return created, err
		}
	default:
		return created, errors.Join(domain.ErrInvalidInput, errors.New("计划派发模式未解析"))
	}
	msg := fmt.Sprintf("定时计划 #%d 触发（dispatch_mode=%s，模板 #%d）", scheduleID, mode, tplID)
	if ev, err := s.store.AppendNote(created.ID, domain.ActorSystem, nil, "scheduler", msg); err == nil {
		s.publish(created.ID, ev)
	}
	return s.store.GetTask(created.ID)
}

func sanitizeCategory(v string) string {
	if domain.ValidCategory(v) {
		return v
	}
	return ""
}

func sanitizeTier(v string) string {
	if domain.ValidTier(v) {
		return v
	}
	return ""
}
