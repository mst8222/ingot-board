/**
 * Agent 管理面板。
 *
 * 危险操作都做成"两步确认"，而不是点一下就删：
 * - 删 Agent 时后端默认拒绝（名下有在办任务），需要 force 才会把任务退回
 *   灵感箱。这个二次确认里必须**说清会退回几个任务**，否则用户是在
 *   不知情的情况下丢弃工作。
 * - token 只在登记/轮换时显示一次，之后无法找回 —— 面板里要写明。
 */

import { useState } from 'react'
import { X, Plus, RefreshCw, Trash2, Power, Copy, Check } from 'lucide-react'
import type { Agent } from '@/api/types'
import { Badge, Button, Input } from '@/components/ui/primitives'
import {
  useDeleteAgent,
  useRegisterAgent,
  useRotateToken,
  useSetAgentEnabled,
  useSetAgentQuota,
  errorHint,
  errorMessage,
} from '@/hooks/useApi'
import { formatRelative } from '@/lib/time'
import { HostsSection } from '@/components/agents/HostsPanel'
import { BalanceSection } from '@/components/agents/BalanceEditor'
import { DispatchSettingsSection } from '@/components/dispatch/DispatchSettingsPanel'

export function AgentsPanel({ agents, onClose }: { agents: Agent[]; onClose: () => void }) {
  const [name, setName] = useState('')
  const kind = 'mcp' // 目前只有 MCP 一种类型；后端 enum 扩展时再改成下拉
  const [newToken, setNewToken] = useState<string | null>(null)
  const [error, setError] = useState<{ msg: string; hint?: string | null } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Agent | null>(null)
  const [copied, setCopied] = useState(false)

  const register = useRegisterAgent()
  const rotate = useRotateToken()
  const setEnabled = useSetAgentEnabled()
  const setQuota = useSetAgentQuota()
  const del = useDeleteAgent()

  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} aria-hidden />

      <aside className="relative flex h-full w-full max-w-md flex-col border-l border-[var(--border)] bg-[var(--surface)]">
        <header className="flex items-center gap-2 border-b border-[var(--border)] px-4 py-3">
          <h2 className="text-sm font-semibold text-[var(--ink)]">Agent 管理</h2>
          <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="关闭">
            <X className="h-4 w-4" aria-hidden />
          </Button>
        </header>

        <div className="scroll-thin flex-1 overflow-y-auto p-4">
          {error ? (
            <div
              role="alert"
              className="mb-3 rounded-md bg-[var(--danger-soft)] px-2.5 py-2 text-xs text-[var(--danger)]"
            >
              <div className="font-medium">{error.msg}</div>
              {error.hint ? <div className="opacity-80">{error.hint}</div> : null}
            </div>
          ) : null}

          {newToken ? (
            <div className="mb-3 rounded-md border border-[var(--warning)] bg-[var(--warning-soft)] p-2.5">
              <p className="mb-1.5 text-xs font-medium text-[var(--warning)]">
                令牌只显示这一次，关闭后无法找回
              </p>
              <div className="flex gap-1.5">
                <code className="min-w-0 flex-1 truncate rounded bg-[var(--surface)] px-2 py-1 font-mono text-xs">
                  {newToken}
                </code>
                <Button
                  size="icon"
                  onClick={() => {
                    void navigator.clipboard?.writeText(newToken)
                    setCopied(true)
                    setTimeout(() => setCopied(false), 1500)
                  }}
                  aria-label="复制令牌"
                >
                  {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                </Button>
              </div>
            </div>
          ) : null}

          <form
            className="mb-4 flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (!name.trim()) return
              register.mutate(
                { name: name.trim(), kind },
                {
                  onSuccess: (d) => {
                    setNewToken(d.token)
                    setName('')
                    setError(null)
                  },
                  onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
                },
              )
            }}
          >
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Agent 名称"
              aria-label="Agent 名称"
            />
            <Button type="submit" variant="primary" disabled={register.isPending || !name.trim()}>
              <Plus className="h-3.5 w-3.5" aria-hidden />
              登记
            </Button>
          </form>

          <ul className="flex flex-col gap-2">
            {agents.length === 0 ? (
              <li className="py-8 text-center text-xs text-[var(--ink-subtle)]">
                还没有登记 Agent
              </li>
            ) : (
              agents.map((a) => (
                <li
                  key={a.id}
                  className="rounded-lg border border-[var(--border)] p-2.5"
                >
                  <div className="flex items-center gap-2">
                    <span
                      className={`h-2 w-2 shrink-0 rounded-full ${
                        a.online ? 'bg-[var(--success)]' : 'bg-[var(--ink-subtle)]'
                      }`}
                      aria-hidden
                    />
                    <span className="min-w-0 flex-1 truncate text-sm font-medium text-[var(--ink)]">
                      {a.name}
                    </span>
                    <Badge>
                      {a.enabled ? (a.online ? '在线' : '离线') : '已禁用'}
                    </Badge>
                  </div>

                  <p className="mt-1 text-xs text-[var(--ink-subtle)]">
                    {a.kind} · {a.serves} · 最后活动 {a.last_seen_at ? formatRelative(a.last_seen_at) : '从未'}
                  </p>
                  <label className="mt-2 flex items-center gap-2 text-xs text-[var(--ink-muted)]">
                    额度
                    <select
                      className="rounded border border-[var(--border)] bg-[var(--surface)] px-1 py-0.5"
                      value={a.quota}
                      onChange={(e) =>
                        setQuota.mutate(
                          { id: a.id, quota: e.target.value as Agent['quota'] },
                          {
                            onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
                          },
                        )
                      }
                    >
                      <option value="unknown">未知</option>
                      <option value="ok">有额度</option>
                      <option value="exhausted">已用尽</option>
                    </select>
                  </label>

                  <BalanceSection agent={a} />

                  <div className="mt-2 flex gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() =>
                        rotate.mutate(a.id, {
                          onSuccess: (d) => setNewToken(d.token),
                          onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
                        })
                      }
                    >
                      <RefreshCw className="h-3 w-3" aria-hidden />
                      轮换令牌
                    </Button>

                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() =>
                        setEnabled.mutate(
                          { id: a.id, enabled: !a.enabled },
                          {
                            onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
                          },
                        )
                      }
                    >
                      <Power className="h-3 w-3" aria-hidden />
                      {a.enabled ? '禁用' : '启用'}
                    </Button>

                    <Button
                      size="sm"
                      variant="ghost"
                      className="ml-auto text-[var(--danger)]"
                      onClick={() => setConfirmDelete(a)}
                    >
                      <Trash2 className="h-3 w-3" aria-hidden />
                      删除
                    </Button>
                  </div>
                </li>
              ))
            )}
          </ul>

          <div className="mt-4">
            <DispatchSettingsSection />
          </div>
          <HostsSection />
        </div>

        {confirmDelete ? (
          <div className="border-t border-[var(--border)] bg-[var(--danger-soft)] p-4">
            <p className="text-sm font-medium text-[var(--danger)]">
              删除「{confirmDelete.name}」？
            </p>
            <p className="mt-1 text-xs text-[var(--ink-muted)]">
              该 Agent 名下的待执行/执行中任务会退回「灵感箱」并清空指派。
              如果只是想临时下线，用「禁用」更合适 —— 禁用会立即让它的令牌失效，
              但保留历史。
            </p>
            <div className="mt-3 flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setConfirmDelete(null)}>
                取消
              </Button>
              <Button
                variant="danger"
                disabled={del.isPending}
                onClick={() =>
                  del.mutate(
                    { id: confirmDelete.id, force: true },
                    {
                      onSuccess: (r) => {
                        setConfirmDelete(null)
                        if (r.reassigned_tasks > 0) {
                          setNewToken(null)
                          setError({
                            msg: `已删除，${r.reassigned_tasks} 个任务已退回灵感箱`,
                            hint: null,
                          })
                        }
                      },
                      onError: (err) => {
                        setConfirmDelete(null)
                        setError({ msg: errorMessage(err), hint: errorHint(err) })
                      },
                    },
                  )
                }
              >
                {del.isPending ? '删除中…' : '确认删除'}
              </Button>
            </div>
          </div>
        ) : null}
      </aside>
    </div>
  )
}
