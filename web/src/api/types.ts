/**
 * 领域类型 —— 全部对应 spec/implemented/010-http-contract/openapi.yaml 的 components.schemas。
 *
 * ⚠️ 唯一真相在 OpenAPI：`npm run gen:api` 可从契约生成 schema.ts 做交叉
 * 校验，但这里刻意手写而不是直接用生成物：
 *
 * 1. 生成类型是 `components["schemas"]["Task"]`，写起来冗长且无法表达
 *    "服务端保证非空"这类业务约束；
 * 2. 时间字段生成的是 string，而我们需要能区分"过期/未过期"的判断；
 * 3. 手写版本让 `internal/api/contract_test.go` 之外还有一层人工锚点 ——
 *    改契约时两处一起改，review 时能看出是否漏改。
 *
 * 改动 Task/Agent/Meta 结构时，必须同步 spec/implemented/010-http-contract/openapi.yaml。
 */

export type TaskStatus = 'idea' | 'todo' | 'doing' | 'review' | 'approved'
export type Priority = 'urgent' | 'high' | 'normal' | 'low'
export type ActorType = 'human' | 'agent' | 'system'
export type AgentKind = 'mcp'

/**
 * 排序字段。必须与 `meta.sort_fields` 完全一致。
 *
 * 之前这里是 `updated_desc` / `updated_asc`，而后端白名单只有
 * `updated_at` / `id_desc` —— 用户在筛选栏点「最近更新」直接拿到
 * 400。现由后端 `TestSortEnumMatchesBackend` 强制四方一致。
 *
 * 只有 `manual` 下同列拖拽重排才有意义；其余排序由各自字段决定顺序，
 * 此时重排会"成功但看不见"，因此前端要预检拦截。
 */
export type SortField =
  | 'manual'
  | 'id_desc'
  | 'id_asc'
  | 'priority'
  | 'updated_at'
  | 'lease'

export type ArchivedMode = 'exclude' | 'only' | 'any'

/**
 * 支持的执行端（027-host-registry）。
 * 字段与 GET /api/v1/hosts 一致，数据来自后端 internal/hosts 注册表。
 */
export interface HostInfo {
  name: string
  label: string
  display_name: string
  kind: 'mcp' | 'runner'
  serves: 'small' | 'floor' | 'large'
  cost_rank: number
  available: boolean
  /** 可用时为空字符串 */
  missing_reason: string
  guide: string
  doc_anchor: string
}

export interface HostList {
  items: HostInfo[]
}

/** 032 总管的双模开关。observe 只给建议，auto 才自动定人。 */
export type DispatchMode = 'observe' | 'auto'

export interface DispatchSettings {
  mode: DispatchMode
  modes: DispatchMode[]
}

/** 032 总管的不可变决策快照；采纳前服务端会重验执行端状态。 */
export interface DispatchDecision {
  id: number
  task_id: number
  mode: DispatchMode
  category: string
  tier: string
  chosen_agent_id: number | null
  cost_estimate: number
  score_total: number
  score_json: string
  snapshot_json: string
  reason: string
  source: 'manual' | 'schedule' | 'handoff'
  schedule_id: number | null
  status: 'pending' | 'accepted' | 'stale' | 'superseded'
  observed_at: string
  expires_at: string
  accepted_at: string
}

export interface Task {
  id: number
  title: string
  description: string
  acceptance_criteria: string
  workdir: string
  status: TaskStatus
  priority: Priority
  /** null = 无人认领 */
  assignee_agent_id: number | null
  /** 租约到期；已超时但未回收时前端要显示"已超时" */
  lease_until: string | null
  archived_at: string | null
  /** 列内位置（1 起），只在同一 status 内有意义 */
  sort_order: number
  /** 乐观锁版本，写操作必须带上服务端给的当前值 */
  version: number
  created_at: string
  updated_at: string
  /** 工作类别。空字符串表示尚未分类 */
  category: '' | 'code_tool' | 'code_game' | 'copy' | 'local_ops'
  /** 执行档位 */
  tier: '' | 'small' | 'floor' | 'large'
  /** 为什么派给当前 Agent */
  route_reason: string
  /** 所属项目；null 表示无项目 */
  project_id: number | null
  /** 总管预估成本（credit）；未派发时为 0 */
  estimated_cost: number
  /** Agent 可选上报的实际成本；null = 未上报（与 0 有区别） */
  cost_used: number | null
}

/** 项目（任务的展示分组，见 024-project） */
export interface Project {
  id: number
  name: string
  /** 空串表示无颜色；否则为 #rgb 或 #rrggbb */
  color: string
  archived_at: string | null
  created_at: string
  updated_at: string
}

/** 项目与工作目录的绑定关系（025）。不反写任务的 workdir/project_id */
export interface ProjectWorkdir {
  id: number
  project_id: number
  workdir: string
  created_at: string
}

/** 最近目录 + 所属项目 id 列表（025 反查，GET /workdirs 条目） */
export interface WorkdirRef {
  workdir: string
  project_ids: number[]
}

export interface TaskEvent {
  id: number
  task_id: number
  actor_type: ActorType
  actor_id: number | null
  actor_name: string
  type: string
  from_status: TaskStatus | null
  to_status: TaskStatus | null
  message: string
  created_at: string
}

export interface Agent {
  id: number
  name: string
  kind: AgentKind | string
  enabled: boolean
  /** 服务端按 last_seen_at 推导（30 分钟阈值），前端不要自己算 */
  online: boolean
  last_seen_at: string | null
  created_at: string
  quota: 'unknown' | 'ok' | 'exhausted'
  serves: 'small' | 'floor' | 'large'
  cost_rank: number
  /** 数值余额（credit）；null = 未录入，总管预算门禁跳过 */
  balance: number | null
  balance_unit: string
  /** 最近一次录入 / 扣减时间，用于提示数据新鲜度 */
  budget_updated_at: string | null
}

/** 029 派发期任务模板。实例任务是独立副本。 */
export interface Template {
  id: number
  name: string
  title: string
  description: string
  acceptance_criteria: string
  workdir: string
  priority: Priority
  category: Task['category']
  tier: Task['tier']
  assignee_agent_id: number | null
  project_id: number | null
  archived_at: string | null
  created_at: string
  updated_at: string
}

export interface TemplatePatch {
  name?: string
  title?: string
  description?: string
  acceptance_criteria?: string
  workdir?: string
  priority?: Priority
  category?: Task['category']
  tier?: Task['tier']
  assignee_agent_id?: number | null
  project_id?: number | null
  archived?: boolean
}

/** 031 定时计划：人预先授权的代派发，实例化仍走 029 模板与 032 派发链路。 */
export type ScheduleDispatchMode = 'inherit' | 'observe' | 'auto'

export interface Schedule {
  id: number
  name: string
  template_id: number
  dispatch_mode: ScheduleDispatchMode
  interval_minutes: number | null
  daily_at: string | null
  enabled: boolean
  next_run_at: string
  last_fired_at: string | null
  created_at: string
  updated_at: string
}

export interface ScheduleCreate {
  name: string
  template_id: number
  dispatch_mode?: ScheduleDispatchMode
  interval_minutes?: number | null
  daily_at?: string | null
}

export interface SchedulePatch {
  name?: string
  template_id?: number
  dispatch_mode?: ScheduleDispatchMode
  interval_minutes?: number | null
  daily_at?: string | null
  enabled?: boolean
}

/** 028 总管给出的转交推荐目标。只作建议，最终目标由人选定。 */
export interface HandoffCandidate {
  agent_id: number
  name: string
  score: number
  reason: string
}

/** 032-B 余额流水条目。delta 为负表示扣减。 */
export interface BudgetEntry {
  id: number
  agent_id: number
  task_id: number | null
  delta: number
  reason: 'human_set' | 'human_clear' | 'task_settled' | 'probe_exhausted'
  actor_type: 'human' | 'agent' | 'system'
  created_at: string
}

/**
 * 看板数据。
 *
 * `columns` 是**对象**而非数组：每个合法状态都有键（空列为 `[]`）。
 * 选对象而非数组是因为前端要按状态直接取 —— 数组还得先 find，
 * 而且要防"某状态不存在"（后端曾用 GROUP BY 导致空列缺失）。
 */
export interface Board {
  columns: Partial<Record<TaskStatus, Task[]>>
  /** 各状态完整计数（不受 per_column 截断影响） */
  counts: Partial<Record<TaskStatus, number>>
  /**
   * **按列**的截断标记。
   *
   * 用 map 而不是单个 bool：只有真正被截断的列才需要显示
   * "仅显示 20/57 条"，单一 bool 会让 5 列全挂上无意义提示。
   */
  truncated: Partial<Record<TaskStatus, boolean>>
}

export interface TaskList {
  /** 恒为数组，空列表是 [] 而非 null */
  items: Task[]
  /** 筛选后总条数，不受分页影响 */
  total: number
}

export interface EventList {
  items: TaskEvent[]
  total: number
  has_more: boolean
}

export interface AgentList {
  items: Agent[]
}

export interface StatusMeta {
  value: TaskStatus
  label: string
  hint: string
  agent_visible: boolean
  terminal: boolean
  /** 看板默认折叠（如 approved） */
  collapsed_by_default: boolean
}

export interface LabeledValue {
  value: string
  label: string
}

export interface Meta {
  statuses: StatusMeta[]
  priorities: LabeledValue[]
  event_types: string[]
  actor_types: string[]
  agent_kinds: string[]
  /**
   * 排序字段的可选值。
   *
   * ⚠️ 这里原本声明成 `string[]`，但后端返回的是 `{value,label,hint}` 对象
   * 序列 —— 与statuses / priorities 同构。声明错形状不会报错，
   * 只会在用到 label 时拿到 undefined。
   * 由后端 `TestMetaSchemaKeysMatchResponse` 锁死字段名，
   * 前端侧由「排序下拉从 meta 渲染」这条路径在E2E 里覆盖。
   */
  sort_fields: LabeledValue[]
  /**
   * 人类端合法流转图，键为当前状态。**看板拖拽用这张**。
   *
   * 为什么分两张图：同一个状态在不同角色下合法去向不同。
   * `todo → doing` 是 Agent 领取的动作，人类不该通过拖拽触发；
   * 若让前端用一张合并的图，人类就能把任务拖进「执行中」，
   * 绕过 Agent 领取 —— 租约与指派会与界面显示脱节。
   */
  human_transitions: Record<string, TaskStatus[]>
  /** Agent 端合法流转图（领取/提交/退回），人类看板不用。 */
  agent_transitions: Record<string, TaskStatus[]>
  /**
   * 人类流转的**前置条件**，键为 `from>to`。
   *
   * 与 human_transitions 分开而不是合并，因为语义不同：
   * 那张表回答"这条边能不能走"，这张表回答"还差什么"。
   *
   * 没有它会怎样：`idea → todo` 在流转图里是合法的（人类确实可以派发），
   * 但服务端要求派发前必须已指派 Agent —— 未指派的任务进了「待执行」，
   * Agent 端 claim 因身份不匹配而永远无人领取，变成死任务。
   * 前端拿不到这条规则，只能让用户拖过去、再被 409 弹回，
   * 表现为"拖不动"且原因不明。
   */
  human_transition_guards: Record<string, TransitionGuard[]>
  /** 前端时钟基准，用于租约倒计时 */
  server_time: string
}

/**
 * 流转守卫类型，与后端 domain.Guard 一一对应。
 *
 * 这不是普通字符串：加一个新守卫时若忘了处理，
 * TypeScript 会在这里报错，而不是让拖拽静默失效。
 */
export type TransitionGuard = 'requires_assignee' | 'requires_comment'

/** SSE 推送的变更负载 */
export interface Change {
  seq: number
  kind: 'task' | 'agent' | 'column' | 'project'
  task_id?: number
  task?: Task
  event?: TaskEvent
  agent?: Agent
  /** 仅 kind='column'：被重排的列 */
  status?: string
  /** 仅 kind='column'：该列当前的完整任务 ID 顺序，按它重排本地缓存即可 */
  column_ids?: number[]
  /** 仅 kind='project'：变更后的项目（删除时只带 id） */
  project?: Pick<Project, 'id'> & Partial<Project>
}

export type ResyncReason = 'buffer_gap' | 'slow_consumer'

// ---- 请求体 ----

export interface TaskCreate {
  title: string
  description?: string
  acceptance_criteria?: string
  workdir?: string
  priority?: Priority
  assignee_agent_id?: number | null
  /** 挂到该项目；null/省略表示无项目 */
  project_id?: number | null
}

/** 全量覆盖语义：未提供的字段会被清空，前端必须提交完整表单 */
export interface TaskUpdate {
  title: string
  description?: string
  acceptance_criteria?: string
  workdir?: string
  priority?: Priority
  assignee_agent_id?: number | null
  category?: Task['category']
  version: number
  /** null 清空项目归属 */
  project_id?: number | null
}

export interface AgentCreate {
  name: string
  kind?: AgentKind | string
}

/** 待评审出站地址。空字符串表示不发。 */
export interface ReviewWebhook {
  url: string
}

/** 看板 / 列表的筛选条件（全部服务端执行） */
export interface PhoneStatus {
  lan_enabled: boolean
  remote_enabled: boolean
  lan_url: string
  remote_url: string
  remote_error: string
  tailscale_installed: boolean
}

export interface PairLink {
  kind: string
  url: string
  qr_png: string
}

export interface PairCode {
  code: string
  expires_at: string
  links: PairLink[]
}

/** 设备令牌范围操作名（017），与路由 operationId 一一同名 */
export type DeviceScopeOp =
  | 'health'
  | 'getMeta'
  | 'getBoard'
  | 'listTasks'
  | 'getTask'
  | 'createTask'
  | 'updateTask'
  | 'transitionTask'
  | 'addTaskNote'
  | 'listTaskEvents'
  | 'archiveTask'
  | 'unarchiveTask'
  | 'listAgents'
  | 'streamEvents'
  | 'listWorkdirs'
  | 'listProjects'
  | 'createProject'
  | 'updateProject'
  | 'archiveProject'
  | 'unarchiveProject'
  | 'deleteProject'
  | 'listProjectWorkdirs'
  | 'addProjectWorkdir'
  | 'removeProjectWorkdir'

/** 设备令牌范围（017）。范围在配对码兑换时随设备落库，签发后不可改 */
export interface DeviceScopes {
  ops: DeviceScopeOp[]
  project_ids: number[]
}

export interface PhoneDevice {
  id: number
  name: string
  created_at: string
  last_seen_at: string | null
  /** 存量设备（scopes 列 NULL）后端 omitempty 省略本字段，语义为全量放行 */
  scopes?: DeviceScopes
}

export interface PushSubscriptionBody {
  endpoint: string
  keys: { p256dh: string; auth: string }
}

export interface TaskFilter {
  status?: TaskStatus[]
  priority?: Priority[]
  assignee?: number
  unassigned?: boolean
  /** 项目多选；空/缺省不过滤 */
  projects?: number[]
  /** 只看无项目任务 */
  no_project?: boolean
  q?: string
  archived?: ArchivedMode
  sort?: SortField
  page?: number
  page_size?: number
  per_column?: number
}
