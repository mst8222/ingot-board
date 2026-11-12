/**
 * 总管派发建议卡（032-dispatch-governance）。
 *
 * observe 下未指派任务被拖向 todo 时，后端返回 409 并已落一条决策建议；
 * 这张卡展示评分、理由与预估成本，人点「采纳并派发」才真正改状态。
 */

import type { DispatchDecision } from '@/api/types'
import { useAcceptSuggestion, useDispatchSuggestion } from '@/hooks/useApi'

interface ScoreRow {
  name: string
  total: number
  note?: string
}

function parseScores(raw: string): ScoreRow[] {
  try {
    const value: unknown = JSON.parse(raw)
    if (!Array.isArray(value)) return []
    return value
      .filter((v): v is Record<string, unknown> => typeof v === 'object' && v !== null)
      .map((v) => ({
        name: String(v.name ?? ''),
        total: Number(v.total ?? 0),
        note: v.note ? String(v.note) : undefined,
      }))
  } catch {
    return []
  }
}

export function DispatchSuggestionCard({
  decision,
  loading = false,
  error,
  accepting = false,
  onAccept,
  onClose,
}: {
  decision?: DispatchDecision
  loading?: boolean
  error?: string
  accepting?: boolean
  onAccept: (decisionId: number) => void
  onClose: () => void
}) {
  const scores = decision ? parseScores(decision.score_json) : []
  return (
    <div
      role="dialog"
      aria-label="总管派发建议"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
    >
      <div className="w-full max-w-md rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xl">
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-[var(--ink)]">总管派发建议</h3>
          <button type="button" onClick={onClose} className="text-xs text-[var(--ink-subtle)]">
            关闭
          </button>
        </div>

        {loading ? <p className="text-xs text-[var(--ink-muted)]">加载中…</p> : null}
        {error ? <p className="text-xs text-[var(--danger)]">{error}</p> : null}
        {!loading && !error && !decision ? (
          <p className="text-xs text-[var(--ink-muted)]">暂无派发建议</p>
        ) : null}

        {decision ? (
          <>
            <p className="text-xs text-[var(--ink-subtle)]">
              类别 {decision.category || '未分类'} · 档位 {decision.tier || '未定'} · 预估成本{' '}
              {decision.cost_estimate} credit
            </p>
            <p className="mt-2 text-xs text-[var(--ink)]">{decision.reason}</p>
            <p className="mt-1 text-xs text-[var(--ink-subtle)]">
              推荐执行端 #{decision.chosen_agent_id ?? '-'}
            </p>

            {scores.length > 0 ? (
              <ul className="mt-3 flex flex-col gap-1" aria-label="候选评分">
                {scores.map((s) => (
                  <li key={s.name} className="text-xs text-[var(--ink-subtle)]">
                    {s.name} · {s.total} 分{s.note ? ` · ${s.note}` : ''}
                  </li>
                ))}
              </ul>
            ) : null}

            <button
              type="button"
              disabled={accepting}
              onClick={() => onAccept(decision.id)}
              className="mt-4 rounded bg-[var(--accent)] px-3 py-1.5 text-xs text-white disabled:opacity-50"
            >
              {accepting ? '派发中…' : '采纳并派发'}
            </button>
          </>
        ) : null}
      </div>
    </div>
  )
}

/** 容器：按任务 ID 拉建议并提交采纳。 */
export function DispatchSuggestionSection({
  taskId,
  onClose,
}: {
  taskId: number
  onClose: () => void
}) {
  const q = useDispatchSuggestion(taskId)
  const accept = useAcceptSuggestion()
  return (
    <DispatchSuggestionCard
      decision={q.data}
      loading={q.isLoading}
      error={q.error instanceof Error ? q.error.message : undefined}
      accepting={accept.isPending}
      onAccept={(decisionId) =>
        accept.mutate({ taskId, decisionId }, { onSuccess: onClose })
      }
      onClose={onClose}
    />
  )
}
