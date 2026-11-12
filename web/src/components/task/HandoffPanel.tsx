/**
 * 028 通用任务转交。
 *
 * 总管只负责把候选按能力与额度排好序并给出理由，选中谁、为什么转交
 * 全部由人决定。纯组件 + Section 拆分，渲染逻辑可脱离 React Query 单测。
 */

import { useState } from 'react'
import type { HandoffCandidate, Task } from '@/api/types'
import { Button, Input } from '@/components/ui/primitives'
import { useHandoff, useHandoffCandidates } from '@/hooks/useApi'

export interface HandoffPanelProps {
  candidates: HandoffCandidate[]
  onHandoff: (targetAgentId: number, reason: string) => void
  pending?: boolean
}

export function HandoffPanel({ candidates, onHandoff, pending = false }: HandoffPanelProps) {
  const [target, setTarget] = useState('')
  const [reason, setReason] = useState('')
  const chosen = target || (candidates[0] ? String(candidates[0].agent_id) : '')
  const canSubmit = Boolean(chosen) && reason.trim().length > 0 && !pending

  return (
    <section
      className="m-4 rounded-lg border border-[var(--border)] p-3"
      role="region"
      aria-label="任务转交"
    >
      <h3 className="text-xs font-semibold text-[var(--ink)]">转交给其他执行端</h3>
      <p className="mt-0.5 text-[11px] text-[var(--ink-subtle)]">
        总管只给排序建议，最终由你决定；仅待执行与待评审可转交。
      </p>

      {candidates.length === 0 ? (
        <p className="mt-2 text-xs text-[var(--ink-subtle)]">暂无可用转交目标</p>
      ) : (
        <ul className="mt-2 flex flex-col gap-1">
          {candidates.map((c, i) => (
            <li key={c.agent_id} className="flex items-start gap-2 text-xs">
              <input
                type="radio"
                name="handoff-target"
                aria-label={`推荐目标 ${c.name}`}
                value={c.agent_id}
                checked={chosen === String(c.agent_id)}
                onChange={() => setTarget(String(c.agent_id))}
              />
              <span className="min-w-0 flex-1">
                <span className="font-medium text-[var(--ink)]">
                  {i === 0 ? '推荐 · ' : ''}{c.name} · {c.score} 分
                </span>
                <span className="block text-[11px] text-[var(--ink-subtle)]">{c.reason}</span>
              </span>
            </li>
          ))}
        </ul>
      )}

      <div className="mt-2 flex gap-1.5">
        <Input
          aria-label="转交原因"
          className="h-7 text-xs"
          placeholder="转交原因（写入时间线）"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
        <Button
          size="sm"
          variant="primary"
          disabled={!canSubmit}
          onClick={() => onHandoff(Number(chosen), reason.trim())}
        >
          {pending ? '转交中…' : '转交'}
        </Button>
      </div>
    </section>
  )
}

/** 容器：接 React Query，只在 todo / review 且未归档时出现在任务抽屉。 */
export function HandoffSection({ task }: { task: Task }) {
  const candidates = useHandoffCandidates(task.id)
  const handoff = useHandoff()
  if (task.archived_at || (task.status !== 'todo' && task.status !== 'review')) return null
  return (
    <HandoffPanel
      candidates={candidates.data?.items ?? []}
      pending={handoff.isPending}
      onHandoff={(targetAgentId, reason) =>
        handoff.mutate({ taskId: task.id, targetAgentId, reason })
      }
    />
  )
}
