/**
 * HTTP 客户端：解统一信封、把错误码转成可分支的类型。
 *
 * 设计要点：
 * 1. **后端返回 HTTP 200 也可能是错误信封**吗？不是 —— 后端保证错误用
 *    对应的 HTTP 状态码。但我们不能只看 status：契约里所有 2xx 都是
 *    `{code:"OK"}`，所以双保险地检查 code。
 * 2. `ApiError` 带 `code`，UI 应当按 code 分支（如 TASK_CONFLICT 提示刷新），
 *    **不要匹配中文文案** —— 文案随时会改。
 * 3. 查询参数序列化集中在这里，避免各处手拼 query 漏掉 archived/sort。
 */

import type {
  Agent,
  AgentCreate,
  AgentList,
  Board,
  Change,
  EventList,
  HostList,
  Meta,
  PairCode,
  PhoneDevice,
  PhoneStatus,
  Project,
  ProjectWorkdir,
  ReviewWebhook,
  PushSubscriptionBody,
  Task,
  TaskCreate,
  TaskFilter,
  TaskList,
  TaskStatus,
  TaskUpdate,
  WorkdirRef,
  DeviceScopes,
  DispatchMode,
  DispatchDecision,
  DispatchSettings,
  BudgetEntry,
  HandoffCandidate,
  Template,
  TemplatePatch,
  Schedule,
  ScheduleCreate,
  SchedulePatch,
} from './types'

/** 与 spec/implemented/010-http-contract/openapi.yaml 的 ErrorCode 枚举一致（契约测试强制同步） */
export type ErrorCode =
  | 'OK'
  | 'INVALID_INPUT'
  | 'NOT_FOUND'
  | 'INVALID_TRANSITION'
  | 'TASK_CONFLICT'
  | 'TASK_NOT_ASSIGNED'
  | 'NOT_LEASE_HOLDER'
  | 'AGENT_DISABLED'
  | 'UNAUTHORIZED'
  | 'PROJECT_IN_USE'
  | 'WORKDIR_BOUND'
  | 'FORBIDDEN'
  | 'ROUTE_NOT_FOUND'
  | 'INTERNAL'
  | 'INSUFFICIENT_BUDGET'
  | 'SUGGESTION_STALE'
  | 'HANDOFF_NOT_ALLOWED'

/**
 * ApiError 保留 HTTP 状态与机器可读 code。
 *
 * 409 有四个不同语义（INVALID_TRANSITION / TASK_CONFLICT /
 * TASK_NOT_ASSIGNED / NOT_LEASE_HOLDER），原后端把它们全映射成 409，
 * 前端只能显示一串中文 —— 用户看到"操作失败"却不知道该刷新、
 * 该重新指派还是该重新领取。
 */
export class ApiError extends Error {
  readonly code: ErrorCode
  readonly status: number
  readonly details: string[]

  constructor(code: ErrorCode, msg: string, status: number, details: string[] = []) {
    super(msg)
    this.name = 'ApiError'
    this.code = code
    this.status = status
    this.details = details
  }

  /** 版本冲突：UI 应提示"任务已被他人修改，请刷新"而不是静默重试 */
  get isConflict(): boolean {
    return this.code === 'TASK_CONFLICT'
  }

  /** 状态机不允许：UI 应提示"这一步在当前状态下不合法"并刷新状态 */
  get isTransition(): boolean {
    return this.code === 'INVALID_TRANSITION'
  }
}

interface Envelope<T> {
  code: string
  msg: string
  data?: T
  details?: string[]
}

const BASE = '/api/v1'

/** 把筛选条件序列化成 query string，空值不参与（避免 ?q= 触发无意义过滤）。 */
export function buildQuery(filter: TaskFilter): string {
  const p = new URLSearchParams()
  if (filter.status?.length) p.set('status', filter.status.join(','))
  if (filter.priority?.length) p.set('priority', filter.priority.join(','))
  if (filter.assignee !== undefined) p.set('assignee', String(filter.assignee))
  if (filter.unassigned) p.set('unassigned', 'true')
  if (filter.projects?.length) p.set('projects', filter.projects.join(','))
  if (filter.no_project) p.set('no_project', '1')
  if (filter.q?.trim()) p.set('q', filter.q.trim())
  if (filter.archived && filter.archived !== 'exclude') p.set('archived', filter.archived)
  if (filter.sort) p.set('sort', filter.sort)
  if (filter.page !== undefined) p.set('page', String(filter.page))
  if (filter.page_size !== undefined) p.set('page_size', String(filter.page_size))
  if (filter.per_column !== undefined) p.set('per_column', String(filter.per_column))
  const s = p.toString()
  return s ? `?${s}` : ''
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  let res: Response
  try {
    res = await fetch(BASE + path, {
      method,
      signal,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (e) {
    // 网络层失败（服务没起、断网）。与业务错误区分开，UI 可以给"重试"。
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError('INTERNAL', '无法连接服务，请确认 ingot 正在运行', 0)
  }

  let env: Envelope<T>
  try {
    env = (await res.json()) as Envelope<T>
  } catch {
    // 非 JSON：通常是反向代理返回了 HTML 错误页，或静态资源错位。
    throw new ApiError('INTERNAL', `服务返回了非 JSON 响应（HTTP ${res.status}）`, res.status)
  }

  if (!res.ok || env.code !== 'OK') {
    throw new ApiError(
      (env.code as ErrorCode) ?? 'INTERNAL',
      env.msg || `请求失败（HTTP ${res.status}）`,
      res.status,
      env.details ?? [],
    )
  }
  return env.data as T
}

export const api = {
  health: () => request<{ status: string; pid: number }>('GET', '/health'),

  meta: (signal?: AbortSignal) => request<Meta>('GET', '/meta', undefined, signal),

  board: (filter: TaskFilter, signal?: AbortSignal) =>
    request<Board>('GET', `/board${buildQuery(filter)}`, undefined, signal),

  listTasks: (filter: TaskFilter, signal?: AbortSignal) =>
    request<TaskList>('GET', `/tasks${buildQuery(filter)}`, undefined, signal),

  createTask: (input: TaskCreate) => request<Task>('POST', '/tasks', input),

  getTask: (id: number, signal?: AbortSignal) =>
    request<Task>('GET', `/tasks/${id}`, undefined, signal),

  updateTask: (id: number, input: TaskUpdate) =>
    request<Task>('PATCH', `/tasks/${id}`, input),

  /** 唯一改状态的入口：拖拽落位也必须走它，不能直接改 status */
  transition: (id: number, to: TaskStatus, comment?: string, toIndex?: number) =>
    request<Task>('POST', `/tasks/${id}/transition`, { to, comment, to_index: toIndex }),

  /**
   * 调整同列内的顺序（不改变状态）。
   *
   * 与 `transition` 分开而不是合并：同列拖拽不涉及状态变化，
   * 走完整的状态机路径只会多写一条无意义的事件、bump 两次 version。
   *
   * 只在 `sort=manual` 下有意义 —— 其余排序的顺序由各自字段决定，
   * 调用会成功但界面不变。调用方（Board）应预检拦截。
   */
  move: (id: number, toIndex: number, version: number) =>
    request<Task>('POST', `/tasks/${id}/move`, { to_index: toIndex, version }),

  addNote: (id: number, content: string) =>
    request<{ id: number }>('POST', `/tasks/${id}/notes`, { content }),

  listEvents: (id: number, page: number, pageSize: number, signal?: AbortSignal) =>
    request<EventList>(
      'GET',
      `/tasks/${id}/events?page=${page}&page_size=${pageSize}`,
      undefined,
      signal,
    ),

  archive: (id: number) => request<Task>('POST', `/tasks/${id}/archive`),
  unarchive: (id: number) => request<Task>('POST', `/tasks/${id}/unarchive`),

  /** 032 总管：双模开关与派发建议。 */
  dispatchSettings: (signal?: AbortSignal) =>
    request<DispatchSettings>('GET', '/dispatch/settings', undefined, signal),

  updateDispatchSettings: (mode: DispatchMode) =>
    request<DispatchSettings>('PUT', '/dispatch/settings', { mode }),

  dispatchSuggestion: (id: number, signal?: AbortSignal) =>
    request<DispatchDecision>('GET', `/tasks/${id}/dispatch-suggestion`, undefined, signal),

  acceptDispatchSuggestion: (id: number, decisionId: number) =>
    request<Task>('POST', `/tasks/${id}/dispatch/accept`, { decision_id: decisionId }),

  /** 028 通用转交：推荐目标纯查询，转交由人确认。 */
  handoffCandidates: (id: number, signal?: AbortSignal) =>
    request<{ items: HandoffCandidate[] }>('GET', `/tasks/${id}/handoff-candidates`, undefined, signal),

  handoff: (id: number, targetAgentId: number, reason: string) =>
    request<Task>('POST', `/tasks/${id}/handoff`, {
      target_agent_id: targetAgentId,
      reason,
    }),

  /** 029 任务模板：从 approved 任务沉淀，再实例化出新任务。 */
  listTemplates: (signal?: AbortSignal) =>
    request<{ items: Template[] }>('GET', '/templates', undefined, signal),

  saveAsTemplate: (sourceTaskId: number, name: string) =>
    request<Template>('POST', '/templates', { source_task_id: sourceTaskId, name }),

  updateTemplate: (id: number, patch: TemplatePatch) =>
    request<Template>('PATCH', `/templates/${id}`, patch),

  deleteTemplate: (id: number) => request<{ ok: boolean }>('DELETE', `/templates/${id}`),

  instantiateTemplate: (id: number) => request<Task>('POST', `/templates/${id}/instantiate`, {}),

  /** 031 定时计划：只在本机回环的人类端可用。 */
  listSchedules: (signal?: AbortSignal) =>
    request<{ items: Schedule[] }>('GET', '/schedules', undefined, signal),

  createSchedule: (input: ScheduleCreate) =>
    request<Schedule>('POST', '/schedules', input),

  updateSchedule: (id: number, patch: SchedulePatch) =>
    request<Schedule>('PATCH', `/schedules/${id}`, patch),

  deleteSchedule: (id: number) => request<{ ok: boolean }>('DELETE', `/schedules/${id}`),

  /** 支持的执行端清单；可用状态由服务端按本机情况实时重算。 */
  listHosts: (signal?: AbortSignal) =>
    request<HostList>('GET', '/hosts', undefined, signal),

  listAgents: (signal?: AbortSignal) =>
    request<AgentList>('GET', '/agents', undefined, signal),

  registerAgent: (input: AgentCreate) =>
    request<{ agent: Agent; token: string }>('POST', '/agents', input),

  deleteAgent: (id: number, force: boolean) =>
    request<{ reassigned_tasks: number }>('DELETE', `/agents/${id}`, { force }),

  rotateToken: (id: number) =>
    request<{ token: string }>('POST', `/agents/${id}/token`, {}),

  setAgentEnabled: (id: number, enabled: boolean) =>
    request<Agent>('POST', `/agents/${id}/enabled`, { enabled }),

  setAgentQuota: (id: number, quota: Agent['quota']) =>
    request<Agent>('POST', `/agents/${id}/quota`, { quota }),

  /** 032-B 数值预算：录入 / 校准 / 清空余额（null 清回未知）。 */
  setAgentBalance: (id: number, balance: number | null) =>
    request<Agent>('POST', `/agents/${id}/balance`, { balance }),

  agentLedger: (id: number, signal?: AbortSignal) =>
    request<{ items: BudgetEntry[] }>('GET', `/agents/${id}/ledger`, undefined, signal),

  listWorkdirs: (signal?: AbortSignal) =>
    request<{ items: WorkdirRef[] }>('GET', '/workdirs', undefined, signal),

  // ---- 项目 ↔ 工作目录（025-project-workdirs）----

  listProjectWorkdirs: (projectId: number, signal?: AbortSignal) =>
    request<ProjectWorkdir[]>('GET', `/projects/${projectId}/workdirs`, undefined, signal),

  addProjectWorkdir: (projectId: number, workdir: string) =>
    request<ProjectWorkdir>('POST', `/projects/${projectId}/workdirs`, { workdir }),

  removeProjectWorkdir: (projectId: number, wid: number) =>
    request<null>('DELETE', `/projects/${projectId}/workdirs/${wid}`),

  // ---- 项目（024-project）----

  listProjects: (includeArchived = false, signal?: AbortSignal) =>
    request<Project[]>('GET', `/projects${includeArchived ? '?include_archived=1' : ''}`, undefined, signal),

  createProject: (name: string, color = '') => request<Project>('POST', '/projects', { name, color }),

  updateProject: (id: number, name: string, color = '') =>
    request<Project>('PATCH', `/projects/${id}`, { name, color }),

  archiveProject: (id: number) => request<Project>('POST', `/projects/${id}/archive`),

  unarchiveProject: (id: number) => request<Project>('POST', `/projects/${id}/unarchive`),

  deleteProject: (id: number) => request<null>('DELETE', `/projects/${id}`),

  reviewWebhook: (signal?: AbortSignal) =>
    request<ReviewWebhook>('GET', '/notify/review-webhook', undefined, signal),

  updateReviewWebhook: (input: ReviewWebhook) =>
    request<ReviewWebhook>('PUT', '/notify/review-webhook', input),

  phoneStatus: (signal?: AbortSignal) =>
    request<PhoneStatus>('GET', '/phone/status', undefined, signal),

  updatePhoneSettings: (input: { lan_enabled: boolean; remote_enabled: boolean }) =>
    request<PhoneStatus>('PUT', '/phone/settings', input),

  createPairCode: (scopes?: DeviceScopes) =>
    request<PairCode>('POST', '/phone/pair-codes', scopes ? { scopes } : undefined),

  listPhoneDevices: (signal?: AbortSignal) =>
    request<{ items: PhoneDevice[] }>('GET', '/phone/devices', undefined, signal),

  revokePhoneDevice: (id: number) =>
    request<{ ok: boolean }>('POST', `/phone/devices/${id}/revoke`),

  pairDevice: (code: string, name?: string) =>
    request<{ device: PhoneDevice; token: string }>('POST', '/devices/pair', { code, name }),

  vapidPublicKey: () => request<{ public_key: string }>('GET', '/push/vapid-public-key'),

  putPushSubscription: (body: PushSubscriptionBody) =>
    request<{ ok: boolean }>('PUT', '/push/subscription', body),

  /** 本机原生目录对话框；取消时 cancelled=true 且 path 为空 */
  pickDirectory: (initial?: string) =>
    request<{ path: string; cancelled: boolean }>('POST', '/system/pick-directory', {
      initial: initial || undefined,
    }),
}

/** 供 SSE 单例复用同一套错误语义。 */
export type { Change }
