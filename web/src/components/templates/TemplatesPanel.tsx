/**
 * 029 任务模板面板。
 *
 * 模板只是「派发期素材」：点实例化会新建一条 idea 任务，再照常走
 * routeIdea 与状态机，不绕过任何守卫。纯组件 + 容器拆分便于单测。
 */

import { X, Play, Trash2 } from 'lucide-react'
import type { Template } from '@/api/types'
import { Badge, Button } from '@/components/ui/primitives'
import {
  useDeleteTemplate,
  useInstantiateTemplate,
  useTemplates,
} from '@/hooks/useApi'

export interface TemplatesPanelProps {
  templates: Template[]
  onInstantiate: (id: number) => void
  onDelete: (id: number) => void
  onClose: () => void
  pending?: boolean
}

export function TemplatesPanel({
  templates,
  onInstantiate,
  onDelete,
  onClose,
  pending = false,
}: TemplatesPanelProps) {
  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} aria-hidden />
      <aside className="relative flex h-full w-full max-w-md flex-col border-l border-[var(--border)] bg-[var(--surface)]">
        <header className="flex items-center gap-2 border-b border-[var(--border)] px-4 py-3">
          <h2 className="text-sm font-semibold text-[var(--ink)]">任务模板</h2>
          <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="关闭">
            <X className="h-4 w-4" aria-hidden />
          </Button>
        </header>
        <div className="scroll-thin flex-1 overflow-y-auto p-4">
          <p className="mb-3 text-xs text-[var(--ink-subtle)]">
            把「已完成」的任务在抽屉里存为模板；实例化会新建灵感箱任务并按总管双模派发。
          </p>
          {templates.length === 0 ? (
            <p className="py-8 text-center text-xs text-[var(--ink-subtle)]">还没有模板</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {templates.map((t) => (
                <li key={t.id} className="rounded-lg border border-[var(--border)] p-2.5">
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate text-sm font-medium text-[var(--ink)]">
                      {t.name}
                    </span>
                    {t.archived_at ? <Badge>已归档</Badge> : null}
                  </div>
                  <p className="mt-1 text-xs text-[var(--ink-subtle)]">
                    {t.title} · {t.category || '未分类'} · {t.assignee_agent_id ? '已绑定执行端' : '由总管定人'}
                  </p>
                  <div className="mt-2 flex gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={pending || Boolean(t.archived_at)}
                      onClick={() => onInstantiate(t.id)}
                    >
                      <Play className="h-3 w-3" aria-hidden />
                      实例化
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="ml-auto text-[var(--danger)]"
                      disabled={pending}
                      onClick={() => onDelete(t.id)}
                    >
                      <Trash2 className="h-3 w-3" aria-hidden />
                      删除
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </aside>
    </div>
  )
}

/** 容器：接 React Query，供顶栏「模板」按钮打开。 */
export function TemplatesDialog({ onClose }: { onClose: () => void }) {
  const list = useTemplates()
  const instantiate = useInstantiateTemplate()
  const del = useDeleteTemplate()
  return (
    <TemplatesPanel
      templates={list.data?.items ?? []}
      pending={instantiate.isPending || del.isPending}
      onInstantiate={(id) => instantiate.mutate(id)}
      onDelete={(id) => del.mutate(id)}
      onClose={onClose}
    />
  )
}
