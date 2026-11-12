// Package app 是业务编排层：组合 store、token 管理、SSE 事件总线与租约回收，
// 供 HTTP API、MCP server、桌面端共同复用。
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/routing"
	"github.com/mst8222/ingot-board/internal/store"
	"github.com/mst8222/ingot-board/internal/webhook"
)

// DefaultLease Agent 单次领取的默认租约时长。
const DefaultLease = 15 * time.Minute

// Service 应用服务。
type Service struct {
	store       *store.Store
	lease       time.Duration
	mu          sync.Mutex
	subscribers map[int64]*subscriber
	nextSubID   int64
	log         *eventLog
	// classify 为空时派发只用关键词，测试不会打到本机决策服务。
	classify func(ctx context.Context, title, description, workdir string) routing.Decision
	// probeFloor 探测 DeepSeek 额度。为空则不探测。
	probeFloor func(ctx context.Context) string
	// applyPhone 打开或关闭手机监听。为空时开关只落库。
	applyPhone PhoneApply
	// onReview 任务进入待评审时通知手机。为空则不通知。
	onReview func(taskID int64, title string)
	// resolveMode 覆盖全局双模开关（测试注入）；为空时读 dispatch_settings。
	resolveMode func() string
	// 031 调度器：单实例只允许起一个 loop。后两项是测试注入点，生产为 nil。
	schedulerOnce          sync.Once
	schedulerLoopStartHook func()
	schedulerDone          chan struct{}
	// vapidPublic Web Push 公钥。
	vapidPublic string
	runtimeMu   sync.Mutex
	runtime     PhoneRuntime
}

// subscriber 单个 SSE 订阅者。
type subscriber struct {
	ch chan Change
	// overflow 慢消费者标记：缓冲满过。SSE handler 下次可写时
	// 显式发 resync 通知前端全量刷新，而不是静默丢事件。
	overflow bool
}

// New 创建服务。
func New(st *store.Store) *Service {
	return &Service{
		store:       st,
		lease:       DefaultLease,
		subscribers: make(map[int64]*subscriber),
		log:         newEventLog(DefaultEventLogSize),
	}
}

// WithLease 设置租约时长（测试/配置用），返回服务自身便于链式调用。
func (s *Service) WithLease(d time.Duration) *Service {
	s.lease = d
	return s
}

// WithEventLogSize 设置事件缓冲容量（测试用）。
func (s *Service) WithEventLogSize(n int) *Service {
	s.log = newEventLog(n)
	return s
}

// Store 暴露底层仓储（CLI 本地管理命令使用）。
func (s *Service) Store() *store.Store { return s.store }

// Close 释放资源。
func (s *Service) Close() error { return s.store.Close() }

// ---------- 事件总线 ----------

// Subscribe 订阅任务变更，调用方负责在结束时 Unsubscribe。
func (s *Service) Subscribe() (int64, <-chan Change) {
	ch := make(chan Change, 64)
	s.mu.Lock()
	s.nextSubID++
	id := s.nextSubID
	s.subscribers[id] = &subscriber{ch: ch}
	s.mu.Unlock()
	return id, ch
}

// Unsubscribe 取消订阅。
func (s *Service) Unsubscribe(id int64) {
	s.mu.Lock()
	if sb, ok := s.subscribers[id]; ok {
		delete(s.subscribers, id)
		close(sb.ch)
	}
	s.mu.Unlock()
}

// CurrentSeq 返回当前事件序号，供 SSE 首帧携带。
func (s *Service) CurrentSeq() int64 { return s.log.last() }

// ReplaySince 回放 seq > lastSeq 的事件。
// 第二个返回值为 true 时表示**必须让客户端全量刷新**。
func (s *Service) ReplaySince(lastSeq int64) ([]Change, bool) { return s.log.since(lastSeq) }

// DrainOverflow 取出并清除某订阅者的溢出标记。
func (s *Service) DrainOverflow(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb, ok := s.subscribers[id]; ok && sb.overflow {
		sb.overflow = false
		return true
	}
	return false
}

func (s *Service) publish(taskID int64, ev *domain.TaskEvent) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		log.Printf("发布事件时加载任务失败 id=%d: %v", taskID, err)
		return
	}
	enteredReview := ev != nil && ev.ToStatus != nil && *ev.ToStatus == domain.StatusReview &&
		(ev.FromStatus == nil || *ev.FromStatus != domain.StatusReview)
	s.mu.Lock()
	notify := s.onReview
	s.mu.Unlock()
	s.emit(Change{Kind: ChangeTask, TaskID: taskID, Task: task, Event: ev})
	if !enteredReview {
		return
	}
	id, title := task.ID, task.Title
	if notify != nil {
		go notify(id, title)
	}
	// 地址在发布当时读取。空地址不另起 goroutine，避免测试结束关库后还去读。
	hookURL, err := s.store.GetReviewWebhookURL()
	if err != nil {
		log.Printf("读取待评审通知地址失败 task=%d: %v", id, err)
		return
	}
	if hookURL != "" {
		go webhook.Post(hookURL, id, title)
	}
}

// publishAgent 广播 Agent 变更。
//
// 重构前 RegisterAgent / RotateToken / SetAgentEnabled 都不广播，
// 导致用户在别处（如 CLI）改了 Agent，Web 端要手动刷新才看得到。
func (s *Service) publishAgent(a *domain.Agent) {
	s.emit(Change{Kind: ChangeAgent, Agent: a})
}

// PublishColumn 广播某一列的新顺序。
//
// 为什么需要独立的一帧，而不是跟着 publish 发那条任务的实体：
// 重排改的是**整列**的相对位置，而实体帧只携带单条任务。
// 客户端拿到"任务 7 的 sort_order 变了"无法推断出其他任务的位置
// 也不再正确，只能重拉整个看板 —— 而重排是高频操作，
// 每次都重拉会让多端操作时界面持续闪烁。
// 一帧里带上整列 ID 序列，客户端直接按它重排本地缓存即可。
func (s *Service) PublishColumn(status string) {
	ids, err := s.store.ColumnTaskIDs(status)
	if err != nil {
		log.Printf("发布列顺序时读取失败 status=%s: %v", status, err)
		return
	}
	s.emit(Change{
		Kind:      ChangeColumn,
		Status:    status,
		ColumnIDs: ids,
	})
}

// emit 写入环形缓冲并广播给所有订阅者。
//
// 慢消费者不再静默丢弃：置 overflow 标记，由 SSE handler 在下次可写时
// 显式发 resync 事件。
func (s *Service) emit(c Change) {
	c = s.log.append(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sb := range s.subscribers {
		select {
		case sb.ch <- c:
		default:
			sb.overflow = true
			log.Printf("SSE 订阅者 %d 缓冲已满，标记待 resync seq=%d", id, c.Seq)
		}
	}
}

// StartReaper 启动租约超时回收循环，随 ctx 取消而退出。
func (s *Service) StartReaper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reaped, err := s.store.ReapExpiredLeases()
				if err != nil {
					log.Printf("租约回收失败: %v", err)
					continue
				}
				for _, r := range reaped {
					log.Printf("任务 %d 租约超时，已回收为 todo", r.TaskID)
					s.publish(r.TaskID, r.Event)
				}
			}
		}
	}()
}

// ---------- Token ----------

// GenerateToken 生成新明文 token 与其 sha256 哈希。
func GenerateToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = "igt_" + hex.EncodeToString(b)
	hash = HashToken(plaintext)
	return plaintext, hash, nil
}

// HashToken 计算 token 哈希。
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ---------- Agent 管理 ----------

// RegisterAgent 登记 Agent 并签发首个 token，明文 token 仅本次返回。
func (s *Service) RegisterAgent(name, kind string) (*domain.Agent, string, error) {
	if name == "" {
		return nil, "", errors.Join(domain.ErrInvalidInput, errors.New("名称不能为空"))
	}
	if kind == "" {
		kind = "mcp"
	}
	plaintext, hash, err := GenerateToken()
	if err != nil {
		return nil, "", err
	}
	a, err := s.store.CreateAgent(name, kind, hash)
	if err != nil {
		return nil, "", err
	}
	s.publishAgent(a)
	return a, plaintext, nil
}

// RotateToken 吊销旧 token 并签发新 token。
func (s *Service) RotateToken(agentID int64) (string, error) {
	if _, err := s.store.GetAgent(agentID); err != nil {
		return "", err
	}
	plaintext, hash, err := GenerateToken()
	if err != nil {
		return "", err
	}
	if err := s.store.RotateAgentToken(agentID, hash); err != nil {
		return "", err
	}
	// token 本身不外泄，但 Agent 的最后出现时间会变，广播一下让顶栏在线数刷新。
	if a, err := s.store.GetAgent(agentID); err == nil {
		s.publishAgent(a)
	}
	return plaintext, nil
}

// SetAgentEnabled 启用/禁用 Agent。
func (s *Service) SetAgentEnabled(agentID int64, enabled bool) error {
	if err := s.store.SetAgentEnabled(agentID, enabled); err != nil {
		return err
	}
	if a, err := s.store.GetAgent(agentID); err == nil {
		s.publishAgent(a)
	}
	return nil
}

// DeleteAgent 删除 Agent。默认拒绝仍有在办任务的 Agent，
// force=true 时把受影响任务退回灵感箱。详见 store.DeleteAgent 的护栏说明。
func (s *Service) DeleteAgent(agentID int64, force bool) (*store.DeleteAgentResult, error) {
	res, err := s.store.DeleteAgent(agentID, force)
	if err != nil {
		return nil, err
	}
	s.emit(Change{Kind: ChangeAgent, Agent: &domain.Agent{ID: agentID}})
	return res, nil
}

// ListAgents 列出全部 Agent（含推导出的在线状态）。
func (s *Service) ListAgents() ([]*domain.Agent, error) { return s.store.ListAgents() }

// Meta 返回状态机与枚举定义，供前端作为唯一数据源。
func (s *Service) Meta() domain.Meta {
	// ServerTime 用于前端校正租约倒计时的时钟基准：客户端时钟漂移会让
	// "还剩 2 分钟"变成"已超时"。这里用 RFC3339 即可 ——
	// 它只被 new Date() 解析，不参与任何字符串比较。
	return domain.BuildMeta(time.Now().UTC().Format(time.RFC3339))
}

// AuthenticateAgent 供 API/MCP 使用 token 换 Agent 身份。
func (s *Service) AuthenticateAgent(plaintext string) (*domain.Agent, error) {
	return s.store.AuthenticateAgent(HashToken(strings.TrimSpace(plaintext)))
}

// ---------- 人类端任务操作 ----------

// CreateTaskInput 创建任务入参。
type CreateTaskInput struct {
	Title              string
	Description        string
	AcceptanceCriteria string
	Workdir            string
	Priority           string
	AssigneeAgentID    *int64
	// ProjectID 可选项目归属。nil 表示无项目；指向不存在/已归档项目由 store 守卫拒绝。
	ProjectID *int64
}

// CreateTask 人类创建任务（默认 idea）。
// ctx 携带设备上下文（手机监听）时按 017 做项目授权校验；回环传 context.Background()。
func (s *Service) CreateTask(ctx context.Context, in CreateTaskInput) (*domain.Task, error) {
	if in.Title == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("标题不能为空"))
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if !domain.ValidPriority(in.Priority) {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("优先级不合法"))
	}
	if in.AssigneeAgentID != nil {
		if err := s.checkAssignable(*in.AssigneeAgentID); err != nil {
			return nil, err
		}
	}
	if err := s.checkProjectScope(ctx, in.ProjectID, in.Workdir); err != nil {
		return nil, err
	}
	t := &domain.Task{
		Title:              in.Title,
		Description:        in.Description,
		AcceptanceCriteria: in.AcceptanceCriteria,
		Workdir:            in.Workdir,
		Priority:           in.Priority,
		Status:             domain.StatusIdea,
		AssigneeAgentID:    in.AssigneeAgentID,
		ProjectID:          in.ProjectID,
	}
	ev, err := s.store.CreateTask(t)
	if err != nil {
		return nil, err
	}
	created, err := s.store.GetTask(t.ID)
	if err != nil {
		return nil, err
	}
	s.publish(created.ID, ev)
	return created, nil
}

// UpdateTaskInput 人类编辑任务入参。
type UpdateTaskInput struct {
	Title              string
	Description        string
	AcceptanceCriteria string
	Workdir            string
	Priority           string
	AssigneeAgentID    *int64
	Version            int64
	Category           string
	// ProjectID 可选项目归属。nil 表示清空项目归属。
	ProjectID *int64
}

// UpdateTask 人类编辑任务内容/指派人。
// ctx 携带设备上下文时按 017 做项目授权校验（按更新后的终态判定）。
func (s *Service) UpdateTask(ctx context.Context, id int64, in UpdateTaskInput) (*domain.Task, error) {
	if in.Title == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("标题不能为空"))
	}
	if !domain.ValidPriority(in.Priority) {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("优先级不合法"))
	}
	if in.AssigneeAgentID != nil {
		cur, err := s.store.GetTask(id)
		if err != nil {
			return nil, err
		}
		// 只在改派时校验：指派人后来被禁用的任务，人类仍要能编辑其他字段。
		if cur.AssigneeAgentID == nil || *cur.AssigneeAgentID != *in.AssigneeAgentID {
			if err := s.checkAssignable(*in.AssigneeAgentID); err != nil {
				return nil, err
			}
		}
	}
	if err := s.checkProjectScope(ctx, in.ProjectID, in.Workdir); err != nil {
		return nil, err
	}
	assignNote := "指派 Agent 执行"
	ev, err := s.store.UpdateTaskMeta(id, in.Title, in.Description, in.AcceptanceCriteria,
		in.Workdir, in.Priority, in.AssigneeAgentID, in.Version, assignNote, in.Category, in.ProjectID)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	s.publish(id, ev)
	return t, nil
}

// checkAssignable 校验 Agent 可被指派：必须存在且已启用。
// 已禁用的 Agent 无法领取，指派给它的任务派发后就是死任务。
func (s *Service) checkAssignable(agentID int64) error {
	a, err := s.store.GetAgent(agentID)
	if err != nil {
		return err
	}
	if !a.Enabled {
		return errors.Join(domain.ErrInvalidInput, errors.New("Agent "+a.Name+" 已禁用，不能指派"))
	}
	return nil
}

// HumanTransition 人类状态流转（发布、驳回、通过、收回灵感箱）。
//
// appendTo 为拖拽落位下标，store.AppendToEnd 表示追加到目标列末尾。
func (s *Service) HumanTransition(id int64, to domain.Status, comment string, appendTo int) (*domain.Task, error) {
	return s.humanTransitionOpts(id, to, comment, appendTo, routeOptions{})
}

// humanTransitionOpts 与 HumanTransition 完全同构，只是把 031 计划级派发模式
// 透传给 routeIdea。状态机、守卫与事件语义一行未改。
func (s *Service) humanTransitionOpts(id int64, to domain.Status, comment string, appendTo int, o routeOptions) (*domain.Task, error) {
	if to == domain.StatusTodo {
		cur, err := s.store.GetTask(id)
		if err != nil {
			return nil, err
		}
		if cur.Status == domain.StatusIdea {
			if err := s.routeIdeaOpts(cur, o); err != nil {
				return nil, err
			}
		}
	}
	_, ev, err := s.store.HumanTransition(id, to, comment, appendTo)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	s.publish(id, ev)
	return t, nil
}

// MoveTask 调整同列内的顺序。
//
// 与 HumanTransition 分开的原因：排序不属于状态流转 ——
// 不写 task_events（拖一下顺序不是业务事件）、不动 version 之外的状态字段、
// 不经过状态机与守卫。合成一个接口会让"纯排序"被当成"流转"来记录，
// 时间线里会塞满无意义的条目。
func (s *Service) MoveTask(id int64, toIndex int, version int64) (*domain.Task, error) {
	return s.store.MoveTask(id, toIndex, version)
}

// AddHumanNote 人类追加备注。
func (s *Service) AddHumanNote(id int64, message string) (*domain.Task, error) {
	if message == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("备注内容不能为空"))
	}
	ev, err := s.store.AppendNote(id, domain.ActorHuman, nil, "human", message)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	s.publish(id, ev)
	return t, nil
}

// ListTasks 按过滤器列出任务，同时返回命中总数（供分页填 total）。
func (s *Service) ListTasks(f store.TaskFilter) ([]*domain.Task, int, error) {
	if err := f.Normalize(); err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountTasks(f)
	if err != nil {
		return nil, 0, err
	}
	tasks, err := s.store.ListTasks(f)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// Board 返回看板数据：按状态分组 + 每列真实计数。
// perColumn 为每列返回上限；Counts 不受其影响，折叠条数字永远正确。
func (s *Service) Board(f store.TaskFilter, perColumn int) (*store.BoardResult, error) {
	return s.store.ListBoard(f, perColumn)
}

// GetTask 取任务详情。
func (s *Service) GetTask(id int64) (*domain.Task, error) { return s.store.GetTask(id) }

// ListEventsPage 分页取任务事件时间线。
func (s *Service) ListEventsPage(f store.EventFilter) ([]*domain.TaskEvent, int, error) {
	return s.store.ListEventsPage(f)
}

// ArchiveTask 归档任务。
func (s *Service) ArchiveTask(id int64) (*domain.Task, error) {
	_, ev, err := s.store.ArchiveTask(id)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	if ev != nil {
		s.publish(id, ev)
	}
	return t, nil
}

// UnarchiveTask 取消归档。
func (s *Service) UnarchiveTask(id int64) (*domain.Task, error) {
	_, ev, err := s.store.UnarchiveTask(id)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	if ev != nil {
		s.publish(id, ev)
	}
	return t, nil
}

// ---------- Agent 端任务操作 ----------

// ListAgentTasks 列出指派给某 Agent 的任务。
//
// ⚠️ 数据泄露防线：
//   - Assignee 由 Token 身份注入，**忽略**调用方传入的任何指派参数；
//   - Statuses 与 AgentVisibleStatuses 求交集；若调用方指定了
//     idea/approved，IntersectStatuses 返回 nil，这里必须**报错**，
//     绝不能退化成"全部" —— 否则灵感箱与终态任务会泄露给 Agent。
//
// 由 TestAgentScopeHidesIdeaAndApproved 强制。
func (s *Service) ListAgentTasks(agentID int64, q store.AgentTaskQuery) ([]*domain.Task, error) {
	if q.Limit <= 0 || q.Limit > store.MaxPageSize {
		q.Limit = store.MaxPageSize
	}
	statuses := domain.IntersectStatuses(q.Statuses, domain.AgentVisibleStatuses)
	if len(statuses) == 0 {
		return nil, store.ErrAgentStatusForbidden()
	}
	return s.store.ListTasks(store.TaskFilter{
		Statuses:   statuses,
		Priorities: q.Priorities,
		Query:      q.Query,
		Assignee:   &agentID,
		Archived:   store.ArchivedExclude,
		Sort:       q.Sort,
		Limit:      q.Limit,
		Offset:     q.Offset,
	})
}

// Claim Agent 领取任务。
func (s *Service) Claim(agentID int64, taskID int64) (*domain.Task, error) {
	_, ev, err := s.store.Claim(taskID, agentID, s.lease)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	s.publish(taskID, ev)
	return t, nil
}

// Heartbeat Agent 续租。
//
// ⚠️ 这里**有意不广播 SSE**，不是遗漏。心跳默认 5 分钟一次 × N 个 Agent
// 会产生大量无意义广播，而它既不改状态机也不写人类可见的时间线
// （task_events 的 heartbeat 行被 ListEventsPage 默认过滤掉）。
// 租约真正到期时由 StartReaper 回收并正常广播。
func (s *Service) Heartbeat(agentID, taskID int64) error {
	if err := s.store.Heartbeat(taskID, agentID, s.lease); err != nil {
		return err
	}
	// 但 Agent 的「最后出现时间」变了，顶栏的在线计数需要跟着更新。
	if a, err := s.store.GetAgent(agentID); err == nil {
		s.publishAgent(a)
	}
	return nil
}

// Submit Agent 提交成果进入评审。不带 cost_used，保持旧调用兼容。
func (s *Service) Submit(agentID, taskID int64, summary string) (*domain.Task, error) {
	return s.SubmitWithCost(agentID, taskID, summary, nil)
}

// SubmitWithCost 提交成果并可选记录实际成本（032-B）。
func (s *Service) SubmitWithCost(agentID, taskID int64, summary string, costUsed *float64) (*domain.Task, error) {
	if summary == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("提交说明不能为空"))
	}
	if err := store.ValidateCostUsed(costUsed); err != nil {
		return nil, err
	}
	_, ev, err := s.store.SubmitWithCost(taskID, agentID, summary, costUsed)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	s.publish(taskID, ev)
	return t, nil
}

// Release Agent 放弃/失败退回任务。
func (s *Service) Release(agentID, taskID int64, reason, quota string) (*domain.Task, error) {
	return s.ReleaseWithCost(agentID, taskID, reason, quota, nil)
}

// ReleaseWithCost 退回任务并可选记录实际成本（032-B）。
func (s *Service) ReleaseWithCost(agentID, taskID int64, reason, quota string, costUsed *float64) (*domain.Task, error) {
	if err := store.ValidateCostUsed(costUsed); err != nil {
		return nil, err
	}
	if quota != "" {
		if !domain.ValidQuota(quota) || quota == domain.QuotaUnknown {
			return nil, errors.Join(domain.ErrInvalidInput, errors.New("额度值不合法"))
		}
		if err := s.store.SetAgentQuota(agentID, quota); err != nil {
			return nil, err
		}
		if a, err := s.store.GetAgent(agentID); err == nil {
			s.publishAgent(a)
		}
	}
	_, ev, err := s.store.ReleaseWithCost(taskID, agentID, reason, costUsed)
	if err != nil {
		return nil, err
	}
	s.publish(taskID, ev)
	agent, err := s.store.GetAgent(agentID)
	if err != nil {
		return nil, err
	}
	if agent.Quota == domain.QuotaExhausted {
		s.reassignTodo(taskID, agentID)
	}
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// SetAgentQuota 人类或 Agent 上报额度。
func (s *Service) SetAgentQuota(agentID int64, quota string) (*domain.Agent, error) {
	if err := s.store.SetAgentQuota(agentID, quota); err != nil {
		return nil, err
	}
	a, err := s.store.GetAgent(agentID)
	if err != nil {
		return nil, err
	}
	s.publishAgent(a)
	return a, nil
}

// ListAgentTaskEvents Agent 读取自己任务的时间线。
//
// 驳回原因与人类备注只存在于事件里：没有这个接口，Agent 重新领取被驳回的任务时
// 拿到的仍是原始描述，只能原样再做一遍。心跳事件对 Agent 无意义，始终过滤。
// idea / approved / 已归档任务对 Agent 不可见，按不存在处理，不泄露其存在性。
func (s *Service) ListAgentTaskEvents(agentID int64, f store.EventFilter) ([]*domain.TaskEvent, int, error) {
	task, err := s.store.GetTask(f.TaskID)
	if err != nil {
		return nil, 0, err
	}
	if !slices.Contains(domain.AgentVisibleStatuses, task.Status) || task.IsArchived() {
		return nil, 0, domain.ErrNotFound
	}
	if task.AssigneeAgentID == nil || *task.AssigneeAgentID != agentID {
		return nil, 0, domain.ErrNotAssigned
	}
	f.WithHeartbeat = false
	return s.store.ListEventsPage(f)
}

// AddAgentNote Agent 追加进展备注（仅限指派给自己的任务）。
func (s *Service) AddAgentNote(agentID int64, agentName string, taskID int64, message string) (*domain.Task, error) {
	if message == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("备注内容不能为空"))
	}
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if task.AssigneeAgentID == nil || *task.AssigneeAgentID != agentID {
		return nil, domain.ErrNotAssigned
	}
	ev, err := s.store.AppendNote(taskID, domain.ActorAgent, &agentID, agentName, message)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	s.publish(taskID, ev)
	return t, nil
}
