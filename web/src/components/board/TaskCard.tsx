/**
 * 任务卡片。
 *
 * 承载"能不能拖"与"能不能点"的区分：
 * - 用 `<article>` + 内部按钮，而不是整个 div onClick，键盘可聚焦。
 * - dnd-kit 的 `distance: 4`（见 Board.tsx）让 4px 内的位移仍算点击 ——
 *   原前端 vuedraggable 用 `delay: 180`，快速点卡片会有明显延迟感。
 *
 * 租约可视化：doing 状态才显示倒计时，且分三档（充裕/即将到期/已超时）。
 * "已超时"用危险色而不是继续倒计时 —— 那段时间租约实际已失效，
 * 继续倒数只会让人以为 Agent 还在干活。
 */

import { memo } from 'react'
import { useDraggable, useDroppable } from '@dnd-kit/core'
import { Clock, Archive, CircleUser } from 'lucide-react'
import type { Agent, Project, Task } from '@/api/types'
import { Badge } from '@/components/ui/primitives'
import { cn } from '@/lib/cn'
import { formatRelative, hasActiveLease, leaseInfo } from '@/lib/time'

const CATEGORY_LABEL: Record<Task['category'], string> = {
  '': '',
  code_tool: '做工具',
  code_game: '做游戏',
  copy: '文案',
  local_ops: '本机操作',
}

const TIER_LABEL: Record<Task['tier'], string> = {
  '': '',
  small: '小模型',
  floor: 'Flash 底线',
  large: '大模型',
}

const PRIORITY_LABEL: Record<Task['priority'], string> = {
  urgent: '紧急',
  high: '高',
  normal: '中',
  low: '低',
}

const PRIORITY_CLASS: Record<Task['priority'], string> = {
  urgent: 'bg-[var(--danger-soft)] text-[var(--danger)]',
  high: 'bg-[var(--warning-soft)] text-[var(--warning)]',
  normal: 'bg-[var(--surface-hover)] text-[var(--ink-muted)]',
  low: 'bg-[var(--surface-hover)] text-[var(--ink-subtle)]',
}

export interface TaskCardProps {
  task: Task
  agents: Map<number, Agent>
  /** 项目 id -> 项目，卡片用它渲染色点；缺失时不显示 */
  projects: Map<number, Project>
  onOpen: (id: number) => void
  /** 该列是否允许接收拖入；不允许时卡片显示为不可拖动 */
  canDrag: boolean
  /**
   * 是否作为放置区：落在这张卡片上即插到它之前（见 Board.resolveIndex）。
   * 拖拽浮层里的副本必须为 false —— 它跟着指针走，否则落点永远是被拖的卡片自己；
   * 窗口外隐藏的列也为 false —— 隐藏元素尺寸为 0，会被碰撞检测误判为最近落点。
   */
  droppable: boolean
}

export const TaskCard = memo(function TaskCard({
  task,
  agents,
  projects,
  onOpen,
  canDrag,
  droppable,
}: TaskCardProps) {
  const { attributes, listeners, setNodeRef: setDragRef, isDragging } = useDraggable({
    id: task.id,
    data: { task },
    disabled: !canDrag,
  })
  const { setNodeRef: setDropRef } = useDroppable({ id: task.id, disabled: !droppable })
  const setNodeRef = (el: HTMLElement | null) => {
    setDragRef(el)
    setDropRef(el)
  }

  const agent = task.assignee_agent_id ? agents.get(task.assignee_agent_id) : undefined
  const project = task.project_id ? projects.get(task.project_id) : undefined
  const lease = hasActiveLease(task) ? leaseInfo(task.lease_until) : null

  // 「不能拖」和「不能用」是两回事，必须把这个区分讲清楚。
  //
  // useDraggable 在 disabled 时会经 attributes 输出 aria-disabled="true"。
  // 而这张卡片是个 role="button" 的可点击元素：即使不能拖动（比如「执行中」
  // 列的任务，人类不能把它拖去别处），点开详情、改标题、看事件都应当照常。
  //
  // 若把 aria-disabled="true" 原样透出，会带来两个实际后果：
  // 1. 屏幕阅读器念出"不可用"，用户以为连详情都打不开；
  // 2. 自动化测试（以及部分辅助技术）会跳过它，click 直接超时。
  //
  // 所以这里显式覆盖成 false：拖拽能力由 title 与 aria-roledescription 之外
  // 的方式表达，可用性则如实反映 —— 卡片永远可点。
  const dragAttrs = {
    ...attributes,
    'aria-disabled': false as const,
  }

  return (
    <article
      ref={setNodeRef}
      {...dragAttrs}
      {...listeners}
      onClick={() => onOpen(task.id)}
      onKeyDown={(e) => {
        // ⚠️ 这里必须**先转发给 dnd-kit 的 onKeyDown**，再考虑自己的行为。
        //
        // JSX 属性后者覆盖前者：若把 {...listeners} 写在前面而自己再写一个
        // onKeyDown，dnd-kit 的键盘监听就被整体替换掉了 ——
        // 实测表现为 Space 拾不起拖拽（KeyboardSensor 收不到事件），
        // 键盘用户完全无法拖拽。
        //
        // Enter 与 Space 的分工（见 Board.tsx 的 keyboardCodes 配置）：
        // Space 归dnd-kit（拾起/放下），Enter 归我们（打开详情）。
        // 所以 Enter 要在转发**之前**处理 —— 若放在之后，
        // dnd-kit 在拖拽中会 consume 掉 Enter，我们的分支就永远不执行。
        if (e.key === 'Enter' && !isDragging) {
          e.preventDefault()
          onOpen(task.id)
          return
        }

        listeners?.onKeyDown?.(e)
      }}
      tabIndex={0}
      role="button"
      aria-label={`${task.title}，${PRIORITY_LABEL[task.priority]}优先级${
        agent ? `，指派给${agent.name}` : '，无人认领'
      }`}
      // 「不可拖」用 title 说明，而不是 aria-disabled ——
      // 后者会被读屏与自动化理解成"整个卡片不可用"。
      title={canDrag ? '拖动可流转到其他状态' : '该状态下不可拖动，点击查看详情'}
      className={cn(
        'group cursor-pointer rounded-lg border border-[var(--border)] bg-[var(--surface)] p-2.5',
        'shadow-[var(--shadow-card)] transition-shadow hover:border-[var(--border-strong)]',
        isDragging && 'dragging',
        !canDrag && 'cursor-default',
      )}
    >
      <div className="flex items-start gap-2">
        <h3 className="min-w-0 flex-1 text-sm font-medium leading-snug text-[var(--ink)]">
          {project ? (
            <span
              className="mr-1.5 inline-block h-2 w-2 rounded-full align-middle"
              style={{
                background: project.color || 'var(--ink-subtle)',
              }}
              title={project.name}
              aria-label={`项目：${project.name}`}
            />
          ) : null}
          {task.title}
        </h3>
        <Badge className={cn('shrink-0', PRIORITY_CLASS[task.priority])}>
          {PRIORITY_LABEL[task.priority]}
        </Badge>
      </div>

      {task.description ? (
        <p className="mt-1 line-clamp-2 text-xs leading-relaxed text-[var(--ink-muted)]">
          {task.description}
        </p>
      ) : null}

      <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-[var(--ink-subtle)]">
        {agent ? (
          <span
            className={cn(
              'inline-flex items-center gap-1 rounded px-1.5 py-0.5',
              agent.online
                ? 'bg-[var(--success-soft)] text-[var(--success)]'
                : 'bg-[var(--surface-hover)] text-[var(--ink-muted)]',
            )}
            title={agent.online ? `${agent.name} 在线` : `${agent.name} 离线`}
          >
            <CircleUser className="h-3 w-3" aria-hidden />
            {agent.name}
          </span>
        ) : (
          <span className="rounded bg-[var(--surface-hover)] px-1.5 py-0.5 text-[var(--ink-subtle)]">
            无人认领
          </span>
        )}

        {task.category ? (
          <span className="rounded bg-[var(--surface-hover)] px-1.5 py-0.5" title={task.route_reason}>
            {CATEGORY_LABEL[task.category]}
            {task.tier ? ` · ${TIER_LABEL[task.tier]}` : ''}
          </span>
        ) : null}

        {lease ? <LeaseBadge info={lease} /> : null}

        {task.archived_at ? (
          <span className="inline-flex items-center gap-1 text-[var(--ink-subtle)]">
            <Archive className="h-3 w-3" aria-hidden />
            已归档
          </span>
        ) : null}

        <span className="ml-auto" title={task.updated_at}>
          {formatRelative(task.updated_at)}
        </span>
      </div>
    </article>
  )
})

function LeaseBadge({ info }: { info: ReturnType<typeof leaseInfo> }) {
  if (info.state === 'none') return null
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium',
        info.state === 'expired' && 'bg-[var(--danger-soft)] text-[var(--danger)]',
        info.state === 'soon' && 'bg-[var(--warning-soft)] text-[var(--warning)]',
        info.state === 'ok' && 'bg-[var(--accent-soft)] text-[var(--accent)]',
      )}
      // 屏幕阅读器需要读出完整语义，不能只读"剩 3 分钟"
      aria-label={`租约${info.state === 'expired' ? '已超时' : '剩余'}：${info.label}`}
    >
      <Clock className="h-3 w-3" aria-hidden />
      {info.label}
    </span>
  )
}
