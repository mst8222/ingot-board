/**
 * 031 定时计划面板。
 *
 * 计划是「人预先授权的代派发」：只有显式选 auto 才会无人值守定人，
 * inherit / observe 只把模板实例落进灵感箱并生成建议等人采纳。
 * 纯组件 + 容器拆分便于单测。
 */

import { useState } from 'react'
import { X, Trash2, CalendarClock } from 'lucide-react'
import type { Schedule, ScheduleCreate, ScheduleDispatchMode, Template } from '@/api/types'
import { Badge, Button } from '@/components/ui/primitives'
import {
  errorMessage,
  useCreateSchedule,
  useDeleteSchedule,
  useSchedules,
  useTemplates,
  useUpdateSchedule,
} from '@/hooks/useApi'

const MODE_LABEL: Record<ScheduleDispatchMode, string> = {
  inherit: '跟随全局双模',
  observe: '只落建议',
  auto: '自动派发模式',
}

function cycleText(sc: Schedule): string {
  if (sc.interval_minutes) return `每 ${sc.interval_minutes} 分钟`
  if (sc.daily_at) return `每日 ${sc.daily_at} (UTC)`
  return '周期未设置'
}

export interface SchedulesPanelProps {
  schedules: Schedule[]
  templates: Template[]
  onToggle: (id: number, enabled: boolean) => void
  onDelete: (id: number) => void
  onCreate: (input: ScheduleCreate) => void
  onClose: () => void
  pending?: boolean
  error?: string | null
}

export function SchedulesPanel({
  schedules,
  templates,
  onToggle,
  onDelete,
  onCreate,
  onClose,
  pending = false,
  error = null,
}: SchedulesPanelProps) {
  const [name, setName] = useState('')
  // 模板由 React Query 异步加载：不能把首屏的空数组固化进 state，
  // 否则人没手动选过模板时「新建计划」会一直是禁用状态。
  const [chosenTemplateId, setChosenTemplateId] = useState<number | null>(null)
  const effectiveTemplateId = chosenTemplateId ?? templates[0]?.id ?? null
  const [mode, setMode] = useState<ScheduleDispatchMode>('inherit')
  const [minutes, setMinutes] = useState('60')

  const submit = () => {
    if (!name.trim() || effectiveTemplateId === null) return
    onCreate({
      name: name.trim(),
      template_id: effectiveTemplateId,
      dispatch_mode: mode,
      interval_minutes: Number(minutes) || 60,
    })
    setName('')
  }

  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} aria-hidden />
      <aside className="relative flex h-full w-full max-w-md flex-col border-l border-[var(--border)] bg-[var(--surface)]">
        <header className="flex items-center gap-2 border-b border-[var(--border)] px-4 py-3">
          <h2 className="text-sm font-semibold text-[var(--ink)]">定时计划</h2>
          <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="关闭">
            <X className="h-4 w-4" aria-hidden />
          </Button>
        </header>
        <div className="scroll-thin flex-1 overflow-y-auto p-4">
          <p className="mb-3 text-xs text-[var(--ink-subtle)]">
            把模板挂到计划上，服务按周期在进程内自动生成任务。选 auto 才会无人值守定人；
            observe 只落灵感箱与建议，仍由人采纳。服务关闭期间不执行、重启最多补偿一次。
          </p>

          <div className="mb-4 rounded-lg border border-[var(--border)] p-3">
            <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-[var(--ink)]">
              <CalendarClock className="h-3.5 w-3.5" aria-hidden />
              新建计划
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs text-[var(--ink-subtle)]">
                计划名称
                <input
                  aria-label="计划名称"
                  className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-sm text-[var(--ink)]"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如：每日复盘"
                />
              </label>
              <label className="text-xs text-[var(--ink-subtle)]">
                模板
                <select
                  aria-label="模板"
                  className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-sm text-[var(--ink)]"
                  value={effectiveTemplateId ?? ''}
                  onChange={(e) => setChosenTemplateId(Number(e.target.value) || null)}
                >
                  {templates.length === 0 ? <option value="">暂无可用模板</option> : null}
                  {templates.filter((t) => !t.archived_at).map((t) => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </label>
              <label className="text-xs text-[var(--ink-subtle)]">
                派发模式
                <select
                  aria-label="派发模式"
                  className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-sm text-[var(--ink)]"
                  value={mode}
                  onChange={(e) => setMode(e.target.value as ScheduleDispatchMode)}
                >
                  <option value="inherit">inherit · 跟随全局</option>
                  <option value="observe">observe · 只落建议</option>
                  <option value="auto">auto · 无人值守派发</option>
                </select>
              </label>
              <label className="text-xs text-[var(--ink-subtle)]">
                间隔（分钟）
                <input
                  aria-label="间隔（分钟）"
                  type="number"
                  min={1}
                  className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-sm text-[var(--ink)]"
                  value={minutes}
                  onChange={(e) => setMinutes(e.target.value)}
                />
              </label>
              <Button size="sm" disabled={pending || !name.trim() || effectiveTemplateId === null} onClick={submit}>
                新建计划
              </Button>
            </div>
          </div>

          {error ? <p className="mb-2 text-xs text-[var(--danger)]">{error}</p> : null}

          {schedules.length === 0 ? (
            <p className="py-8 text-center text-xs text-[var(--ink-subtle)]">还没有计划</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {schedules.map((sc) => {
                const tpl = templates.find((t) => t.id === sc.template_id)
                return (
                  <li key={sc.id} className="rounded-lg border border-[var(--border)] p-2.5">
                    <div className="flex items-center gap-2">
                      <span className="min-w-0 flex-1 truncate text-sm font-medium text-[var(--ink)]">
                        {sc.name}
                      </span>
                      {sc.enabled ? null : <Badge>已停用</Badge>}
                    </div>
                    <p className="mt-1 text-xs text-[var(--ink-subtle)]">
                      {tpl?.name ?? `模板 #${sc.template_id}`} · {MODE_LABEL[sc.dispatch_mode]} · {cycleText(sc)}
                    </p>
                    <p className="mt-0.5 text-xs text-[var(--ink-subtle)]">
                      下次触发 {sc.next_run_at}
                    </p>
                    <div className="mt-2 flex gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={pending}
                        onClick={() => onToggle(sc.id, !sc.enabled)}
                      >
                        {sc.enabled ? '停用' : '启用'}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="ml-auto text-[var(--danger)]"
                        disabled={pending}
                        onClick={() => onDelete(sc.id)}
                      >
                        <Trash2 className="h-3 w-3" aria-hidden />
                        删除
                      </Button>
                    </div>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      </aside>
    </div>
  )
}

/** 容器：接 React Query，供顶栏「计划」按钮打开。 */
export function SchedulesDialog({ onClose }: { onClose: () => void }) {
  const list = useSchedules()
  const templates = useTemplates()
  const create = useCreateSchedule()
  const update = useUpdateSchedule()
  const del = useDeleteSchedule()
  const err = create.error ?? update.error ?? del.error
  return (
    <SchedulesPanel
      schedules={list.data?.items ?? []}
      templates={templates.data?.items ?? []}
      pending={create.isPending || update.isPending || del.isPending}
      error={err ? errorMessage(err) : null}
      onCreate={(input) => create.mutate(input)}
      onToggle={(id, enabled) => update.mutate({ id, patch: { enabled } })}
      onDelete={(id) => del.mutate(id)}
      onClose={onClose}
    />
  )
}
