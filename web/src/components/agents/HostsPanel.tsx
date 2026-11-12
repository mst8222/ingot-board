/**
 * 支持的执行端列表（027-host-registry）。
 *
 * 展示组件与数据获取刻意分开：列表能用构造数据直接单测，
 * HostsSection 才走 useHosts，不必在测试里搭 QueryClientProvider。
 */

import { useHosts } from '@/hooks/useApi'
import type { HostInfo } from '@/api/types'

export function HostsPanel({
  hosts,
  loading = false,
}: {
  hosts: HostInfo[]
  loading?: boolean
}) {
  return (
    <section aria-label="支持的执行端" className="mt-6 border-t border-[var(--border)] pt-4">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-sm font-semibold text-[var(--ink)]">支持的执行端</h3>
        <span className="text-xs text-[var(--ink-subtle)]">{hosts.length} 个</span>
      </div>
      <p className="mb-3 text-xs text-[var(--ink-subtle)]">
        清单来自后端注册表。装好某个编辑器后重跑 ingot-agent connect，可用状态会即时刷新。
      </p>

      {loading ? <p className="text-xs text-[var(--ink-muted)]">加载中…</p> : null}
      {!loading && hosts.length === 0 ? (
        <p className="text-xs text-[var(--ink-muted)]">暂无执行端清单</p>
      ) : null}

      <ul className="flex flex-col gap-2">
        {hosts.map((h) => (
          <li
            key={h.name}
            data-testid={`host-${h.name}`}
            className="rounded-lg border border-[var(--border)] p-2.5"
          >
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-sm font-medium text-[var(--ink)]">
                {h.display_name}
              </span>
              <span
                className={`shrink-0 rounded px-1.5 py-0.5 text-xs ${
                  h.available
                    ? 'bg-[var(--success-soft)] text-[var(--success)]'
                    : 'bg-[var(--danger-soft)] text-[var(--danger)]'
                }`}
              >
                {h.available ? '可用' : '不可用'}
              </span>
            </div>
            <p className="mt-1 text-xs text-[var(--ink-subtle)]">
              {h.kind} · 档位 {h.serves} · 成本序 {h.cost_rank}
            </p>
            {h.missing_reason ? (
              <p className="mt-1 text-xs text-[var(--danger)]">{h.missing_reason}</p>
            ) : null}
            <p className="mt-1 text-xs text-[var(--ink-muted)]">{h.guide}</p>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** 挂在 Agent 管理面板里的容器：唯一负责取数据的地方。 */
export function HostsSection() {
  const q = useHosts()
  return <HostsPanel hosts={q.data?.items ?? []} loading={q.isLoading} />
}
