// Package domain 定义任务看板的核心实体与状态机规则。
// 所有状态流转的合法性集中在此处判定，store 层只负责持久化。
package domain

import (
	"errors"
	"time"
)

// Status 任务状态。
type Status string

const (
	// StatusIdea 灵感箱：仅人类可见，不会出现在任何 Agent 接口中。
	StatusIdea Status = "idea"
	// StatusTodo 待执行：已明确内容并指派 Agent，可被领取。
	StatusTodo Status = "todo"
	// StatusDoing 执行中：已被某 Agent 凭租约持有。
	StatusDoing Status = "doing"
	// StatusReview 待评审：Agent 已提交成果，等待人类评审。
	StatusReview Status = "review"
	// StatusApproved 已完成：人类评审通过的终态。
	StatusApproved Status = "approved"
)

// ActorType 事件产生者类型。
type ActorType string

const (
	ActorHuman  ActorType = "human"
	ActorAgent  ActorType = "agent"
	ActorSystem ActorType = "system"
)

// EventType 任务事件类型。
type EventType string

const (
	EventCreated     EventType = "created"       // 任务创建
	EventUpdated     EventType = "updated"       // 人类编辑任务内容
	EventAssigned    EventType = "assigned"      // 指派 Agent
	EventDispatch    EventType = "dispatch"      // 人类派发：idea -> todo
	EventClaim       EventType = "claim"         // Agent 领取
	EventHeartbeat   EventType = "heartbeat"     // Agent 心跳（不展示在主时间线）
	EventSubmit      EventType = "submit"        // Agent 提交成果
	EventReject      EventType = "reject"        // 人类驳回，回到 todo
	EventApprove     EventType = "approve"       // 人类通过
	EventRelease     EventType = "release"       // Agent 主动/失败退回
	EventLeaseExpire EventType = "lease_expired" // 租约超时系统回收
	EventNote        EventType = "note"          // 备注
	EventArchived    EventType = "archived"      // 人类归档
	EventUnarchived  EventType = "unarchived"    // 取消归档
	EventReassign    EventType = "reassign"      // 额度不足，改派给下一名
)

// 任务类别。一条任务只取一个。
const (
	CategoryCodeTool = "code_tool"
	CategoryCodeGame = "code_game"
	CategoryCopy     = "copy"
	CategoryLocalOps = "local_ops"
)

// 执行档位。floor 对应 DeepSeek V4.1 Flash。
const (
	TierSmall = "small"
	TierFloor = "floor"
	TierLarge = "large"
)

// Agent 额度与它能接的档位。
const (
	QuotaUnknown   = "unknown"
	QuotaOK        = "ok"
	QuotaExhausted = "exhausted"
	ServesSmall    = "small"
	ServesFloor    = "floor"
	ServesLarge    = "large"
)

// 业务错误，API 层据此映射状态码。
var (
	ErrNotFound       = errors.New("资源不存在")
	ErrInvalidStatus  = errors.New("非法的状态流转")
	ErrNotAssigned    = errors.New("任务未指派给该 Agent")
	ErrNotLeaseHolder = errors.New("当前租约不属于该 Agent")
	ErrAgentDisabled  = errors.New("Agent 已被禁用")
	ErrConflict       = errors.New("任务已被其他操作抢先变更")
	ErrInvalidInput   = errors.New("请求参数不合法")
	ErrUnauthorized   = errors.New("未授权")
	// ErrProjectInUse 项目仍被任务引用，禁止删除。API 层映射为 409。
	ErrProjectInUse = errors.New("项目仍被任务引用")
	// ErrWorkdirBound 目录已绑定到该项目（UNIQUE 冲突）。API 层映射为 409。
	ErrWorkdirBound = errors.New("目录已绑定到该项目")
	// ErrForbidden 设备令牌没有执行该操作的权限（操作不在范围或项目越权）。
	// API 层映射为 403，与 AGENT_DISABLED 的 403 同构。
	ErrForbidden = errors.New("没有权限执行此操作")
	// ErrInsufficientBudget 执行端已录入余额但不足以完成该任务（032）。
	ErrInsufficientBudget = errors.New("执行端余额不足")
	// ErrSuggestionStale 派发建议生成后执行端状态已变化，需重新计算（032）。
	ErrSuggestionStale = errors.New("派发建议已失效，请重新计算")
	// ErrHandoffNotAllowed 任务状态或目标不允许转交（028）。
	ErrHandoffNotAllowed = errors.New("当前任务状态不允许转交")
)

// Task 任务实体。
type Task struct {
	ID                 int64      `json:"id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	AcceptanceCriteria string     `json:"acceptance_criteria"`
	Workdir            string     `json:"workdir"`
	Priority           string     `json:"priority"`
	Status             Status     `json:"status"`
	AssigneeAgentID    *int64     `json:"assignee_agent_id"`
	LeaseUntil         *time.Time `json:"lease_until"`
	Version            int64      `json:"version"`
	// ArchivedAt 非空表示已归档。归档是"已完成"列无限膨胀的解药：
	// approved 是终态无法回退，若不归档，该列只增不减最终挤满看板。
	ArchivedAt *time.Time `json:"archived_at"`
	// SortOrder 列内位置，**只在同一 status 内有意义**（不跨列比较）。
	//
	// 值为 1..n 的连续整数，由 store 在整列重编号时维护。
	// 用连续整数而不是分数（float）或稀疏值：重排会整列重写，
	// 连续整数让 ORDER BY 走索引，也让"第 k 位"可以直接换算。
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Category 工作类别。空字符串表示尚未分类。
	Category string `json:"category"`
	// Tier 执行档位：small / floor / large。
	Tier string `json:"tier"`
	// RouteReason 为什么派给当前 Agent。
	RouteReason string `json:"route_reason"`
	// ProjectID 可选的项目归属。nil 表示无项目。
	// 项目只是展示分组，不影响状态机与 Agent 可见性。
	ProjectID *int64 `json:"project_id"`
	// EstimatedCost 总管预估的任务成本，单位由执行端 balance_unit 决定。
	EstimatedCost float64 `json:"estimated_cost"`
	// CostUsed Agent 在 submit/release 时可选上报的实际成本。
	// nil 表示未上报（与 0 有区别）；终审时优先按它结算。
	CostUsed *float64 `json:"cost_used"`
}

// IsArchived 判断任务是否已归档。
func (t *Task) IsArchived() bool { return t.ArchivedAt != nil }

// AgentVisibleStatuses Agent 端可见的状态集合。
// idea（灵感箱）仅人类可见，approved（终态）无需 Agent 介入，两者都不可见。
var AgentVisibleStatuses = []Status{StatusTodo, StatusDoing, StatusReview}

// IntersectStatuses 求 base 与 allowed 的交集。
//
// base 为空表示调用方未指定，返回 allowed 全集。
// 调用方指定了 allowed 中不存在的值时返回 nil，**调用方必须据此报错**，
// 绝不能退化成"全部"——否则 idea/approved 会泄露给 Agent。
// 这个 nil 分支是数据泄露的唯一防线，由 TestAgentScopeHidesIdeaAndApproved 强制。
func IntersectStatuses(base, allowed []Status) []Status {
	if len(base) == 0 {
		return append([]Status(nil), allowed...)
	}
	inAllowed := make(map[Status]bool, len(allowed))
	for _, s := range allowed {
		inAllowed[s] = true
	}
	var out []Status
	for _, s := range base {
		if inAllowed[s] {
			out = append(out, s)
		}
	}
	return out
}

// OnlineThreshold Agent 多久内有心跳/认证视为"在线"。
// 取 2 倍默认租约：超过这个时间没动静，基本可以认为该 Agent 进程已经不在了。
const OnlineThreshold = 30 * time.Minute

// Agent 已登记的 Agent 身份（对应一个编辑器/CLI 运行时）。
type Agent struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Enabled    bool       `json:"enabled"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	CreatedAt  time.Time  `json:"created_at"`
	// Quota 额度：unknown 仍可领一次，exhausted 不能领。
	Quota string `json:"quota"`
	// Serves 这个 Agent 接哪一档：small / floor / large。
	Serves string `json:"serves"`
	// CostRank 越小越便宜。同档里先选它。
	CostRank int `json:"cost_rank"`
	// Online 由应用层根据 LastSeenAt 推导，不落库。
	// 看板需要它来判断"这个 Agent 还活着吗"——这是本产品最核心的信息之一，
	// 原实现里完全不可见。
	Online bool `json:"online"`
	// Balance 数值余额。nil 表示尚未录入，总管的预算硬门禁会跳过它。
	Balance *float64 `json:"balance"`
	// BalanceUnit 余额单位，首版固定 credit。
	BalanceUnit string `json:"balance_unit"`
	// BudgetUpdatedAt 最近一次录入/扣减时间，用于界面提示数据新鲜度。
	BudgetUpdatedAt *time.Time `json:"budget_updated_at"`
}

// ComputeOnline 根据 LastSeenAt 与 enabled 推导在线状态。
func (a *Agent) ComputeOnline(now time.Time) {
	a.Online = a.Enabled && a.LastSeenAt != nil && now.Sub(*a.LastSeenAt) < OnlineThreshold
}

// TaskEvent 任务事件，append-only，看板时间线的数据源。
type TaskEvent struct {
	ID         int64     `json:"id"`
	TaskID     int64     `json:"task_id"`
	ActorType  ActorType `json:"actor_type"`
	ActorID    *int64    `json:"actor_id"`
	ActorName  string    `json:"actor_name"`
	Type       EventType `json:"type"`
	FromStatus *Status   `json:"from_status"`
	ToStatus   *Status   `json:"to_status"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// CanTransition 判定一次状态流转是否合法。
// human=true 表示人类发起，否则为 Agent 发起。
func CanTransition(from, to Status, human bool) bool {
	if from == to {
		return false
	}
	if human {
		switch from {
		case StatusIdea:
			return to == StatusTodo
		case StatusTodo:
			// 派发前允许收回灵感箱；执行中不允许人类干预状态。
			return to == StatusIdea
		case StatusReview:
			return to == StatusApproved || to == StatusTodo
		}
		return false
	}
	// Agent 侧：领取、提交、失败退回。
	switch from {
	case StatusTodo:
		return to == StatusDoing
	case StatusDoing:
		return to == StatusReview || to == StatusTodo
	}
	return false
}

// ValidStatuses 看板列顺序，供前端与校验复用。
func ValidStatuses() []Status {
	return []Status{StatusIdea, StatusTodo, StatusDoing, StatusReview, StatusApproved}
}

// IsValidStatus 判断状态字符串是否合法。
func IsValidStatus(s string) bool {
	for _, v := range ValidStatuses() {
		if string(v) == s {
			return true
		}
	}
	return false
}

// ValidPriority 合法优先级。
func ValidPriority(p string) bool {
	switch p {
	case "low", "normal", "high", "urgent":
		return true
	}
	return false
}

// ValidCategory 判断类别是否在四类之内。
func ValidCategory(c string) bool {
	switch c {
	case CategoryCodeTool, CategoryCodeGame, CategoryCopy, CategoryLocalOps:
		return true
	}
	return false
}

// ValidTier 判断档位是否合法。
func ValidTier(t string) bool {
	switch t {
	case TierSmall, TierFloor, TierLarge:
		return true
	}
	return false
}

// ValidQuota 判断额度值是否合法。
func ValidQuota(q string) bool {
	switch q {
	case QuotaUnknown, QuotaOK, QuotaExhausted:
		return true
	}
	return false
}

// DefaultTier 人类选定类别时使用的档位。
func DefaultTier(category string) string {
	switch category {
	case CategoryCopy, CategoryLocalOps:
		return TierSmall
	case CategoryCodeGame:
		return TierLarge
	case CategoryCodeTool:
		return TierFloor
	default:
		return ""
	}
}

// ProgrammingCategory 编程类任务，执行档不得低于 floor。
func ProgrammingCategory(c string) bool {
	return c == CategoryCodeTool || c == CategoryCodeGame
}
