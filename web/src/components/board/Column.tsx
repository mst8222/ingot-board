/**
 * 看板单列。
 *
 * 「已完成」默认折叠（approved）：完成的任务不该继续占据视野，
 * 但也不能消失 —— 折叠态显示计数，点开可看，符合 meta.collapsed_by_default。
 *
 * 折叠状态存在 localStorage 而不是组件内：刷新后应保持用户的选择。
 */

import { useDroppable } from '@dnd-kit/core'
import { ChevronDown, ChevronRight, AlertTriangle } from 'lucide-react'
import type { Agent, Project, Task, TaskStatus } from '@/api/types'
import { TaskCard } from './TaskCard'
import { cn } from '@/lib/cn'
import { COLLAPSED_COLUMN_WIDTH, MIN_COLUMN_WIDTH } from '@/lib/boardLayout'

export const COLUMN_LABEL: Record<TaskStatus, string> = {
  idea: '灵感箱',
  todo: '待执行',
  doing: '执行中',
  review: '评审中',
  approved: '已完成',
}

export interface ColumnProps {
  status: TaskStatus
  tasks: Task[]
  /** 服务端给的完整计数（不受 per_column 截断影响） */
  count: number
  shownCount: number
  truncated: boolean
  agents: Map<number, Agent>
  projects: Map<number, Project>
  collapsed: boolean
  pending: Set<number>
  onToggleCollapse: () => void
  onOpenTask: (id: number) => void
  canDrag: boolean
  /**
   * 窗口模式下不在窗口内。此时列仍保持挂载（被拖的卡片可能在这一列，
   * 卸载会让进行中的拖拽丢失源元素），只隐藏并关闭放置区。
   */
  hidden: boolean
}

export function Column({
  status,
  tasks,
  count,
  shownCount,
  truncated,
  agents,
  projects,
  collapsed,
  pending,
  onToggleCollapse,
  onOpenTask,
  canDrag,
  hidden,
}: ColumnProps) {
  // 整个列作为放置区：空列也能接收拖入
  const { setNodeRef, isOver } = useDroppable({ id: `col:${status}`, disabled: hidden })

  const isTerminal = status === 'approved'
  // 服务端按 per_column 截断，只返回了部分任务
  const partial = truncated && shownCount < count

  return (
    <section
      className={cn(
        hidden ? 'hidden' : 'flex',
        'flex-col rounded-lg border transition-colors',
        isOver ? 'border-[var(--accent)] bg-[var(--accent-soft)]' : 'border-[var(--border)]',
        'bg-[var(--surface-sunken)]',
      )}
      // 展开列均分剩余宽度（不横向滚动）；折叠列固定为窄条
      style={
        collapsed
          ? { width: COLLAPSED_COLUMN_WIDTH, flex: 'none' }
          : { minWidth: MIN_COLUMN_WIDTH, flex: '1 1 0' }
      }
      aria-label={COLUMN_LABEL[status]}
    >
      <header className="flex items-center gap-1.5 px-2 py-2">
        <button
          type="button"
          onClick={onToggleCollapse}
          aria-expanded={!collapsed}
          className={cn(
            'flex min-w-0 flex-1 items-center gap-1.5 rounded text-left',
            'text-[var(--ink)] hover:bg-[var(--surface-hover)]',
            collapsed && 'justify-center',
          )}
          title={collapsed ? `展开${COLUMN_LABEL[status]}` : `折叠${COLUMN_LABEL[status]}`}
        >
          {collapsed ? (
            <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden />
          ) : (
            <ChevronDown className="h-3.5 w-3.5 shrink-0" aria-hidden />
          )}
          {!collapsed ? (
            <>
              <span
                className="h-2 w-2 shrink-0 rounded-full"
                style={{ background: `var(--status-${status})` }}
                aria-hidden
              />
              <span className="truncate text-sm font-medium">{COLUMN_LABEL[status]}</span>
              {/* 折叠态也要能看到数量，否则用户不知道该展开 */}
              <span className="ml-auto shrink-0 text-xs text-[var(--ink-subtle)]">
                {count}
              </span>
            </>
          ) : null}
        </button>
      </header>

      {!collapsed ? (
        <div
          ref={setNodeRef}
          className="scroll-thin flex min-h-24 flex-1 flex-col gap-1.5 overflow-y-auto px-1.5 pb-1.5"
        >
          {partial ? (
            <p className="flex items-center gap-1 rounded bg-[var(--warning-soft)] px-2 py-1 text-xs text-[var(--warning)]">
              <AlertTriangle className="h-3 w-3 shrink-0" aria-hidden />
              仅显示 {shownCount}/{count} 条，收窄筛选或改用列表视图
            </p>
          ) : null}

          {tasks.length === 0 ? (
            <p className="py-6 text-center text-xs text-[var(--ink-subtle)]">
              {isTerminal ? '还没有完成的任务' : '拖动任务到此列'}
            </p>
          ) : (
            tasks.map((task) => (
              <div
                key={task.id}
                className={cn(
                  // 提交中：降低透明度并禁用点击，避免重复提交
                  pending.has(task.id) && 'pointer-events-none opacity-60',
                )}
              >
                <TaskCard
                  task={task}
                  agents={agents}
                  projects={projects}
                  onOpen={onOpenTask}
                  canDrag={canDrag}
                  droppable={!hidden}
                />
              </div>
            ))
          )}
        </div>
      ) : null}
    </section>
  )
}
