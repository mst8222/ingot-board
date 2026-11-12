/**
 * 数据获取 hooks。
 *
 * 所有查询都经过这里，不在组件里直接调 api —— 这样失效策略、缓存键、
 * 错误语义只有一处定义。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError, api } from '@/api/client'
import type { Agent, AgentCreate, DeviceScopes, DispatchDecision, DispatchMode, ScheduleCreate, SchedulePatch, TaskCreate, TaskFilter, TaskStatus, TaskUpdate } from '@/api/types'
import { invalidateTaskCaches, qk } from '@/store/query'
import { syncClock } from '@/lib/time'

/** 元数据：状态机、中文标签、服务端时钟基准。 */
export function useMeta() {
  return useQuery({
    queryKey: qk.meta(),
    queryFn: ({ signal }) => api.meta(signal),
    staleTime: Infinity, // 枚举与状态机不随运行变化
  })
}

/** 拉取 meta 并顺带校准时钟。挂在 App 顶层。 */
export function useClockSync() {
  const q = useMeta()
  if (q.data?.server_time) syncClock(q.data.server_time)
}

export function useAgents() {
  return useQuery({
    queryKey: qk.agents(),
    queryFn: ({ signal }) => api.listAgents(signal),
  })
}

/** 支持的执行端清单（027-host-registry）：来自注册表，可用状态实时重算。 */
export function useHosts() {
  return useQuery({
    queryKey: qk.hosts(),
    queryFn: ({ signal }) => api.listHosts(signal),
  })
}

/** 032 总管的双模开关。切换后看板的拖拽预检会跟着变化。 */
export function useDispatchSettings() {
  return useQuery({
    queryKey: qk.dispatchSettings(),
    queryFn: ({ signal }) => api.dispatchSettings(signal),
  })
}

export function useSetDispatchMode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (mode: DispatchMode) => api.updateDispatchSettings(mode),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.dispatchSettings() })
    },
  })
}

/** observe 下未指派任务的建议；404 表示暂无建议，不当成错误重试。 */
export function useDispatchSuggestion(taskId: number | null) {
  return useQuery<DispatchDecision>({
    queryKey: qk.dispatchSuggestion(taskId ?? 0),
    queryFn: ({ signal }) => api.dispatchSuggestion(taskId as number, signal),
    enabled: taskId !== null,
    retry: false,
  })
}

export function useAcceptSuggestion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ taskId, decisionId }: { taskId: number; decisionId: number }) =>
      api.acceptDispatchSuggestion(taskId, decisionId),
    onSuccess: () => {
      void invalidateTaskCaches(qc)
      void qc.invalidateQueries({ queryKey: qk.dispatchSettings() })
    },
  })
}

/** 029 任务模板：存 / 实例化 / 删除后刷新模板列表与看板。 */
export function useTemplates() {
  return useQuery({
    queryKey: qk.templates(),
    queryFn: ({ signal }) => api.listTemplates(signal),
  })
}

export function useSaveAsTemplate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ taskId, name }: { taskId: number; name: string }) =>
      api.saveAsTemplate(taskId, name),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.templates() }),
  })
}

export function useInstantiateTemplate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.instantiateTemplate(id),
    onSuccess: () => void invalidateTaskCaches(qc),
  })
}

export function useDeleteTemplate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.deleteTemplate(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.templates() }),
  })
}

/** 031 定时计划：调度器只在服务进程内运行，关闭期间不执行也不补偿。 */
export function useSchedules() {
  return useQuery({
    queryKey: qk.schedules(),
    queryFn: ({ signal }) => api.listSchedules(signal),
  })
}

export function useCreateSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: ScheduleCreate) => api.createSchedule(input),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.schedules() }),
  })
}

export function useUpdateSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: SchedulePatch }) =>
      api.updateSchedule(id, patch),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.schedules() }),
  })
}

export function useDeleteSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.deleteSchedule(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.schedules() }),
  })
}

/** 028 转交推荐目标：纯查询，不改任务。 */
export function useHandoffCandidates(taskId: number | null) {
  return useQuery({
    queryKey: qk.handoffCandidates(taskId ?? 0),
    queryFn: ({ signal }) => api.handoffCandidates(taskId as number, signal),
    enabled: taskId !== null,
  })
}

export function useHandoff() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ taskId, targetAgentId, reason }: {
      taskId: number
      targetAgentId: number
      reason: string
    }) => api.handoff(taskId, targetAgentId, reason),
    onSuccess: (_data, vars) => {
      void invalidateTaskCaches(qc)
      void qc.invalidateQueries({ queryKey: qk.handoffCandidates(vars.taskId) })
      void qc.invalidateQueries({ queryKey: qk.agents() })
    },
  })
}

/** 032-B 余额流水；录入 / 校准 / 清空后同时刷新执行端与流水。 */
export function useAgentLedger(agentId: number) {
  return useQuery({
    queryKey: qk.agentLedger(agentId),
    queryFn: ({ signal }) => api.agentLedger(agentId, signal),
  })
}

export function useSetAgentBalance() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, balance }: { id: number; balance: number | null }) =>
      api.setAgentBalance(id, balance),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: qk.agents() })
      void qc.invalidateQueries({ queryKey: qk.agentLedger(vars.id) })
      void qc.invalidateQueries({ queryKey: qk.dispatchSettings() })
    },
  })
}

export function useBoard(filter: TaskFilter) {
  return useQuery({
    queryKey: qk.board(filter),
    queryFn: ({ signal }) => api.board(filter, signal),
    // 看板由 SSE 驱动更新，不做轮询兜底 —— 轮询会在有人同时操作时
    // 与 SSE 打架，出现"卡片跳回去"的闪烁。
    refetchInterval: false,
  })
}

export function useTasks(filter: TaskFilter) {
  return useQuery({
    queryKey: qk.tasks(filter),
    queryFn: ({ signal }) => api.listTasks(filter, signal),
    placeholderData: (prev) => prev, // 翻页时保留上一页，避免整页闪白
  })
}

export function useTask(id: number | null) {
  return useQuery({
    queryKey: qk.task(id ?? 0),
    queryFn: ({ signal }) => api.getTask(id as number, signal),
    enabled: id !== null,
  })
}

export function useTaskEvents(id: number | null, page: number, pageSize: number) {
  return useQuery({
    queryKey: qk.taskEvents(id ?? 0, page, pageSize),
    queryFn: ({ signal }) => api.listEvents(id as number, page, pageSize, signal),
    enabled: id !== null,
    placeholderData: (prev) => prev,
  })
}

/** 统一的 mutation 失效策略：任何写操作后重拉看板。 */
function useInvalidatingMutation<TArgs, TResult>(
  fn: (args: TArgs) => Promise<TResult>,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => invalidateTaskCaches(qc),
  })
}

export function useCreateTask() {
  return useInvalidatingMutation((input: TaskCreate) => api.createTask(input))
}

export function useUpdateTask() {
  return useInvalidatingMutation(({ id, input }: { id: number; input: TaskUpdate }) =>
    api.updateTask(id, input),
  )
}

export function useTransition() {
  return useInvalidatingMutation(
    ({ id, to, comment, toIndex }: {
      id: number
      to: TaskStatus
      comment?: string
      toIndex?: number
    }) => api.transition(id, to, comment, toIndex),
  )
}

/**
 * 同列重排。
 *
 * 失效策略与其他写操作一致（重拉看板）：拖拽发起方已经做过乐观更新，
 * 再拉一次就能拿到服务端权威顺序并顺带刷新 version。
 *
 * 其他客户端走 SSE 的 `column` 帧本地重排，不需要重拉 ——
 * 重排是高频操作，每次都重拉会让多端操作时界面持续闪烁。
 */
export function useMoveTask() {
  return useInvalidatingMutation(
    ({ id, toIndex, version }: { id: number; toIndex: number; version: number }) =>
      api.move(id, toIndex, version),
  )
}

export function useAddNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, content }: { id: number; content: string }) => api.addNote(id, content),
    // 加备注不影响看板，只刷新时间线
    onSuccess: (_d, v) => {
      void qc.invalidateQueries({ queryKey: ['taskEvents', v.id] })
    },
  })
}

export function useArchiveTask() {
  return useInvalidatingMutation(({ id, archived }: { id: number; archived: boolean }) =>
    archived ? api.archive(id) : api.unarchive(id),
  )
}

export function useRegisterAgent() {
  return useInvalidatingMutation((input: AgentCreate) => api.registerAgent(input))
}

export function useDeleteAgent() {
  return useInvalidatingMutation(({ id, force }: { id: number; force: boolean }) =>
    api.deleteAgent(id, force),
  )
}

export function useRotateToken() {
  return useInvalidatingMutation((id: number) => api.rotateToken(id))
}

export function useSetAgentQuota() {
  return useInvalidatingMutation(({ id, quota }: { id: number; quota: Agent['quota'] }) =>
    api.setAgentQuota(id, quota),
  )
}

export function useSetAgentEnabled() {
  return useInvalidatingMutation(({ id, enabled }: { id: number; enabled: boolean }) =>
    api.setAgentEnabled(id, enabled),
  )
}

export function useReviewWebhook(enabled: boolean) {
  return useQuery({
    queryKey: ['reviewWebhook'],
    queryFn: ({ signal }) => api.reviewWebhook(signal),
    enabled,
  })
}

export function useUpdateReviewWebhook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.updateReviewWebhook,
    onSuccess: (data) => {
      qc.setQueryData(['reviewWebhook'], data)
    },
  })
}

export function usePhoneStatus(enabled: boolean) {
  return useQuery({
    queryKey: ['phoneStatus'],
    queryFn: ({ signal }) => api.phoneStatus(signal),
    enabled,
  })
}

export function usePhoneDevices(enabled: boolean) {
  return useQuery({
    queryKey: ['phoneDevices'],
    queryFn: ({ signal }) => api.listPhoneDevices(signal),
    enabled,
  })
}

export function useWorkdirs() {
  return useQuery({
    queryKey: ['workdirs'],
    queryFn: ({ signal }) => api.listWorkdirs(signal),
  })
}

/**
 * 项目列表（**含已归档**），FilterBar 与任务表单共用。
 *
 * 为什么不排除归档：URL 里可能选中了一个已归档项目的筛选，
 * chips 若因列表缺失而消失，用户既看不到"为什么看板变空了"，
 * 也无法取消这个筛选。归档与否由各消费方自行过滤。
 */
export function useProjects() {
  return useQuery({
    queryKey: ['projects'],
    queryFn: ({ signal }) => api.listProjects(true, signal),
  })
}

// ---- 项目 CRUD（025 项目管理对话框用；024 时无 UI，这里补齐）----

export function useCreateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, color }: { name: string; color?: string }) =>
      api.createProject(name, color),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useUpdateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, name, color }: { id: number; name: string; color?: string }) =>
      api.updateProject(id, name, color),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useArchiveProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.archiveProject,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useUnarchiveProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.unarchiveProject,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useDeleteProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deleteProject,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

// ---- 项目 ↔ 工作目录（025-project-workdirs）----

export function useProjectWorkdirs(projectId: number | null) {
  return useQuery({
    queryKey: ['projectWorkdirs', projectId],
    queryFn: ({ signal }) => api.listProjectWorkdirs(projectId!, signal),
    enabled: projectId != null,
  })
}

export function useAddProjectWorkdir() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ projectId, workdir }: { projectId: number; workdir: string }) =>
      api.addProjectWorkdir(projectId, workdir),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ['projectWorkdirs', vars.projectId] })
      void qc.invalidateQueries({ queryKey: ['workdirs'] })
    },
  })
}

export function useRemoveProjectWorkdir() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ projectId, wid }: { projectId: number; wid: number }) =>
      api.removeProjectWorkdir(projectId, wid),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ['projectWorkdirs', vars.projectId] })
      void qc.invalidateQueries({ queryKey: ['workdirs'] })
    },
  })
}

export function useUpdatePhoneSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.updatePhoneSettings,
    onSuccess: (data) => {
      qc.setQueryData(['phoneStatus'], data)
    },
  })
}

export function useCreatePairCode() {
  return useMutation({
    /** scopes 为 undefined 时不发 body，后端走缺省范围（026） */
    mutationFn: (scopes?: DeviceScopes) => api.createPairCode(scopes),
  })
}

export function useRevokePhoneDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.revokePhoneDevice,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['phoneDevices'] })
    },
  })
}

export function usePairDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => api.pairDevice(code),
    onSuccess: () => {
      void qc.invalidateQueries()
    },
  })
}

/** 把任意异常转成可展示的文案。 */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return '未知错误'
}

/**
 * 按错误码给出**可执行的**下一步提示。
 *
 * 单纯显示"操作失败"是不够的：409 有四种语义，用户需要知道该刷新、
 * 重新指派还是重新领取。
 */
export function errorHint(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null
  switch (e.code) {
    case 'TASK_CONFLICT':
      return '任务已被其他操作修改，界面已自动刷新，请确认后重试'
    case 'INVALID_TRANSITION':
      return '当前状态不允许这一步，界面已自动刷新'
    case 'TASK_NOT_ASSIGNED':
      return '该任务未指派给当前 Agent'
    case 'NOT_LEASE_HOLDER':
      return '租约已过期或不属于当前 Agent，任务可能已被回收'
    case 'AGENT_DISABLED':
      return '该 Agent 已被禁用'
    case 'UNAUTHORIZED':
      return 'Agent 令牌无效或已被禁用'
    case 'PROJECT_IN_USE':
      return '项目仍被任务引用，先把任务移出或删除后再删项目'
    case 'WORKDIR_BOUND':
      return '该目录已绑定到该项目'
    case 'FORBIDDEN':
      return '设备令牌没有这项权限'
    default:
      return null
  }
}
