/**
 * 把 SSE 变更桥接到 React Query 缓存。
 *
 * 两种更新策略，按成本分档：
 *
 * 1. **轻量变更**（Agent 在线状态、单条任务更新）：直接改缓存里的对象，
 *    不重新请求。这是 99% 的情况 —— Agent 心跳、任务编辑、状态流转。
 *
 * 2. **重量变更**（收到 resync、变更涉及归档、或拿不准）：整体失效重拉。
 *
 * 为什么不"全都重拉"：Agent 心跳不广播任务变更，但多个 Agent 并行工作时
 * 每秒可能有几条变更，每次都重拉看板会让界面持续闪烁。
 *
 * 为什么不"全都局部改"：筛选状态下的看板无法在本地判断一条变更后
 * 该不该出现在列表里 —— 归档、被筛掉的任务、计数都会错。
 * `matchesFilter` 能覆盖大部分情况，但计数与 truncated 无法可靠推导，
 * 所以在这两类场景选择保守重拉。
 */

import { useEffect } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { stream, type ConnectionState } from '@/lib/stream'
import { matchesFilter } from '@/lib/dragRules'
import type { Change, Task } from '@/api/types'
import { invalidateTaskCaches } from '@/store/query'
import { useFilters, isFiltered } from '@/store/filters'
import { useState } from 'react'

export interface StreamStatus {
  state: ConnectionState
  detail?: string
}

export function useStreamBridge(): StreamStatus {
  const qc = useQueryClient()
  const filter = useFilters()
  const [status, setStatus] = useState<StreamStatus>({ state: 'connecting' })

  useEffect(() => {
    const release = stream.subscribe({
      onStateChange: (state, detail) => setStatus({ state, detail }),

      onChange: (change: Change) => {
        // resync：缺口已丢，只能整体重拉
        if (change.kind === 'agent') {
          // Agent 在线/启用状态变化：只刷 agents 缓存
          void qc.invalidateQueries({ queryKey: ['agents'] })
          return
        }

        // 项目增删改：只刷项目列表（FilterBar chips 与表单下拉用）。
        // ⚠️ 必须显式分支：下面 task==null 的兜底会全量失效任务缓存，
        // 每次改名都把看板重拉一遍，多端编辑项目时界面会持续闪烁。
        if (change.kind === 'project') {
          void qc.invalidateQueries({ queryKey: ['projects'] })
          return
        }

        // 列内顺序变更：按服务端给的 ID 序列重排本地缓存，不重拉。
        //
        // 重排是高频操作，若每次都重拉看板，多端同时操作时界面会持续闪烁。
        // 一帧里带整列顺序正是为了这个 —— 客户端有足够信息自己排好。
        if (change.kind === 'column') {
          if (change.status && change.column_ids) {
            reorderColumnInCaches(
              qc.getQueriesData({ queryKey: ['board'] }),
              change.status,
              change.column_ids,
              qc,
            )
          }
          return
        }

        const task = change.task
        if (!task) {
          invalidateTaskCaches(qc)
          return
        }

        // 归档变更会改变 counts 与 truncated，本地推导不可靠
        if (task.archived_at !== null) {
          invalidateTaskCaches(qc)
          return
        }

        // 拿不准时保守重拉：涉及筛选条件变化、且当前正处于筛选视图
        if (isFiltered(filter) && !matchesFilter(task, filter)) {
          // 任务已不满足筛选 —— 它应该从看板消失，重拉最稳
          invalidateTaskCaches(qc)
          return
        }

        // 新建的任务不在任何列表里，需要重拉才能看到。
        //
        // ⚠️ 必须在这里 return，不能先走上面的 patchTaskEverywhere。
        // 先 patch 会把新任务手工塞进缓存，紧接着 refetch 拿回同样内容，
        // structural sharing 判定"没变化"而复用旧引用 —— 结果是
        // 缓存里有数据、界面却不动（详见 patchTaskEverywhere 的注释）。
        if (change.event?.type === 'created') {
          invalidateTaskCaches(qc)
          return
        }

        // 轻量路径：就地更新各缓存里的这条任务
        patchTaskEverywhere(qc.getQueriesData({ queryKey: ['board'] }), task, qc)
        patchTaskEverywhere(qc.getQueriesData({ queryKey: ['tasks'] }), task, qc)
        qc.setQueryData(['task', task.id], task)

        // 时间线有新事件
        void qc.invalidateQueries({ queryKey: ['taskEvents', task.id] })
      },
    })

    return release
  }, [qc, filter])

  return status
}

/**
 * 在若干缓存结构里替换一条任务，返回新的顶层对象。
 *
 * ⚠️ 必须返回**全新的顶层对象**，不能原地修改。
 *
 * 早先的实现是 `cols[status] = next` 直接改缓存内部结构，
 * 注释还写着"不修改原对象"—— 说的和做的相反。后果不是"少更新一次"，
 * 而是一条极难定位的链：
 *
 *   1. SSE 帧到达，本函数把新任务塞进缓存，但顶层引用没变 → 不重渲染；
 *   2. created 事件随后 invalidate + refetch，服务端返回**含该任务**的数据；
 *   3. React Query v5 的 structural sharing 发现"旧数据"与"新数据"此刻
 *      **结构相同**（旧数据已被步骤 1 手工塞过），于是复用旧引用、
 *      判定"数据没变"，不通知订阅者。
 *
 * 净效果：接口返回正确、缓存里也确实有这条任务，界面上却永远看不到；
 * 而手动刷新一下就好了 —— 全新加载不存在步骤 1 的污染。
 * 这个 bug 只在"新建任务 + 不刷新"这条路径上出现，很容易被误判成 SSE 没推。
 */
function patchTaskEverywhere(
  queries: ReadonlyArray<[readonly unknown[], unknown]>,
  task: Task,
  qc: QueryClient,
): void {
  for (const [key, data] of queries) {
    if (!data || typeof data !== 'object') continue
    const next = patchShape(data, task)
    if (next) qc.setQueryData(key, next)
  }
}

/** 返回替换掉这条任务后的**新**缓存对象；结构不匹配或无需改动时 null。 */
export function patchShape(data: unknown, task: Task): unknown | null {
  // null 也满足 typeof === 'object'，不挡掉的话下面读 .columns 会抛。
  // 缓存里出现 null 不奇怪（请求失败、还没加载完），这里必须能安全退化。
  if (data === null || typeof data !== 'object') return null
  const obj = data as Record<string, unknown>

  // Board: { columns: { todo: [Task], ... }, counts, truncated }
  if (obj.columns && typeof obj.columns === 'object' && !Array.isArray(obj.columns)) {
    const cols = obj.columns as Record<string, Task[]>
    const nextCols: Record<string, Task[]> = { ...cols }
    const removedFrom: string[] = []
    let added = false
    let touched = false

    for (const [status, list] of Object.entries(cols)) {
      if (!Array.isArray(list)) continue
      const idx = list.findIndex((t) => t.id === task.id)
      if (status === task.status) {
        if (idx >= 0) {
          const next = [...list]
          next[idx] = task
          nextCols[status] = next
        } else {
          nextCols[status] = [...list, task]
          added = true
        }
        touched = true
      } else if (idx >= 0) {
        // 状态已经变了，旧列里的那一份必须摘掉。
        // 只在原位替换会让同一条任务沿着它经过的每一列各留一张。
        nextCols[status] = list.filter((t) => t.id !== task.id)
        removedFrom.push(status)
        touched = true
      }
    }
    if (!touched) return null

    let counts: unknown = obj.counts
    if (counts && typeof counts === 'object' && !Array.isArray(counts)) {
      const nextCounts = { ...(counts as Record<string, number>) }
      let countChanged = false
      for (const status of removedFrom) {
        const n = nextCounts[status]
        if (typeof n === 'number' && n > 0) {
          nextCounts[status] = n - 1
          countChanged = true
        }
      }
      if (added) {
        const n = nextCounts[task.status]
        nextCounts[task.status] = (typeof n === 'number' ? n : 0) + 1
        countChanged = true
      }
      if (countChanged) counts = nextCounts
    }
    // 顶层也要新引用，否则 Board 拿到的还是同一个 data，React 不会重渲染
    return { ...obj, columns: nextCols, counts }
  }

  // TaskList / EventList: { items: [Task] }
  if (Array.isArray(obj.items)) {
    const items = obj.items as Task[]
    const idx = items.findIndex((t) => t.id === task.id)
    if (idx >= 0) {
      const next = [...items]
      next[idx] = task
      return { ...obj, items: next }
    }
  }
  return null
}

/**
 * 按服务端给的 ID 序列重排各看板缓存里的某一列。
 *
 * 为什么可以在本地完成重排：这一列的所有任务都已经在缓存里
 * （SSE 只在顺序变化时发这一帧，任务本身的增删由 task 帧负责）。
 * 我们只调整它们的排列，不新增也不删除任何条目。
 *
 * ⚠️ 必须处理"缓存里有、但服务端序列里没有"的任务
 * （比如它刚被别的客户端归档、或超出了 per_column 截断）。
 * 简单地按序列 map 会把这类任务整个丢掉 —— 界面上任务凭空消失，
 * 直到下一次重拉才回来。把它们接在序列之后是安全的降级：
 * 顺序可能不完美，但一条都不丢。
 */
function reorderColumnInCaches(
  queries: ReadonlyArray<[readonly unknown[], unknown]>,
  status: string,
  orderedIds: number[],
  qc: QueryClient,
): void {
  for (const [key, data] of queries) {
    if (!data || typeof data !== 'object') continue
    const obj = data as Record<string, unknown>
    const cols = obj.columns
    if (!cols || typeof cols !== 'object' || Array.isArray(cols)) continue
    const colMap = cols as Record<string, Task[]>
    const list = colMap[status]
    if (!Array.isArray(list)) continue

    const byId = new Map(list.map((t) => [t.id, t]))
    const next: Task[] = []
    for (const id of orderedIds) {
      const t = byId.get(id)
      if (t) {
        next.push(t)
        byId.delete(id)
      }
    }
    // 未在序列中的任务保持相对顺序接在后面，而不是被丢掉
    for (const t of list) {
      if (byId.has(t.id)) next.push(t)
    }
    if (next.length !== list.length) continue // 集合对不上，放弃本地重排
    // 同patchShape：必须给顶层新引用，否则重排了也不会重渲染
    qc.setQueryData(key, { ...obj, columns: { ...colMap, [status]: next } })
  }
}
