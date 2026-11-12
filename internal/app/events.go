package app

import (
	"sync"

	"github.com/mst8222/ingot-board/internal/domain"
)

// DefaultEventLogSize 事件环形缓冲容量。
// 512 条足够覆盖一次网络抖动与慢客户端；再小则断线重连时
// 更容易落到"必须全量刷新"的分支。
const DefaultEventLogSize = 512

// ChangeKind 变更类型，决定前端走哪条更新路径。
type ChangeKind string

const (
	// ChangeTask 任务变更 —— 前端需要重取看板与该任务详情。
	ChangeTask ChangeKind = "task"
	// ChangeAgent Agent 增删/启停 —— 前端只需重取 Agent 列表。
	ChangeAgent ChangeKind = "agent"
	// ChangeColumn 列内顺序变更 —— 只重排本地缓存，不重拉。
	//
	// 单列最多 500 条，广播 ID 序列的成本远低于让每个客户端重拉看板。
	ChangeColumn ChangeKind = "column"
	// ChangeProject 项目增删改 —— 前端只需重取项目列表（筛选 chips 用）。
	ChangeProject ChangeKind = "project"
)

// Change 推送给 SSE 订阅者的变更载荷。
type Change struct {
	// Seq 单调递增的全局序号，由 Service 分配，是 Last-Event-ID 的唯一依据。
	// ⚠️ 服务端重启后 seq 从 0 重新开始，客户端必须能识别（见 ready 帧比对）。
	Seq    int64             `json:"seq"`
	Kind   ChangeKind        `json:"kind"`
	TaskID int64             `json:"task_id,omitempty"`
	Task   *domain.Task      `json:"task,omitempty"`
	Event  *domain.TaskEvent `json:"event,omitempty"`
	Agent  *domain.Agent     `json:"agent,omitempty"`
	// Status 与 ColumnIDs 仅在 Kind==ChangeColumn 时有值，
	// 分别是列的状态与该列当前的完整任务 ID 顺序。
	Status    string  `json:"status,omitempty"`
	ColumnIDs []int64 `json:"column_ids,omitempty"`
	// Project 仅在 Kind==ChangeProject 时有值。任务挂/摘项目不在此载荷，
	// 走既有 ChangeTask（Task 自带 project_id）。
	Project *domain.Project `json:"project,omitempty"`
}

// eventLog 是定容环形缓冲，支持按 seq 回放与缺口检测。
//
// 为什么需要它：重构前 publish 在订阅者缓冲满时**静默丢弃**事件，
// 只在服务端打一行日志，注释写着"由前端自行全量刷新兜底"——
// 但那层兜底并不存在。断线或慢客户端会永久丢事件，看板与服务端逐渐漂移。
// 有了环形缓冲，断线重连可以按 Last-Event-ID 精确补发；
// 补不上（落后太多）时显式通知前端全量刷新，而不是静默丢数据。
type eventLog struct {
	mu     sync.Mutex
	buf    []Change
	head   int // 下一个写入位置
	n      int // 已用元素数
	lastID int64
}

func newEventLog(size int) *eventLog {
	if size <= 0 {
		size = DefaultEventLogSize
	}
	return &eventLog{buf: make([]Change, size)}
}

// append 写入一条并返回带 seq 的副本。
func (l *eventLog) append(c Change) Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastID++
	c.Seq = l.lastID
	l.buf[l.head] = c
	l.head = (l.head + 1) % len(l.buf)
	if l.n < len(l.buf) {
		l.n++
	}
	return c
}

// last 返回已分配的最大 seq。
func (l *eventLog) last() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastID
}

// since 返回 seq > lastSeq 的全部事件（按 seq 升序）。
//
// resyncRequired=true 表示缓冲已经滚动覆盖掉客户端需要的区间，
// **无法靠回放恢复**。调用方必须通知客户端做全量刷新 —— 这是
// 防止看板静默漂移的最后一道防线。
func (l *eventLog) since(lastSeq int64) (events []Change, resyncRequired bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 全新连接（lastSeq<=0）或已是最新，无需回放。
	if lastSeq <= 0 || lastSeq >= l.lastID {
		return nil, false
	}
	// 客户端声称落后，但缓冲为空：服务端重启过，历史已不可恢复。
	if l.n == 0 {
		return nil, true
	}
	oldest := l.buf[(l.head-l.n+len(l.buf))%len(l.buf)].Seq
	// 客户端要的起点已经被覆盖掉了。
	if lastSeq+1 < oldest {
		return nil, true
	}

	first := (l.head - l.n + len(l.buf)) % len(l.buf)
	events = make([]Change, 0, l.n)
	for i := 0; i < l.n; i++ {
		c := l.buf[(first+i)%len(l.buf)]
		if c.Seq > lastSeq {
			events = append(events, c)
		}
	}
	return events, false
}
