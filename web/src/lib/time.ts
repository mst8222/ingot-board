/**
 * 时间与租约计算。
 *
 * 关键点：**用服务端时钟做基准，不是浏览器时钟**。
 *
 * 原前端用 `new Date()` 和服务端时间比，客户端时钟漂 5 分钟就会把
 * "还剩 2 分钟"显示成"已超时"，Agent 也会被误判为掉线。现在 meta.server_time
 * 给出基准，页面加载时记录一次偏移量，之后所有倒计时都用偏移修正。
 *
 * 偏移量只在页面生命周期内有效：用户不会开着看板过夜，若真跨过很久，
 * 下一次 SSE 变更或窗口重新聚焦会重新拉 meta 校准。
 */

import type { Task } from '@/api/types'

let clockSkewMs = 0

/** 用 meta.server_time 校准时钟偏移。 */
export function syncClock(serverTime: string): void {
  const server = Date.parse(serverTime)
  if (Number.isNaN(server)) return
  // 服务端用 RFC3339 秒级精度，误差远小于时钟漂移，可以直接当基准
  clockSkewMs = server - Date.now()
}

/** 修正后的"现在"。 */
export function now(): number {
  return Date.now() + clockSkewMs
}

/** 仅供测试：重置偏移 */
export function resetClock(): void {
  clockSkewMs = 0
}

export type LeaseState = 'none' | 'ok' | 'soon' | 'expired'

export interface LeaseInfo {
  state: LeaseState
  /** 剩余毫秒；expired 时为负（已超时多久） */
  remainingMs: number
  /** "3 分钟"这样的短文案 */
  label: string
}

/** 剩余不足这个时间就算"即将到期"，用醒目样式提示 Agent 可能掉线 */
const SOON_THRESHOLD_MS = 60_000

/**
 * 计算租约显示状态。
 *
 * 注意 `expired` 的含义：租约时间已过但回收器还没跑（最多 30s 周期）。
 * 这段时间任务状态仍是 `doing`，但租约实际已失效 —— 所以要显示"已超时"
 * 而不是继续倒计时，否则用户会以为 Agent 还在正常干活。
 */
export function leaseInfo(leaseUntil: string | null): LeaseInfo {
  if (!leaseUntil) {
    return { state: 'none', remainingMs: 0, label: '' }
  }
  const until = Date.parse(leaseUntil)
  if (Number.isNaN(until)) {
    return { state: 'none', remainingMs: 0, label: '' }
  }
  const remainingMs = until - now()

  if (remainingMs <= 0) {
    return { state: 'expired', remainingMs, label: `已超时 ${formatDuration(-remainingMs)}` }
  }
  if (remainingMs <= SOON_THRESHOLD_MS) {
    return { state: 'soon', remainingMs, label: `剩 ${formatDuration(remainingMs)}` }
  }
  return { state: 'ok', remainingMs, label: formatDuration(remainingMs) }
}

/** 时长 → "2 天" / "3 小时" / "5 分钟" / "40 秒"。 */
export function formatDuration(ms: number): string {
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s} 秒`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分钟`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时`
  const d = Math.floor(h / 24)
  if (d < 30) return `${d} 天`
  return `${Math.floor(d / 30)} 个月`
}

/** 相对时间 → "刚刚" / "5 分钟前" / "3 天前"。 */
export function formatRelative(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const diff = now() - t
  if (diff < 0) return '刚刚'
  if (diff < 45_000) return '刚刚'
  return `${formatDuration(diff)}前`
}

/** 任务是否处于"Agent 正在处理"的状态 —— 决定是否该显示租约。 */
export function hasActiveLease(task: Task): boolean {
  return task.lease_until !== null && task.status === 'doing'
}
