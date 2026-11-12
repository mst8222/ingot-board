/**
 * 看板布局 —— 纯函数，无 React 依赖。
 *
 * 看板不横向滚动。宽度放得下全部列时全部显示（折叠列为窄条）；
 * 放不下时进入"窗口模式"：只显示未折叠列中连续的 N 列，
 * 顶部页签作为全部 5 列的总览，点击或拖拽悬停页签会平移窗口。
 *
 * 窗口取连续列而不是任意组合：跨列拖拽绝大多数发生在相邻状态之间
 * （待执行 → 执行中 → 评审中），连续窗口让最常见的拖拽不需要切换。
 */

import type { TaskStatus } from '@/api/types'
import { COLUMN_ORDER } from './dragRules'

/** 展开列的最小宽度。低于它卡片标题会频繁截断。 */
export const MIN_COLUMN_WIDTH = 256
/** 折叠列（竖条）宽度。 */
export const COLLAPSED_COLUMN_WIDTH = 56
/** 列间距，与看板容器的 gap-3 一致。 */
export const COLUMN_GAP = 12
/** 拖拽悬停在页签上多久后平移窗口。 */
export const TAB_HOVER_SWITCH_MS = 500

export type BoardLayout =
  | { mode: 'all' }
  | { mode: 'window'; capacity: number }

/** 未折叠的列，按流转顺序。窗口只在这个序列上滑动。 */
export function expandedSequence(collapsed: Record<string, boolean>): TaskStatus[] {
  return COLUMN_ORDER.filter((s) => !collapsed[s])
}

/**
 * 根据内容区宽度（不含内边距）决定布局。
 *
 * 判定按"当前所有列实际需要的宽度"而不是固定断点：
 * 折叠的列只占窄条，"已完成"默认折叠时阈值自动降低。
 */
export function computeLayout(width: number, collapsed: Record<string, boolean>): BoardLayout {
  const expanded = expandedSequence(collapsed).length
  const folded = COLUMN_ORDER.length - expanded
  const need =
    expanded * MIN_COLUMN_WIDTH +
    folded * COLLAPSED_COLUMN_WIDTH +
    (COLUMN_ORDER.length - 1) * COLUMN_GAP
  if (width >= need) return { mode: 'all' }

  const fit = Math.floor((width + COLUMN_GAP) / (MIN_COLUMN_WIDTH + COLUMN_GAP))
  // 最窄也显示一列；全部折叠时没有可显示的列，只剩页签
  return { mode: 'window', capacity: Math.min(Math.max(fit, 1), expanded) }
}

/**
 * 窗口起点下标。
 *
 * 起点按状态名记忆（宽度变化后容量变了仍然稳定）。
 * 若记住的状态已被折叠，取它之后第一个未折叠的列；
 * 未记忆过时从「待执行」开始 —— 它是人类最常看的列。
 * 结果会被夹紧，保证窗口总是填满 capacity 列。
 */
export function windowStartIndex(
  sequence: readonly TaskStatus[],
  start: TaskStatus | undefined,
  capacity: number,
): number {
  const anchor = COLUMN_ORDER.indexOf(start ?? 'todo')
  let idx = sequence.findIndex((s) => COLUMN_ORDER.indexOf(s) >= anchor)
  if (idx < 0) idx = sequence.length - 1
  return Math.max(0, Math.min(idx, sequence.length - capacity))
}

/** 当前窗口内可见的列。 */
export function visibleWindow(
  sequence: readonly TaskStatus[],
  start: TaskStatus | undefined,
  capacity: number,
): TaskStatus[] {
  const i = windowStartIndex(sequence, start, capacity)
  return sequence.slice(i, i + capacity)
}

/**
 * 以最小平移把 target 纳入窗口，返回新的起点状态。
 *
 * target 在窗口左侧 → 它成为第一列；在右侧 → 它成为最后一列；
 * 已在窗口内 → 不动。target 不在序列中（已折叠）时由调用方先展开。
 */
export function shiftWindow(
  sequence: readonly TaskStatus[],
  start: TaskStatus | undefined,
  capacity: number,
  target: TaskStatus,
): TaskStatus | undefined {
  const t = sequence.indexOf(target)
  if (t < 0 || capacity <= 0) return start
  const i = windowStartIndex(sequence, start, capacity)
  let next = i
  if (t < i) next = t
  else if (t >= i + capacity) next = t - capacity + 1
  return sequence[next]
}
