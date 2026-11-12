/**
 * React Query 缓存键与乐观更新。
 *
 * 缓存键集中在这里定义 —— 键写错会导致"改了 A 视图，B 视图不刷新"这类
 * 极难排查的问题。约定：
 *
 * - 键的第一段是资源名（board/tasks/meta/agents）
 * - 第二段是筛选条件，**字段顺序固定**（对象字面量天然有序，但展开顺序不可控，
 *   所以统一由 normalizeFilter 生成）
 *
 * 特别注意：`archived` 默认 'exclude' 与不传该字段，对后端是同一含义，
 * 但键不同。所以必须 normalize，否则"清除归档筛选"会命中旧缓存。
 */

import { QueryClient } from '@tanstack/react-query'
import type { Task, TaskFilter, TaskStatus } from '@/api/types'
import { COLUMN_ORDER } from '@/lib/dragRules'
/** 归一化：去掉未设置/默认值的键，保证语义相同的筛选产生同一个键。 */
export function normalizeFilter(f: TaskFilter): Record<string, string | number> {
  const out: Record<string, string | number> = {}
  // 数组按顺序排序：status=[a,b] 与 [b,a] 语义相同，不该是两个缓存
  if (f.status?.length) out.status = [...f.status].sort().join(',')
  if (f.priority?.length) out.priority = [...f.priority].sort().join(',')
  if (f.assignee !== undefined) out.assignee = f.assignee
  if (f.unassigned) out.unassigned = 1
  if (f.projects?.length) out.projects = [...f.projects].sort((a, b) => a - b).join(',')
  if (f.no_project) out.no_project = 1
  if (f.q?.trim()) out.q = f.q.trim()
  // 默认值不写入，避免 exclude 与 undefined 产生两个键
  if (f.archived && f.archived !== 'exclude') out.archived = f.archived
  if (f.sort) out.sort = f.sort
  if (f.page !== undefined) out.page = f.page
  if (f.page_size !== undefined) out.page_size = f.page_size
  if (f.per_column !== undefined) out.per_column = f.per_column
  return out
}

export const qk = {
  meta: () => ['meta'] as const,
  agents: () => ['agents'] as const,
  dispatchSettings: () => ['dispatchSettings'] as const,
  dispatchSuggestion: (id: number) => ['dispatchSuggestion', id] as const,
  agentLedger: (id: number) => ['agentLedger', id] as const,
  handoffCandidates: (id: number) => ['handoffCandidates', id] as const,
  templates: () => ['templates'] as const,
  schedules: () => ['schedules'] as const,
  hosts: () => ['hosts'] as const,
  projects: () => ['projects'] as const,
  board: (f: TaskFilter) => ['board', normalizeFilter(f)] as const,
  tasks: (f: TaskFilter) => ['tasks', normalizeFilter(f)] as const,
  task: (id: number) => ['task', id] as const,
  taskEvents: (id: number, page: number, size: number) =>
    ['taskEvents', id, page, size] as const,
}

/**
 * 失效策略：任何任务变更都要让所有含筛选的 board/tasks 缓存失效。
 *
 * 粗粒度失效换来的是正确性。精细失效（比如只失效被改的那一条）在筛选
 * 场景下几乎必然出错 —— 任务状态变了之后，它是否还满足当前筛选是
 * 服务端才知道的事。
 */
export function invalidateTaskCaches(qc: QueryClient): void {
  void qc.invalidateQueries({ queryKey: ['board'] })
  void qc.invalidateQueries({ queryKey: ['tasks'] })
  void qc.invalidateQueries({ queryKey: ['task'] })
  void qc.invalidateQueries({ queryKey: ['taskEvents'] })
  void qc.invalidateQueries({ queryKey: ['agents'] })
}

/** 把 Board.columns 补成每个状态都有键的字典，缺列补空数组。 */
export function columnsByStatus(
  columns: Partial<Record<TaskStatus, Task[]>> | undefined,
): Record<TaskStatus, Task[]> {
  const out = {} as Record<TaskStatus, Task[]>
  for (const s of COLUMN_ORDER) out[s] = columns?.[s] ?? []
  return out
}

/** 全局 QueryClient 默认配置。 */
export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 看板数据由 SSE 推送驱动，不需要窗口聚焦时重拉 ——
        // 否则用户切回标签页会看到一次不必要的闪烁。
        refetchOnWindowFocus: false,
        staleTime: 30_000,
        retry: (failureCount, error) => {
          // 4xx 是确定性错误，重试没有意义，只会把错误提示推迟 3 次重试的时间
          const status = (error as { status?: number })?.status ?? 0
          if (status >= 400 && status < 500) return false
          return failureCount < 2
        },
      },
      mutations: { retry: false },
    },
  })
}
