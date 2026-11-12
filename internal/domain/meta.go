package domain

// Meta 是前端获取状态机与枚举定义的唯一数据源。
//
// 为什么需要它：重构前状态流转规则在 domain.CanTransition 与前端 store.ts
// 各写一份，枚举中文标签在 meta.ts 的 STATUS_LABEL 与 store.ts 的
// COLUMNS[].label 又写了两遍。四份定义必然漂移，且无法被测试强制。
//
// 这里的 HumanTransitions / AgentTransitions 由 CanTransition 在 init()
// 里穷举生成，因此"与状态机一致"这件事可以由
// TestMetaMatchesCanTransition 逐格断言，而不是靠自觉。
//
// HumanTransitionGuards 同理，由 TransitionGuards 穷举生成：
// 它回答的是合法边上"还差什么前置条件"（派发要先指派、驳回要写原因）。
// 没有它，前端只知道 idea 能拖到 todo，却不知道拖过去会被服务端拒绝 ——
// 而那条规则原本只存在于store 里，前端无从得知。
type Meta struct {
	Statuses         []StatusMeta        `json:"statuses"`
	Priorities       []EnumMeta          `json:"priorities"`
	EventTypes       []EnumMeta          `json:"event_types"`
	ActorTypes       []EnumMeta          `json:"actor_types"`
	AgentKinds       []EnumMeta          `json:"agent_kinds"`
	SortFields       []EnumMeta          `json:"sort_fields"`
	HumanTransitions map[string][]string `json:"human_transitions"`
	AgentTransitions map[string][]string `json:"agent_transitions"`
	// HumanTransitionGuards 键为 "from>to"，值为该次流转的前置条件列表。
	//
	// 与 HumanTransitions 分开而不是合并进去，是因为两者语义不同：
	// 前者回答"能不能"，后者回答"还差什么"。
	// 前端拖拽落位前先查这张表：守卫未满足时直接弹出指派/填写原因的引导，
	// 而不是发一次注定 409 的请求再把卡片弹回来。
	HumanTransitionGuards map[string][]string `json:"human_transition_guards"`
	// ServerTime 供前端校正租约倒计时用的时钟基准，避免客户端时钟漂移导致误判。
	ServerTime string `json:"server_time"`
}

// StatusMeta 单个看板列的展示定义。
type StatusMeta struct {
	Value Status `json:"value"`
	Label string `json:"label"`
	// Hint 是列头下方的一行说明，来自原前端 store.ts 的 COLUMNS[].hint。
	Hint string `json:"hint"`
	// AgentVisible 决定该状态是否出现在 Agent 端接口里。
	AgentVisible bool `json:"agent_visible"`
	// Terminal 表示终态，不可再流转。UI 应据此禁用所有流转入口。
	Terminal bool `json:"terminal"`
	// CollapsedByDefault 让"已完成"默认折叠为计数条，避免该列无限膨胀挤占看板。
	CollapsedByDefault bool `json:"collapsed_by_default"`
}

// EnumMeta 通用枚举项。
type EnumMeta struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// statusLabels / statusHints 把原来散落在前端的两张表收进后端。
// key 必须与 Status 常量一一对应，由 TestStatusLabelCoverage 强制。
var statusLabels = map[Status]string{
	StatusIdea:     "灵感",
	StatusTodo:     "待执行",
	StatusDoing:    "执行中",
	StatusReview:   "待评审",
	StatusApproved: "已完成",
}

var statusHints = map[Status]string{
	StatusIdea:     "未确定执行，Agent 不可见",
	StatusTodo:     "已派发，等待 Agent 领取",
	StatusDoing:    "Agent 持有租约",
	StatusReview:   "人类验收中",
	StatusApproved: "评审通过",
}

// transitions 穷举 CanTransition，from→to 的全部合法边。
// human 与 agent 两个视角分开，因为 UI 的拖拽白名单与 Agent 的领取条件不同。
func transitions(human bool) map[string][]string {
	out := make(map[string][]string, len(statusLabels))
	all := ValidStatuses()
	for _, from := range all {
		var tos []string
		for _, to := range all {
			if CanTransition(from, to, human) {
				tos = append(tos, string(to))
			}
		}
		// 全部合法目标都为空时，仍然写入空切片而非省略 key，
		// 这样前端 `meta.human_transitions[s] ?? []` 不会走到 undefined 分支。
		if tos == nil {
			tos = []string{}
		}
		out[string(from)] = tos
	}
	return out
}

// transitionGuards 穷举所有需要前置条件的边，键为 "from>to"。
//
// 键里的 ">" 选用它是因为状态名只用小写字母与下划线，不可能包含 ">"，
// 因此拼接与拆分都不会歧义 —— 不必为了"更规范"去嵌套结构，
// 前端一行 `k.split('>')` 就够。
func transitionGuards() map[string][]string {
	out := make(map[string][]string)
	for _, from := range ValidStatuses() {
		for _, to := range ValidStatuses() {
			guards := TransitionGuards(from, to, true)
			if len(guards) == 0 {
				continue
			}
			names := make([]string, 0, len(guards))
			for _, g := range guards {
				names = append(names, string(g))
			}
			out[string(from)+">"+string(to)] = names
		}
	}
	return out
}

// BuildMeta 构造 Meta。serverTime 由调用方传入（通常 time.Now().UTC()）。
func BuildMeta(serverTime string) Meta {
	statuses := make([]StatusMeta, 0, len(statusLabels))
	for _, s := range ValidStatuses() {
		statuses = append(statuses, StatusMeta{
			Value:              s,
			Label:              statusLabels[s],
			Hint:               statusHints[s],
			AgentVisible:       isAgentVisible(s),
			Terminal:           s == StatusApproved,
			CollapsedByDefault: s == StatusApproved,
		})
	}
	return Meta{
		Statuses:              statuses,
		Priorities:            priorityMeta(),
		EventTypes:            eventTypeMeta(),
		ActorTypes:            actorTypeMeta(),
		AgentKinds:            agentKindMeta(),
		SortFields:            sortFieldMeta(),
		HumanTransitions:      transitions(true),
		AgentTransitions:      transitions(false),
		HumanTransitionGuards: transitionGuards(),
		ServerTime:            serverTime,
	}
}

func isAgentVisible(s Status) bool {
	for _, v := range AgentVisibleStatuses {
		if v == s {
			return true
		}
	}
	return false
}

func priorityMeta() []EnumMeta {
	return []EnumMeta{
		{Value: "low", Label: "低"},
		{Value: "normal", Label: "普通"},
		{Value: "high", Label: "高"},
		{Value: "urgent", Label: "紧急"},
	}
}

func eventTypeMeta() []EnumMeta {
	return []EnumMeta{
		{Value: string(EventCreated), Label: "创建"},
		{Value: string(EventUpdated), Label: "编辑"},
		{Value: string(EventAssigned), Label: "指派"},
		{Value: string(EventDispatch), Label: "派发"},
		{Value: string(EventClaim), Label: "领取"},
		{Value: string(EventHeartbeat), Label: "心跳"},
		{Value: string(EventSubmit), Label: "提交成果"},
		{Value: string(EventReject), Label: "驳回"},
		{Value: string(EventApprove), Label: "评审通过"},
		{Value: string(EventRelease), Label: "退回"},
		{Value: string(EventLeaseExpire), Label: "租约超时回收"},
		{Value: string(EventNote), Label: "备注"},
		{Value: string(EventArchived), Label: "归档"},
		{Value: string(EventUnarchived), Label: "取消归档"},
		{Value: string(EventReassign), Label: "改派"},
	}
}

func actorTypeMeta() []EnumMeta {
	return []EnumMeta{
		{Value: string(ActorHuman), Label: "人类"},
		{Value: string(ActorAgent), Label: "Agent"},
		{Value: string(ActorSystem), Label: "系统"},
	}
}

func agentKindMeta() []EnumMeta {
	return []EnumMeta{
		{Value: "mcp", Label: "MCP 编辑器接入"},
		{Value: "runner", Label: "Runner 无人值守"},
	}
}

// sortFieldMeta 与 store.SortFields 保持一致；由 TestSortFieldMetaMatchesStore 强制。
func sortFieldMeta() []EnumMeta {
	// 顺序即前端下拉框的顺序，把默认项放在最前。
	//
	// manual 必须同时是**列表首项**和**服务端默认值**：
	// 只有两边一致，"看板默认按什么排"才有唯一答案。
	// 之前服务端默认 id_desc、OpenAPI 写updated_desc、前端发updated_desc，
	// 三方各说各话，选「最近更新」直接 400。
	return []EnumMeta{
		{Value: "manual", Label: "手动顺序"},
		{Value: "id_desc", Label: "最新创建"},
		{Value: "priority", Label: "优先级"},
		{Value: "updated_at", Label: "最近更新"},
		{Value: "lease", Label: "租约到期"},
		{Value: "id_asc", Label: "最早创建"},
	}
}
