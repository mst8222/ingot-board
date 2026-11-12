/** 项目选择下拉：选项来自项目列表（不含已归档），含「无项目」空值。 */

import type { Project } from '@/api/types'

export function ProjectField({
  projects,
  value,
  onChange,
  label = '项目',
}: {
  projects: Project[]
  /** null = 无项目 */
  value: number | null
  onChange: (id: number | null) => void
  label?: string
}) {
  // 已归档项目不再可选（但看板筛选仍能保留它们，见 FilterBar）
  const selectable = projects.filter((p) => !p.archived_at)
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs font-medium text-[var(--ink-muted)]">{label}</span>
      <select
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)}
        className="h-8 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
      >
        <option value="">无项目</option>
        {selectable.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </select>
    </label>
  )
}
