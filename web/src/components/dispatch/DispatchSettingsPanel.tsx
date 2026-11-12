/**
 * 总管派发的双模开关（032-dispatch-governance）。
 *
 * observe 是默认值：总管只给可解释的建议，人一键采纳才落指派；
 * auto 才允许未指派任务进入 todo 时由总管自动定人。
 */

import type { DispatchMode, DispatchSettings } from '@/api/types'
import { useDispatchSettings, useSetDispatchMode } from '@/hooks/useApi'

export function DispatchSettingsPanel({
  settings,
  loading = false,
  onChange,
}: {
  settings?: DispatchSettings
  loading?: boolean
  onChange: (mode: DispatchMode) => void
}) {
  const mode: DispatchMode = settings?.mode ?? 'observe'
  return (
    <section aria-label="总管派发" className="rounded-lg border border-[var(--border)] p-3">
      <div className="mb-1 flex items-center gap-2">
        <h3 className="text-sm font-semibold text-[var(--ink)]">总管派发</h3>
        <span className="text-xs text-[var(--ink-subtle)]">
          当前：{mode === 'observe' ? '观察模式' : '自动派发模式'}
        </span>
      </div>
      <p className="mb-2 text-xs text-[var(--ink-subtle)]">
        观察模式只给建议，任务留在灵感箱；自动派发会在拖入待执行时按能力、额度与成本自动选执行端。
      </p>
      <div role="group" aria-label="派发模式" className="flex gap-2">
        <button
          type="button"
          aria-pressed={mode === 'observe'}
          onClick={() => onChange('observe')}
          className="rounded border border-[var(--border)] px-2 py-1 text-xs text-[var(--ink)]"
        >
          观察（推荐）
        </button>
        <button
          type="button"
          aria-pressed={mode === 'auto'}
          onClick={() => onChange('auto')}
          className="rounded border border-[var(--border)] px-2 py-1 text-xs text-[var(--ink)]"
        >
          自动派发
        </button>
      </div>
      {loading ? <p className="mt-2 text-xs text-[var(--ink-muted)]">加载中…</p> : null}
    </section>
  )
}

/** 容器：唯一负责取数据与提交切换的地方，便于纯组件单测。 */
export function DispatchSettingsSection() {
  const q = useDispatchSettings()
  const mutation = useSetDispatchMode()
  return (
    <DispatchSettingsPanel
      settings={q.data}
      loading={q.isLoading}
      onChange={(mode) => mutation.mutate(mode)}
    />
  )
}
