/**
 * 032-B 执行端数值余额。
 *
 * 纯展示组件 + Section 容器拆分，是为了让余额与流水的渲染能脱离
 * React Query 单独测试；数据获取与失效策略统一留在 useApi 里。
 *
 * NULL 余额不是 0：它代表「未录入」，总管预算硬门禁会跳过、只提示，
 * 终审也不会凭空扣出一个负数余额。
 */

import { useState } from 'react'
import type { Agent, BudgetEntry } from '@/api/types'
import { Button, Input } from '@/components/ui/primitives'
import { useAgentLedger, useSetAgentBalance } from '@/hooks/useApi'

const REASON_LABEL: Record<BudgetEntry['reason'], string> = {
  human_set: '人工录入 / 校准',
  human_clear: '清空余额',
  task_settled: '任务结算扣减',
  probe_exhausted: '额度探测归零',
}

export interface BalanceEditorProps {
  balance: number | null
  unit: string
  updatedAt: string | null
  entries: BudgetEntry[]
  loading?: boolean
  onSave: (balance: number | null) => void
}

export function BalanceEditor({
  balance,
  unit,
  updatedAt,
  entries,
  loading = false,
  onSave,
}: BalanceEditorProps) {
  const [draft, setDraft] = useState('')

  const submit = () => {
    const raw = draft.trim()
    if (!raw) return
    const value = Number(raw)
    if (!Number.isFinite(value) || value < 0) return
    onSave(value)
    setDraft('')
  }

  return (
    <div className="mt-2 rounded-md border border-[var(--border)] p-2" role="region" aria-label="数值余额">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        {balance === null ? (
          <span className="text-[var(--ink-subtle)]">余额未录入 · 预算门禁跳过</span>
        ) : (
          <span className="font-medium text-[var(--ink)]">
            {balance} {unit}
          </span>
        )}
        {updatedAt ? (
          <span className="text-[var(--ink-subtle)]">更新于 {updatedAt}</span>
        ) : null}
      </div>

      <div className="mt-1.5 flex items-center gap-1.5">
        <Input
          type="number"
          min={0}
          step="any"
          aria-label="数值余额"
          className="h-7 w-24 text-xs"
          placeholder="余额"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button size="sm" variant="ghost" disabled={loading} onClick={submit}>
          {loading ? '保存中…' : '录入'}
        </Button>
        {balance !== null ? (
          <Button size="sm" variant="ghost" disabled={loading} onClick={() => onSave(null)}>
            清空
          </Button>
        ) : null}
      </div>

      {entries.length > 0 ? (
        <ul className="mt-2 flex flex-col gap-0.5 border-t border-[var(--border)] pt-1.5">
          {entries.slice(0, 5).map((e) => (
            <li key={e.id} className="flex justify-between gap-2 text-[11px] text-[var(--ink-muted)]">
              <span className="truncate">
                {REASON_LABEL[e.reason] ?? e.reason}
                {e.task_id ? ` · 任务 #${e.task_id}` : ''}
              </span>
              <span className="shrink-0 tabular-nums">
                {e.delta > 0 ? '+' : ''}{e.delta} {unit}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

/** 容器：接 React Query，供 Agent 管理面板直接内嵌。 */
export function BalanceSection({ agent }: { agent: Agent }) {
  const ledger = useAgentLedger(agent.id)
  const save = useSetAgentBalance()
  return (
    <BalanceEditor
      balance={agent.balance}
      unit={agent.balance_unit || 'credit'}
      updatedAt={agent.budget_updated_at}
      entries={ledger.data?.items ?? []}
      loading={save.isPending}
      onSave={(balance) => save.mutate({ id: agent.id, balance })}
    />
  )
}
