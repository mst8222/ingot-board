/**
 * 窗口模式下的列页签：全部 5 列的总览。
 *
 * 宽度放不下所有列时看板只显示连续的 N 列，页签告诉用户"其余的列在哪、各有多少"：
 * - 窗口内的列高亮，窗口外的变暗，已折叠的列带折叠标记；
 * - 点击页签把那一列纳入窗口（已折叠的会先展开）；
 * - 页签也是放置区：直接松手 = 放到该列末尾；
 *   拖拽悬停片刻 = 平移窗口过去，之后可放到列中任意位置。
 */

import { useEffect } from 'react'
import { useDroppable } from '@dnd-kit/core'
import { ChevronRight } from 'lucide-react'
import type { TaskStatus } from '@/api/types'
import { COLUMN_ORDER } from '@/lib/dragRules'
import { TAB_HOVER_SWITCH_MS } from '@/lib/boardLayout'
import { cn } from '@/lib/cn'
import { COLUMN_LABEL } from './Column'

export interface ColumnTabsProps {
  counts: Record<TaskStatus, number>
  visible: ReadonlySet<TaskStatus>
  collapsed: Record<string, boolean>
  /** 是否有卡片正在拖拽中；悬停平移只在拖拽时生效 */
  dragging: boolean
  onSelect: (status: TaskStatus) => void
}

export function ColumnTabs({ counts, visible, collapsed, dragging, onSelect }: ColumnTabsProps) {
  return (
    <div role="tablist" aria-label="看板列" className="flex flex-wrap gap-1.5 px-3 pt-3">
      {COLUMN_ORDER.map((status) => (
        <Tab
          key={status}
          status={status}
          count={counts[status]}
          active={visible.has(status)}
          folded={collapsed[status] === true}
          dragging={dragging}
          onSelect={onSelect}
        />
      ))}
    </div>
  )
}

function Tab({
  status,
  count,
  active,
  folded,
  dragging,
  onSelect,
}: {
  status: TaskStatus
  count: number
  active: boolean
  folded: boolean
  dragging: boolean
  onSelect: (status: TaskStatus) => void
}) {
  const { setNodeRef, isOver } = useDroppable({ id: `tab:${status}` })

  // 拖拽中悬停在窗口外的页签上：停留片刻再平移，划过时不触发
  // 已折叠的列不在拖拽中自动展开 —— 展开会改变用户的折叠设置，只做"放到末尾"
  const hoverSwitch = dragging && isOver && !active && !folded
  useEffect(() => {
    if (!hoverSwitch) return
    const t = setTimeout(() => onSelect(status), TAB_HOVER_SWITCH_MS)
    return () => clearTimeout(t)
  }, [hoverSwitch, onSelect, status])

  return (
    <button
      ref={setNodeRef}
      type="button"
      role="tab"
      aria-selected={active}
      onClick={() => onSelect(status)}
      title={folded ? `${COLUMN_LABEL[status]}（已折叠，点击展开）` : COLUMN_LABEL[status]}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm transition-colors',
        isOver
          ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--ink)]'
          : active
            ? 'border-[var(--accent)] bg-[var(--surface)] text-[var(--ink)]'
            : 'border-[var(--border)] text-[var(--ink-subtle)] hover:bg-[var(--surface-hover)]',
        folded && !isOver && 'border-dashed',
      )}
    >
      <span
        className={cn('h-2 w-2 shrink-0 rounded-full', !active && 'opacity-50')}
        style={{ background: `var(--status-${status})` }}
        aria-hidden
      />
      <span>{COLUMN_LABEL[status]}</span>
      <span className="text-xs text-[var(--ink-subtle)]">{count}</span>
      {folded ? <ChevronRight className="h-3 w-3 shrink-0" aria-label="已折叠" /> : null}
    </button>
  )
}
