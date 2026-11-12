/**
 * 筛选栏。
 *
 * 为什么筛选必须在服务端：SSE 推的是变更事件，前端要在本地判断
 * "这条变更后的任务是否还满足筛选"才能决定要不要重排。筛选在客户端时
 * 这个判断做不完整，看板会漂移（被筛掉的任务突然冒出来）。
 *
 * 交互决策：
 * - 搜索框**防抖 250ms** 才提交。原前端是每次按键都发请求，
 *   打 5 个字就是 5 次往返，且每次都会重排看板。
 * - 状态/优先级用 toggle 按钮而不是多选框：选项少（5 个状态、4 个优先级），
 *   一眼能看全当前选中状态，多选框还要点开才知道选了什么。
 * - assignee 与 unassigned 互斥：见 store/filters.ts 的说明。
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import { Search, X, SlidersHorizontal, Check } from 'lucide-react'
import { Button, Input } from '@/components/ui/primitives'
import { useFilters } from '@/store/filters'
import type { Meta, Priority, Project, SortField, TaskStatus } from '@/api/types'
import { cn } from '@/lib/cn'

const SEARCH_DEBOUNCE_MS = 250

export function FilterBar({
  meta,
  agents,
  projects,
  className,
}: {
  meta?: Meta
  agents: { id: number; name: string; enabled: boolean }[]
  /** 全量项目列表（含已归档的兜底渲染，避免 URL 筛选静默丢失） */
  projects?: Project[]
  className?: string
}) {
  const f = useFilters()
  const [expanded, setExpanded] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // 防抖提交：输入过程中不发请求
  useEffect(() => {
    const t = setTimeout(() => f.commitSearch(), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(t)
    // 只依赖 searchInput —— commitSearch 每次渲染都是新函数引用，
    // 放进依赖里会不断重置定时器，防抖就失效了。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [f.searchInput])

  // "/" 聚焦搜索框：看板是键盘密集界面，省一次鼠标点击
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      if (e.key === '/') {
        e.preventDefault()
        inputRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const activeCount = useMemo(() => {
    let n = 0
    if (f.status?.length) n += f.status.length
    if (f.priority?.length) n += f.priority.length
    if (f.projects?.length) n += f.projects.length
    if (f.no_project) n += 1
    if (f.assignee !== undefined) n += 1
    if (f.unassigned) n += 1
    if (f.archived !== 'exclude') n += 1
    return n
  }, [f])

  const statuses = meta?.statuses ?? []
  const priorities = meta?.priorities ?? []

  return (
    <div className={cn('flex flex-col gap-2', className)}>
      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-md">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--ink-subtle)]"
            aria-hidden
          />
          <Input
            ref={inputRef}
            value={f.searchInput}
            onChange={(e) => f.setSearchInput(e.target.value)}
            placeholder="搜索标题或描述（按 / 聚焦）"
            className="pl-7"
            aria-label="搜索任务"
          />
        </div>

        <Button
          variant={expanded ? 'primary' : 'secondary'}
          size="md"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          aria-controls="filter-panel"
        >
          <SlidersHorizontal className="h-3.5 w-3.5" aria-hidden />
          筛选
          {activeCount > 0 ? (
            <span
              className="ml-0.5 rounded-full bg-[var(--accent)] px-1.5 text-[10px] font-semibold text-[var(--accent-ink)]"
              aria-label={`${activeCount} 个筛选条件生效中`}
            >
              {activeCount}
            </span>
          ) : null}
        </Button>

        {activeCount > 0 || f.q ? (
          <Button variant="ghost" size="md" onClick={f.clearAll}>
            <X className="h-3.5 w-3.5" aria-hidden />
            清除
          </Button>
        ) : null}

        {/* 排序常驻在工具栏，不放进折叠面板。

            原因：只有「手动顺序」下拖拽重排才有意义，其余排序下重排
            "成功但看不见"。这直接决定用户能不能用重排，把它藏进需要点开的
            面板里，等于让这个能力处于"找不到"的状态 ——
            用户会先尝试拖拽、失败、再去猜原因。

            标签随 meta 走而不是前端硬编码：多一份就多一处可能漂移
            （曾出现前端发 updated_desc、后端只认 updated_at 的 400）。 */}
        <label htmlFor="sort-select" className="sr-only">
          排序方式
        </label>
        <select
          id="sort-select"
          // 空值即服务端默认（手动顺序），不要在本地硬编码默认值：
          // 两处各写一个默认值迟早会不一致，而"看板默认按什么排"
          // 必须只有一个答案。
          value={f.sort ?? 'manual'}
          onChange={(e) => f.setSort(e.target.value as SortField)}
          className="h-8 rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-xs text-[var(--ink)]"
        >
          {(meta?.sort_fields ?? [{ value: 'manual', label: '手动顺序' }]).map((sf) => (
            <option key={sf.value} value={sf.value}>
              {sf.label}
            </option>
          ))}
        </select>
      </div>

      {/* 非手动排序时明说不能拖 —— 提前告知好过让用户拖了发现顺序没变 */}
      {f.sort && f.sort !== 'manual' ? (
        <p role="status" className="text-xs text-[var(--ink-muted)]">
          当前排序下不能拖动调整顺序，切回「手动顺序」即可。
        </p>
      ) : null}

      {expanded ? (
        <div
          id="filter-panel"
          className="flex flex-col gap-3 rounded-lg border border-[var(--border)] bg-[var(--surface)] p-3"
        >
          <FilterGroup label="状态">
            {statuses.map((s) => (
              <ToggleChip
                key={s.value}
                active={f.status?.includes(s.value)}
                onClick={() => f.toggleStatus(s.value as TaskStatus)}
                title={s.hint}
                dotColor={`var(--status-${s.value})`}
              >
                {s.label}
              </ToggleChip>
            ))}
          </FilterGroup>

          <FilterGroup label="优先级">
            {priorities.map((p) => (
              <ToggleChip
                key={p.value}
                active={f.priority?.includes(p.value as Priority)}
                onClick={() => f.togglePriority(p.value as Priority)}
              >
                {p.label}
              </ToggleChip>
            ))}
          </FilterGroup>

          <FilterGroup label="项目">
            {/* URL 中已选中的已归档项目仍渲染（半透明），否则筛选会静默失效 */}
            {(projects ?? [])
              .filter((p) => !p.archived_at || f.projects?.includes(p.id))
              .map((p) => (
                <ToggleChip
                  key={p.id}
                  active={f.projects?.includes(p.id)}
                  onClick={() => f.toggleProject(p.id)}
                  dotColor={p.color || undefined}
                >
                  {p.name}
                  {p.archived_at ? <span className="opacity-60">（已归档）</span> : null}
                </ToggleChip>
              ))}
            <ToggleChip active={f.no_project} onClick={f.toggleNoProject}>
              无项目
            </ToggleChip>
          </FilterGroup>

          <FilterGroup label="负责人">
            <ToggleChip active={f.unassigned} onClick={f.toggleUnassigned}>
              无人认领
            </ToggleChip>
            {agents.map((a) => (
              <ToggleChip
                key={a.id}
                active={f.assignee === a.id}
                onClick={() => f.setAssignee(f.assignee === a.id ? undefined : a.id)}
                disabled={!a.enabled}
              >
                {a.name}
                {!a.enabled ? <span className="opacity-60">（已禁用）</span> : null}
              </ToggleChip>
            ))}
          </FilterGroup>

          <FilterGroup label="归档">
            {(['exclude', 'only', 'any'] as const).map((m) => (
              <ToggleChip
                key={m}
                active={(f.archived ?? 'exclude') === m}
                onClick={() => f.setArchived(m)}
              >
                {m === 'exclude' ? '不含归档' : m === 'only' ? '只看归档' : '全部'}
              </ToggleChip>
            ))}
          </FilterGroup>


        </div>
      ) : null}
    </div>
  )
}

function FilterGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="w-14 shrink-0 text-xs text-[var(--ink-muted)]">{label}</span>
      {children}
    </div>
  )
}

function ToggleChip({
  active,
  onClick,
  children,
  title,
  dotColor,
  disabled,
}: {
  active?: boolean
  onClick: () => void
  children: React.ReactNode
  title?: string
  dotColor?: string
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-pressed={active}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors',
        'disabled:cursor-not-allowed disabled:opacity-50',
        active
          ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]'
          : 'border-[var(--border)] bg-[var(--surface)] text-[var(--ink-muted)] hover:bg-[var(--surface-hover)]',
      )}
    >
      {dotColor ? (
        <span
          className="h-1.5 w-1.5 rounded-full"
          style={{ background: dotColor }}
          aria-hidden
        />
      ) : null}
      {children}
      {active ? <Check className="h-3 w-3" aria-hidden /> : null}
    </button>
  )
}
