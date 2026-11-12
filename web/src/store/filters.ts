/**
 * 筛选条件 store —— 单一真相源，URL 同步。
 *
 * 为什么 URL 要同步：看板是这个工具的主要使用场景，用户会收藏
 * "我负责的 urgent 任务"这个视图，也会想刷新后保留筛选。原 Vue 前端的
 * 筛选存在组件内，刷新即丢、无法分享 —— 这是可用性缺陷而非细节。
 *
 * 用 zustand 而不是 Context：筛选条件高频变化（每次输入搜索词），
 * 走 Context 会让所有消费者重渲染。
 */

import { create } from 'zustand'
import type { ArchivedMode, Priority, SortField, TaskFilter, TaskStatus } from '@/api/types'

/** 空筛选：default 对象每次新建，避免共享引用被误改。 */
const emptyFilter = (): TaskFilter => ({
  status: [],
  priority: [],
  projects: [],
  no_project: false,
  q: '',
  archived: 'exclude',
})

/**
 * 当前筛选是否生效。
 *
 * 抽成独立纯函数而不是只留store 方法，是因为有两处需要它：
 * App 渲染时判断"要不要允许重排"，SSE 桥接时判断"能否本地推导变更"。
 * 两处若各写一份，条件迟早会不一致 —— 而"重排被允许但落位算错"
 * 这类问题只在特定组合下出现，很难在联调时发现。
 *
 * 注意 `archived: 'exclude'` 是默认值而非筛选条件。
 */
export function isFiltered(f: Partial<TaskFilter>): boolean {
  return Boolean(
    f.status?.length ||
      f.priority?.length ||
      f.projects?.length ||
      f.no_project ||
      f.q?.trim() ||
      f.assignee !== undefined ||
      f.unassigned ||
      (f.archived && f.archived !== 'exclude'),
  )
}

interface FilterState extends TaskFilter {
  /** 搜索词输入框的即时值，与实际生效的 q 分离（防抖用） */
  searchInput: string
  setSearchInput: (v: string) => void
  /** 防抖后真正生效 */
  commitSearch: () => void
  toggleStatus: (s: TaskStatus) => void
  togglePriority: (p: Priority) => void
  toggleProject: (id: number) => void
  toggleNoProject: () => void
  setAssignee: (id: number | undefined) => void
  toggleUnassigned: () => void
  setArchived: (m: ArchivedMode) => void
  setSort: (s: SortField) => void
  clearAll: () => void
  /** 是否处于"已筛选"状态，用于决定是否显示清除按钮 */
  isFiltered: () => boolean
}

function toQuery(state: Partial<TaskFilter>): string {
  const p = new URLSearchParams()
  if (state.status?.length) p.set('status', state.status.join(','))
  if (state.priority?.length) p.set('priority', state.priority.join(','))
  if (state.assignee !== undefined) p.set('assignee', String(state.assignee))
  if (state.unassigned) p.set('unassigned', '1')
  if (state.projects?.length) p.set('projects', state.projects.join(','))
  if (state.no_project) p.set('no_project', '1')
  if (state.q?.trim()) p.set('q', state.q.trim())
  if (state.archived && state.archived !== 'exclude') p.set('archived', state.archived)
  if (state.sort) p.set('sort', state.sort)
  return p.toString()
}

/** 供测试直接验证 URL 解析规则。 */
export function fromSearch(search: string): Partial<TaskFilter> {
  const p = new URLSearchParams(search)
  const out: Partial<TaskFilter> = {}
  const status = p.get('status')
  if (status) out.status = status.split(',').filter(Boolean) as TaskStatus[]
  const priority = p.get('priority')
  if (priority) out.priority = priority.split(',').filter(Boolean) as Priority[]
  const assignee = p.get('assignee')
  if (assignee) {
    const n = Number(assignee)
    if (Number.isFinite(n)) out.assignee = n
  }
  if (p.get('unassigned') === '1') out.unassigned = true
  const projects = p.get('projects')
  if (projects) {
    const ids = projects
      .split(',')
      .map((s) => Number(s))
      .filter((n) => Number.isFinite(n) && n > 0)
    if (ids.length) out.projects = ids
  }
  if (p.get('no_project') === '1') out.no_project = true
  const q = p.get('q')
  if (q) out.q = q
  const archived = p.get('archived')
  if (archived === 'only' || archived === 'any') out.archived = archived
  const sort = p.get('sort')
  if (sort) out.sort = sort as SortField
  return out
}

/** 把当前筛选写回地址栏（replaceState，避免每个按键都塞一条历史）。 */
function syncUrl(state: TaskFilter): void {
  const qs = toQuery(state)
  const url = qs ? `${location.pathname}?${qs}` : location.pathname
  window.history.replaceState(null, '', url)
}

const initial = {
  ...emptyFilter(),
  ...fromSearch(typeof location !== 'undefined' ? location.search : ''),
}

export const useFilters = create<FilterState>((set, get) => ({
  ...initial,
  searchInput: initial.q ?? '',

  setSearchInput: (v) => set({ searchInput: v }),

  commitSearch: () => {
    const v = get().searchInput.trim()
    set({ q: v })
    syncUrl(get())
  },

  toggleStatus: (s) => {
    const cur = get().status ?? []
    const next = cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]
    set({ status: next })
    syncUrl(get())
  },

  togglePriority: (p) => {
    const cur = get().priority ?? []
    const next = cur.includes(p) ? cur.filter((x) => x !== p) : [...cur, p]
    set({ priority: next })
    syncUrl(get())
  },

  toggleProject: (id) => {
    const cur = get().projects ?? []
    const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]
    set({ projects: next })
    syncUrl(get())
  },

  toggleNoProject: () => {
    set({ no_project: !get().no_project })
    syncUrl(get())
  },

  setAssignee: (id) => {
    // 选了具体 Agent 就不该同时"无人认领" —— 两个条件互斥，
    // 否则请求会返回空列表，用户以为没有任务。
    set({ assignee: id, unassigned: id === undefined ? get().unassigned : false })
    syncUrl(get())
  },

  toggleUnassigned: () => {
    const next = !get().unassigned
    set({ unassigned: next, assignee: next ? undefined : get().assignee })
    syncUrl(get())
  },

  setArchived: (m) => {
    set({ archived: m })
    syncUrl(get())
  },

  setSort: (s) => {
    set({ sort: s })
    syncUrl(get())
  },

  clearAll: () => {
    const base = emptyFilter()
    set({ ...base, searchInput: '' })
    syncUrl(get())
  },

  isFiltered: () => isFiltered(get()),
}))
