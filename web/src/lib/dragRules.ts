/**
 * 拖拽规则 —— 纯函数，无 React、无 dnd-kit 依赖。
 *
 * 为什么要单独抽出来：拖拽是本项目最容易出静默错误的地方
 * （拖不动、拖错列、乐观更新后被服务端打回、点击被误判成拖拽），
 * 而这些都要在组件之外才能可靠地测。
 *
 * 两条核心决策：
 *
 * 1. **服务端权威**：拖拽只改视觉，落位后调 `transitionTask`。
 *    前端不猜测"能不能从 A 到 B" —— 虽然 meta.transitions 给了规则，
 *    但拿它做前置判断只是优化，真正决定成败的是服务端响应。
 *
 * 2. **拖拽与点击消歧**：dnd-kit 的 `distance: 4` 意味着移动不足 4px
 *    不触发拖拽。原 Vue 前端用 vuedraggable 的 `delay: 180`，导致
 *    快速点击卡片会延迟 180ms 才响应 —— 明显的"卡一下"手感。
 */

import type { Agent, Task, TaskStatus, TransitionGuard } from '@/api/types'

/** 看板列顺序 = 状态机推进顺序，不另设配置。 */
export const COLUMN_ORDER: readonly TaskStatus[] = [
  'idea',
  'todo',
  'doing',
  'review',
  'approved',
] as const

/** 允许放置的空列区域 id 约定。 */
export const EMPTY_DROP_ZONE = 'empty-drop'

export function isTaskStatus(v: string): v is TaskStatus {
  return (COLUMN_ORDER as readonly string[]).includes(v)
}

/**
 * 判断目标状态是否合法。
 *
 * 注意 `approved` 是终态：`CanTransition` 里没有 approved → 任何状态，
 * 所以看板列虽然展示 approved，但**不能往里拖**（只能由 review 前进到它）。
 */
export function canDropInto(
  from: TaskStatus,
  to: TaskStatus,
  transitions: Record<string, TaskStatus[]>,
): boolean {
  if (from === to) return false
  const allowed = transitions[from]
  if (!allowed) return false
  return allowed.includes(to)
}

/**
 * 计算一次拖拽的结果。
 *
 * 返回 `null` 表示这次拖拽应当被忽略（放回原位，不发请求）。
 * 这样组件里只需 `if (!plan) return`，不必到处重复判断。
 */
export interface DropPlan {
  taskId: number
  from: TaskStatus
  to: TaskStatus
  /**
   * 落位下标（0 基）。
   *
   * 同列重排 = 精确下标；跨列 = 目标列中的下标。
   * null 表示"追加到目标列末尾"，对应调用方未指定落点。
   *
   * ⚠️ 早先这里是 `number | null` 且跨列一律给 null，
   * 同时注释写着"服务端不存列内顺序"——那正是本次要修的缺口。
   */
  toIndex: number | null
}

export function planDrop(args: {
  task: Task
  to: TaskStatus
  transitions: Record<string, TaskStatus[]>
  /** 落位下标（0 基）；跨列时可省略表示追加到末尾 */
  toIndex?: number
  /** 目标列当前顺序，用于判断是否真的变了 */
  orderedIds?: readonly number[]
}): DropPlan | null {
  const { task, to, transitions, toIndex, orderedIds } = args

  if (task.status === to) {
    // 同列重排：只改列内顺序，不改状态，因此**不查流转表**
    // （canDropInto 故意排除同状态自移，否则这里永远被拒）。
    // 只有顺序真的变了才算一次操作。
    if (toIndex === undefined || !orderedIds) return null
    const currentIndex = orderedIds.indexOf(task.id)
    if (currentIndex < 0 || currentIndex === toIndex) return null

    return { taskId: task.id, from: task.status, to, toIndex }
  }

  if (!canDropInto(task.status, to, transitions)) {
    // 非法流转：返回 null 让调用方"弹回"，不请求服务端。
    // 发请求也没用（会 409），但会浪费一轮往返并在 UI 上闪一下。
    return null
  }

  return {
    taskId: task.id,
    from: task.status,
    to,
    // 跨列时目标下标同样要传：拖到列中间某个位置与拖到末尾，
    // 用户意图完全不同，丢掉下标等于把用户的操作替换成另一个。
    toIndex: toIndex ?? null,
  }
}

/**
 * 重排会被静默丢弃的两种情况。
 *
 * 这两类都必须**提前拒绝**而不是"做了但看不见"：
 *
 * 1. 非手动排序：sort_order 不参与读取，落位后界面顺序不变。
 * 2. 筛选中：落位下标是筛选后子集内的下标，服务端无法唯一映射到完整列，
 *    存下来的位置与用户看到的不一致。
 *
 * 返回文案要明确告诉用户下一步做什么 —— 只说"不能拖"等于没说。
 */
export interface ReorderBlock {
  reason: 'not_manual_sort' | 'filtered'
  message: string
}

export function checkReorderable(args: {
  /** 当前排序；undefined 表示未设置，即服务端默认（手动顺序） */
  sort?: string
  /** 当前是否有任何筛选条件生效 */
  filtered: boolean
}): ReorderBlock | null {
  const { sort, filtered } = args
  // 空值走服务端默认，而默认就是 manual
  const effective = sort ?? 'manual'

  if (effective !== 'manual') {
    return {
      reason: 'not_manual_sort',
      message: '当前按「最近更新 / 优先级」等排序，列内顺序由排序决定。把排序切回「手动顺序」即可自由拖动。',
    }
  }
  if (filtered) {
    return {
      reason: 'filtered',
      message: '筛选状态下看到的顺序只是完整列的一部分，落位位置无法确定。清除筛选后再拖动。',
    }
  }
  return null
}

/**
 * 驳回类流转必须带原因。
 *
 * 没有理由的驳回对 Agent 毫无价值 —— 它只知道退回，不知道该补什么，
 * 下次提交大概率还是被打回。与其让 Agent 瞎猜，不如服务端强制。
 */
export function requiresComment(from: TaskStatus, to: TaskStatus): boolean {
  return from === 'review' && to === 'todo'
}

/**
 * 未满足的前置条件。
 *
 * 与"流转非法"必须分开处理：
 * - 非法（`canDropInto` 为 false）意味着这个方向根本不该走，直接弹回；
 * - 守卫未满足意味着方向对、只差一步补齐，应该引导用户去补，而不是弹回。
 *
 * 混为一谈的结果就是"用户永远不知道该做什么"。
 */
export interface GuardBlock {
  guard: TransitionGuard
  /** 给用户看的一句话，必须说清下一步该做什么 */
  message: string
}

/** 守卫 → 用户可读的引导文案。 */
const GUARD_MESSAGE: Record<TransitionGuard, string> = {
  requires_assignee: '派发前要先指派一个已启用的 Agent —— 否则任务进了「待执行」也没人能领',
  requires_comment: '驳回必须填写原因，Agent 需要知道该补什么',
}

/** meta.human_transition_guards 的键格式，与后端 guardKey 一致。 */
function guardKey(from: TaskStatus, to: TaskStatus): string {
  return `${from}>${to}`
}

/**
 * 检查一次落位的守卫是否全部满足。
 *
 * 数据源是 meta.human_transition_guards —— 也就是后端状态机本身，
 * 不在前端硬编码任何一条规则。meta 没到时返回 null 表示"暂无法判定"，
 * 调用方应放行并交给服务端裁决（meta 加载失败不该让看板瘫掉）。
 */
export function checkGuards(args: {
  task: Task
  to: TaskStatus
  guards: Record<string, TransitionGuard[]> | undefined
  /** 已登记的 Agent，按 id 索引；requires_assignee 要求指派人存在且已启用 */
  agents: ReadonlyMap<number, Agent>
  comment?: string
  /**
   * 032 总管接管派发后，未指派的 idea 任务允许发起拖拽：
   * auto 下后端会自动定人，observe 下后端只生成建议并留在 idea。
   * 默认 false，保证未接总管的调用方与既有单测行为不变。
   */
  allowUnassigned?: boolean
}): GuardBlock | null {
  const { task, to, guards, agents, comment, allowUnassigned = false } = args
  if (!guards) return null

  const list = guards[guardKey(task.status, to)]
  if (!list || list.length === 0) return null

  for (const g of list) {
    const satisfied =
      g === 'requires_assignee'
        ? allowUnassigned || hasClaimableAssignee(task, agents)
        : Boolean(comment?.trim())
    if (!satisfied) {
      return { guard: g, message: GUARD_MESSAGE[g] ?? '还差一步才能完成这次流转' }
    }
  }
  return null
}

/**
 * 任务是否指派给了一个能领取的 Agent（存在且已启用），与后端守卫判定一致。
 *
 * 不能只判 null/undefined：Agent ID 从 1 开始，0 是无效值。
 * 用 truthy 判断时 `assignee_agent_id: 0` 会被当成"已指派"，
 * 前端放行、服务端 409 —— 与守卫缺失时的表现完全一样，很难排查。
 * 指派给已禁用 Agent 同理：它无法领取，派发出去就是死任务，服务端会拒绝。
 */
function hasClaimableAssignee(task: Task, agents: ReadonlyMap<number, Agent>): boolean {
  const id = task.assignee_agent_id
  if (typeof id !== 'number' || id <= 0) return false
  return agents.get(id)?.enabled === true
}

/**
 * 乐观更新：把任务在本地缓存里移到目标列。
 *
 * 直接返回新的 `Record<status, Task[]>`，由调用方写回 React Query 缓存。
 * 写成一个函数是为了能测"乐观更新后被服务端拒绝要怎么回滚"。
 */
export function applyOptimisticMove(
  columns: Record<TaskStatus, Task[]>,
  plan: DropPlan,
): Record<TaskStatus, Task[]> {
  // 先从每一列摘掉。任务若已因过期的推送留在多列里，只清 plan.from 会继续重复。
  const next = {} as Record<TaskStatus, Task[]>
  let moving: Task | undefined
  for (const s of COLUMN_ORDER) {
    const list = columns[s] ?? []
    next[s] = list.filter((t) => t.id !== plan.taskId)
    moving ??= list.find((t) => t.id === plan.taskId)
  }
  if (!moving) return columns

  const target = next[plan.to] ?? []
  // ⚠️ 必须显式排除 null：DropPlan.toIndex 用 null 表示"跨列，追加到末尾"，
  // 而 `null !== undefined` 为真、`null >= 0` 经强制转换也是真，
  // 于是 Math.min(null, len) === 0，任务会被插到列首而不是末尾。
  const insertAt =
    typeof plan.toIndex === 'number' && plan.toIndex >= 0
      ? Math.min(plan.toIndex, target.length)
      : target.length

  const moved: Task = { ...moving, status: plan.to }
  next[plan.to] = [
    ...target.slice(0, insertAt),
    moved,
    ...target.slice(insertAt),
  ]
  return next
}

/**
 * 回滚：把任务放回原来的列与位置。
 *
 * 服务端拒绝 409 时调用。`originalTask` 必须是被移动前的完整对象 ——
 * 重建时带上原 version，避免回滚把别人刚做的改动覆盖掉。
 *
 * ⚠️ 必须先从**所有**列里摘掉这条任务，再插回原列。
 * 只重建原列的话，乐观更新时被移进去的目标列仍会残留一份，
 * 结果是同一条任务在看板上出现两次 —— 而服务端只有一条。
 */
export function revertMove(
  columns: Record<TaskStatus, Task[]>,
  taskId: number,
  originalTask: Task,
  originalIndex: number,
): Record<TaskStatus, Task[]> {
  const next = {} as Record<TaskStatus, Task[]>
  for (const s of COLUMN_ORDER) {
    next[s] = (columns[s] ?? []).filter((t) => t.id !== taskId)
  }

  const target = next[originalTask.status] ?? []
  const insertAt = Math.min(Math.max(originalIndex, 0), target.length)
  next[originalTask.status] = [
    ...target.slice(0, insertAt),
    originalTask,
    ...target.slice(insertAt),
  ]
  return next
}

/**
 * 筛选后判断某任务是否仍应显示在看板。
 *
 * SSE 推的是**全量变更**。前端必须自己判断"这个变更后的任务是否仍满足
 * 当前筛选"，否则被筛掉的任务会因为一次无关变更突然冒出来。
 * 这也是为什么筛选必须在服务端做 —— 服务端推的才是"筛选后的集合"。
 */
export function matchesFilter(
  task: Task,
  filter: {
    status?: TaskStatus[]
    priority?: string[]
    assignee?: number
    unassigned?: boolean
    projects?: number[]
    no_project?: boolean
    q?: string
  },
): boolean {
  if (filter.status?.length && !filter.status.includes(task.status)) return false
  if (filter.priority?.length && !filter.priority.includes(task.priority)) return false

  if (filter.unassigned && task.assignee_agent_id !== null) return false
  if (
    filter.assignee !== undefined &&
    task.assignee_agent_id !== filter.assignee
  ) {
    return false
  }

  // 项目筛选与后端 buildTaskWhere 的并集语义一致：
  // projects 命中 或 no_project（project_id 为 null）都算满足；
  // 两者都未启用时不加约束。
  const hasProjectFilter = (filter.projects?.length ?? 0) > 0 || filter.no_project
  if (hasProjectFilter) {
    const inProjects = (filter.projects?.length ?? 0) > 0 &&
      task.project_id !== null &&
      filter.projects!.includes(task.project_id)
    const isNoProject = Boolean(filter.no_project) && task.project_id === null
    if (!inProjects && !isNoProject) return false
  }

  const q = filter.q?.trim().toLowerCase()
  if (q) {
    // 与服务端 instr(lower(col), lower(?)) 保持一致：只搜标题与描述，
    // 不搜 ID / 路径，避免搜 "1" 命中一切。
    const haystack = `${task.title}\n${task.description}`.toLowerCase()
    if (!haystack.includes(q)) return false
  }
  return true
}
