/**
 * 看板。
 *
 * 拖拽实现要点（都是踩过的坑）：
 *
 * 1. **`distance: 4`** —— 不设的话点击卡片会被判定成拖拽而吞掉点击，
 *    详情打不开；设成 4px 则"轻微手抖"仍算点击。dnd-kit 没有 delay 选项，
 *    原前端 vuedraggable 的 `delay: 180` 让点击有 180ms 延迟，手感明显发涩。
 *
 * 2. **服务端权威** —— 落位后调 `transitionTask`，不用前端算流转。
 *    `planDrop` 返回 null 时直接弹回，不发请求（省一轮往返 + 避免闪烁）。
 *
 * 3. **乐观更新 + 精确回滚** —— 服务端 409 时用 `revertMove` 把任务放回
 *    原列原位置，并提示原因。回滚必须从所有列里摘掉这条任务，
 *    否则同一条任务会在看板上出现两次。
 *
 * 4. **approved 不可拖入** —— 它是终态，只能由 review 前进而来。
 *
 * 5. **不横向滚动** —— 放不下全部列时进入窗口模式（见 lib/boardLayout.ts）：
 *    只显示连续 N 列，顶部页签作总览。窗口外的列仍挂载、只隐藏，
 *    因为被拖的卡片所在列可能在拖拽中途被平移出窗口。
 */

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  closestCorners,
  pointerWithin,
  type CollisionDetection,
  type DragStartEvent,
  type DragEndEvent,
  type DragOverEvent,
} from '@dnd-kit/core'

import { restrictToWindowEdges } from '@dnd-kit/modifiers'
import {
  COLUMN_ORDER,
  checkGuards,
  checkReorderable,
  isTaskStatus,
  planDrop,
  type GuardBlock,
  type ReorderBlock,
} from '@/lib/dragRules'
import {
  computeLayout,
  expandedSequence,
  shiftWindow,
  visibleWindow,
} from '@/lib/boardLayout'
import { columnsByStatus } from '@/store/query'
import type { Agent, Board as BoardData, Project, Task, TaskStatus, TransitionGuard } from '@/api/types'
import { TaskCard } from './TaskCard'
import { Column } from './Column'
import { ColumnTabs } from './ColumnTabs'
import type { DropPlan } from '@/lib/dragRules'
import { ApiError } from '@/api/client'
import { DispatchSuggestionSection } from '@/components/dispatch/DispatchSuggestionCard'

/**
 * 碰撞检测：页签按"指针是否在页签内"判定，其余落点用 closestCorners。
 *
 * 只用 closestCorners 时页签几乎永远命中不了：它比较的是拖拽浮层（288px 宽）
 * 与各落点的四角距离，小小的页签输给旁边整列，指针明明停在页签上也不算悬停。
 * 键盘拖拽没有指针坐标，pointerWithin 返回空，自然退回 closestCorners。
 */
const collisionDetection: CollisionDetection = (args) => {
  const isTab = (id: string | number) => String(id).startsWith('tab:')
  const onTab = pointerWithin({
    ...args,
    droppableContainers: args.droppableContainers.filter((c) => isTab(c.id)),
  })
  if (onTab.length > 0) return onTab
  return closestCorners({
    ...args,
    droppableContainers: args.droppableContainers.filter((c) => !isTab(c.id)),
  })
}

/** 窗口模式起点（第一列的状态名）持久化的 localStorage 键。 */
const WINDOW_START_KEY = 'ingot.windowStart'

function loadWindowStart(): TaskStatus | undefined {
  try {
    const v = localStorage.getItem(WINDOW_START_KEY)
    return v && isTaskStatus(v) ? v : undefined
  } catch {
    return undefined
  }
}

/**
 * 看板列区域的内容宽度（不含内边距），随容器尺寸变化更新。
 * 首次在绘制前同步量一次，避免先按错误布局渲染再跳变。
 */
function useContentWidth() {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const style = getComputedStyle(el)
    setWidth(el.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight))
    const ro = new ResizeObserver(([entry]) => {
      if (entry) setWidth(entry.contentRect.width)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, width] as const
}

export interface BoardProps {
  data: BoardData
  agents: Map<number, Agent>
  /** 项目 id -> 项目；卡片渲染色点用 */
  projects: Map<number, Project>
  transitions: Record<string, TaskStatus[]>
  /** 前置条件，来自 meta.human_transition_guards；meta 未就绪时为 undefined */
  guards: Record<string, TransitionGuard[]> | undefined
  /** 当前排序，用于判断重排是否有意义 */
  sort: string | undefined
  /** 当前是否有筛选条件生效 */
  filtered: boolean
  collapsed: Record<string, boolean>
  onToggleCollapse: (status: TaskStatus) => void
  onOpenTask: (id: number) => void
  /** 落位回调；返回 Promise，失败时组件负责回滚 */
  onTransition: (plan: DropPlan, original: Task, originalIndex: number) => Promise<void>
  /** 守卫未满足时的引导（如需先指派 Agent），由App 层决定怎么提示 */
  onGuardBlocked?: (block: GuardBlock, task: Task) => void
  /** 重排会被静默丢弃时的提示（非手动排序 / 筛选中） */
  onReorderBlocked?: (block: ReorderBlock) => void
}

export function Board({
  data,
  agents,
  projects,
  transitions,
  guards,
  sort,
  filtered,
  collapsed,
  onToggleCollapse,
  onOpenTask,
  onTransition,
  onGuardBlocked,
  onReorderBlocked,
}: BoardProps) {
  const [activeTask, setActiveTask] = useState<Task | null>(null)
  const [pending, setPending] = useState<Set<number>>(new Set())
  // observe 下未指派任务被 409 挡下后，弹出总管建议卡。
  const [suggestionTaskId, setSuggestionTaskId] = useState<number | null>(null)

  const sensors = useSensors(
    // distance 而非 delay：见文件头注释 1
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    // 键盘拖拽是无障碍要求（WCAG 2.1.1）：空格拾起、方向键移动、空格放下
    //
    // ⚠️ 必须重配 keyboardCodes。dnd-kit 默认 start: ['Space', 'Enter']，
    // 于是 Enter 也会拾起拖拽 —— 但 Enter 在本项目里是"打开详情"，
    // 结果是：按下 Enter 时dnd-kit 先启动拖拽并 preventDefault，
    // TaskCard 里的守卫 `if (e.defaultPrevented) return` 直接返回，
    // **详情抽屉永远打不开**。实测表现为键盘用户既拖不动也打不开。
    //
    // 所以：拾起只用 Space；Enter 仅在已拾起时用于放下，
    // 不作为拾起键。Escape 取消是 dnd-kit 自带的，保留。
    useSensor(KeyboardSensor, {
      keyboardCodes: {
        start: ['Space'],
        cancel: ['Escape'],
        end: ['Space', 'Enter'],
      },
    }),
  )

  // 拖拽开始前的快照，回滚时要用（缓存里的对象已经被乐观更新改过了）
  const snapshot = useMemo(() => new Map<number, { task: Task; index: number }>(), [])

  const columns = useMemo(() => columnsByStatus(data.columns), [data.columns])

  // 布局：放得下全部列就全部显示，否则显示未折叠列中连续的 N 列
  const [rowRef, width] = useContentWidth()
  const [windowStart, setWindowStart] = useState(loadWindowStart)
  const layout = computeLayout(width, collapsed)
  const sequence = expandedSequence(collapsed)
  const visible = new Set<TaskStatus>(
    layout.mode === 'all' ? COLUMN_ORDER : visibleWindow(sequence, windowStart, layout.capacity),
  )

  const persistStart = useCallback((s: TaskStatus | undefined) => {
    setWindowStart(s)
    try {
      if (s) localStorage.setItem(WINDOW_START_KEY, s)
    } catch {
      /* 隐私模式下写不进去，不影响功能 */
    }
  }, [])

  /** 点击或拖拽悬停页签：把该列纳入窗口；已折叠的先展开。 */
  const selectColumn = useCallback(
    (status: TaskStatus) => {
      const next = collapsed[status] ? { ...collapsed, [status]: false } : collapsed
      if (next !== collapsed) onToggleCollapse(status)
      const l = computeLayout(width, next)
      if (l.mode === 'all') return
      persistStart(shiftWindow(expandedSequence(next), windowStart, l.capacity, status))
    },
    [collapsed, onToggleCollapse, width, windowStart, persistStart],
  )

  const counts = useMemo(() => {
    const out = {} as Record<TaskStatus, number>
    for (const s of COLUMN_ORDER) out[s] = data.counts?.[s] ?? columns[s]?.length ?? 0
    return out
  }, [data.counts, columns])

  function onDragStart(e: DragStartEvent) {
    const id = Number(e.active.id)
    const col = COLUMN_ORDER.find((s) => columns[s].some((t) => t.id === id))
    const task = col ? columns[col].find((t) => t.id === id) : undefined
    if (!task || !col) return
    snapshot.set(id, { task, index: columns[col].findIndex((t) => t.id === id) })
    setActiveTask(task)
  }

  function onDragOver(_e: DragOverEvent) {
    // 跨列悬停时的实时预览由 DragOverlay + 列高亮承担，
    // 不在这里改缓存 —— 悬停阶段就改数据会让撤销逻辑变复杂。
  }

  async function onDragEnd(e: DragEndEvent) {
    setActiveTask(null)
    const id = Number(e.active.id)
    const snap = snapshot.get(id)
    if (!snap) return

    const overCol = resolveDropColumn(e, columns)
    if (!overCol) return

    const sameColumn = overCol === snap.task.status

    // 同列重排的预检：非手动排序或筛选中时，顺序改动不会（或不正确地）
    // 体现。提前拒绝并说明原因，好过"拖成功了但刷新就没了"。
    if (sameColumn) {
      const blocked = checkReorderable({ sort, filtered })
      if (blocked) {
        onReorderBlocked?.(blocked)
        return
      }
    }

    // 守卫预检：方向合法但还差前置条件时，不发注定被拒的请求。
    // 直接引导用户补齐（指派 Agent / 填原因），而不是让卡片弹回再报错。
    if (!sameColumn) {
      const blocked = checkGuards({
        task: snap.task, to: overCol, guards, agents,
        // 032 总管接管派发：未指派交由后端决定（auto 定人 / observe 出建议）。
        allowUnassigned: true,
      })
      if (blocked) {
        onGuardBlocked?.(blocked, snap.task)
        return
      }
    }

    // 跨列时也算落位下标：拖到列中间与拖到末尾是不同意图，
    // 一律当成"追加到末尾"等于替用户改了操作。
    // resolveIndex 对"落在空列/列头"返回列长，正好对应追加末尾。
    const toIndex = resolveIndex(e, columns, overCol)

    const plan = planDrop({
      task: snap.task,
      to: overCol,
      transitions,
      toIndex,
      orderedIds: columns[snap.task.status].map((t) => t.id),
    })

    // 非法流转或位置没变：弹回，不请求服务端
    if (!plan) return

    setPending((s) => new Set(s).add(id))
    try {
      await onTransition(plan, snap.task, snap.index)
    } catch (err) {
      // observe 下未指派任务被 409 挡下时后端已生成建议，弹卡而不是报错。
      if (!(err instanceof ApiError && err.code === 'TASK_NOT_ASSIGNED' && overCol === 'todo')) {
        throw err
      }
      setSuggestionTaskId(id)
    } finally {
      setPending((s) => {
        const next = new Set(s)
        next.delete(id)
        return next
      })
    }
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={collisionDetection}
      // 防止拖出窗口后卡片丢失：拖到边界外时浮层贴边而不是跑出可视区。
      // 注意它对键盘拖拽同样生效（dnd-kit 的 modifier 作用于所有 sensor），
      // 实测不会影响方向键移动 —— 键盘步长 25px，远小于视口。
      modifiers={[restrictToWindowEdges]}
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDragEnd={onDragEnd}
      onDragCancel={() => setActiveTask(null)}
    >
      <div className="flex h-full flex-col">
      {layout.mode === 'window' ? (
        <ColumnTabs
          counts={counts}
          visible={visible}
          collapsed={collapsed}
          dragging={activeTask !== null}
          onSelect={selectColumn}
        />
      ) : null}
      <div
        ref={rowRef}
        className="flex min-h-0 flex-1 gap-3 p-3"
        role="list"
        aria-label="任务看板"
      >
        {COLUMN_ORDER.map((status) => {
          const tasks = columns[status] ?? []
          const isCollapsed = collapsed[status] ?? false
          return (
            <Column
              key={status}
              status={status}
              tasks={tasks}
              count={counts[status]}
              shownCount={tasks.length}
              truncated={data.truncated?.[status] === true}
              agents={agents}
              projects={projects}
              collapsed={isCollapsed}
              pending={pending}
              onToggleCollapse={() => onToggleCollapse(status)}
              onOpenTask={onOpenTask}
              // 终态列（approved）里的卡片不可拖出 —— 它没有任何合法去向。
              // 注意这与"能否拖入"无关：review → approved 是合法的，
              // 所以 approved 列仍要能作为落点。
              canDrag={(transitions[status]?.length ?? 0) > 0}
              hidden={!visible.has(status)}
            />
          )
        })}
      </div>
      </div>

      {suggestionTaskId !== null ? (
        <DispatchSuggestionSection
          taskId={suggestionTaskId}
          onClose={() => setSuggestionTaskId(null)}
        />
      ) : null}

      {/* 拖拽中的浮层：跟随指针，不受列滚动容器裁切 */}
      <DragOverlay dropAnimation={null}>
        {activeTask ? (
          <div className="w-72">
            <TaskCard
              task={activeTask}
              agents={agents}
              projects={projects}
              onOpen={() => {}}
              canDrag
              droppable={false}
            />
          </div>
        ) : null}
      </DragOverlay>
    </DndContext>
  )
}

/**
 * 整列落点的 id 前缀：列容器 `col:<status>` 与窗口模式页签 `tab:<status>`。
 * 两者语义相同 —— 落在上面即追加到该列末尾。
 */
function columnDropTarget(overId: string): TaskStatus | null {
  if (!overId.startsWith('col:') && !overId.startsWith('tab:')) return null
  const s = overId.slice(4)
  return isTaskStatus(s) ? s : null
}

/** 从落点元素解析出目标列：卡片 id → 其所在列；空列、列头或页签 → 对应列。 */
function resolveDropColumn(
  e: DragEndEvent,
  columns: Record<TaskStatus, Task[]>,
): TaskStatus | null {
  const over = e.over
  if (!over) return null
  const overId = String(over.id)

  // 落点是某张卡片 → 它所在的列
  for (const s of COLUMN_ORDER) {
    if (columns[s]?.some((t) => String(t.id) === overId)) return s
  }
  return columnDropTarget(overId)
}

/**
 * 落位下标（0 基）。
 *
 * 落在某张卡片上 → 插到那张卡片之前，即取它的下标。
 * 落在空列或列头上 → 追加到末尾，返回列长。
 *
 * 返回 undefined 只在"根本不知道落在哪"时发生（over 为空），
 * 此时调用方应放弃本次操作。
 */
function resolveIndex(
  e: DragEndEvent,
  columns: Record<TaskStatus, Task[]>,
  status: TaskStatus,
): number | undefined {
  const over = e.over
  if (!over) return undefined
  const overId = String(over.id)

  // 先按前缀判断，再转数字。
  //
  // 顺序反过来会有坑：`Number('col:todo')` 是 NaN 能被 isFinite 挡住，
  // 但若将来某个 droppable 的 id 不是 `col:` 前缀、恰好能被转成数字
  // （比如纯数字字符串），findIndex 会拿着错误的 id 去比对、静默返回 -1，
  // 落位下标变undefined。重排就此失效，且没有任何报错。
  if (columnDropTarget(overId)) {
    // 落在列容器（空列或列头）或页签上：追加到该列末尾
    return (columns[status] ?? []).length
  }

  const list = columns[status] ?? []
  const idx = list.findIndex((t) => String(t.id) === overId)
  if (idx < 0) return undefined
  return idx
}
