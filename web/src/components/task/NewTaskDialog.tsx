/** 新建任务对话框。 */

import { useState } from 'react'
import { X } from 'lucide-react'
import type { Agent, Task } from '@/api/types'
import { Button, Input, Textarea } from '@/components/ui/primitives'
import { WorkdirField } from '@/components/task/WorkdirField'
import { ProjectField } from '@/components/task/ProjectField'
import { useCreateTask, useProjects, errorHint, errorMessage } from '@/hooks/useApi'

export function NewTaskDialog({
  agents,
  onClose,
  onCreated,
}: {
  agents: Agent[]
  onClose: () => void
  onCreated: (id: number) => void
}) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [acceptance, setAcceptance] = useState('')
  const [workdir, setWorkdir] = useState('')
  const [priority, setPriority] = useState<Task['priority']>('normal')
  const [assignee, setAssignee] = useState<number | null>(null)
  const [projectId, setProjectId] = useState<number | null>(null)
  const [error, setError] = useState<{ msg: string; hint?: string | null } | null>(null)

  const create = useCreateTask()
  const projects = useProjects()

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} aria-hidden />

      <form
        className="relative flex max-h-[90vh] w-full max-w-lg flex-col gap-3 overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-[var(--shadow-pop)]"
        onSubmit={(e) => {
          e.preventDefault()
          if (!title.trim()) return
          create.mutate(
            {
              title: title.trim(),
              description,
              acceptance_criteria: acceptance,
              workdir,
              priority,
              assignee_agent_id: assignee,
              project_id: projectId,
            },
            {
              onSuccess: (task) => onCreated(task.id),
              onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
            },
          )
        }}
      >
        <div className="flex items-center">
          <h2 className="text-sm font-semibold text-[var(--ink)]">新建任务</h2>
          <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="关闭">
            <X className="h-4 w-4" aria-hidden />
          </Button>
        </div>

        {error ? (
          <div role="alert" className="rounded-md bg-[var(--danger-soft)] px-2.5 py-2 text-xs text-[var(--danger)]">
            <div className="font-medium">{error.msg}</div>
            {error.hint ? <div className="opacity-80">{error.hint}</div> : null}
          </div>
        ) : null}

        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-[var(--ink-muted)]">标题</span>
          <Input
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="要做什么？"
            required
          />
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-[var(--ink-muted)]">描述</span>
          <Textarea rows={3} value={description} onChange={(e) => setDescription(e.target.value)} />
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-[var(--ink-muted)]">
            验收标准
            <span className="ml-1 font-normal text-[var(--ink-subtle)]">
              Agent 据此判断是否达标，写清楚能显著减少来回沟通
            </span>
          </span>
          <Textarea rows={3} value={acceptance} onChange={(e) => setAcceptance(e.target.value)} />
        </label>

        <div className="grid grid-cols-2 gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-[var(--ink-muted)]">优先级</span>
            <select
              value={priority}
              onChange={(e) => setPriority(e.target.value as Task['priority'])}
              className="h-8 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
            >
              <option value="urgent">紧急</option>
              <option value="high">高</option>
              <option value="normal">中</option>
              <option value="low">低</option>
            </select>
          </label>

          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-[var(--ink-muted)]">负责人</span>
            <select
              value={assignee ?? ''}
              onChange={(e) => setAssignee(e.target.value ? Number(e.target.value) : null)}
              className="h-8 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
            >
              <option value="">暂不指派</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id} disabled={!a.enabled}>
                  {a.name}
                  {a.online ? '（在线）' : ''}
                  {a.enabled ? '' : '（已禁用）'}
                </option>
              ))}
            </select>
          </label>
        </div>

        <div className="flex flex-col gap-1">
          <span className="text-xs font-medium text-[var(--ink-muted)]">工作目录</span>
          <WorkdirField value={workdir} onChange={setWorkdir} />
        </div>

        <ProjectField projects={projects.data ?? []} value={projectId} onChange={setProjectId} />

        <p className="text-xs text-[var(--ink-subtle)]">
          新任务会创建在「灵感箱」，派发后 Agent 才能领取。
        </p>

        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" variant="primary" disabled={create.isPending || !title.trim()}>
            {create.isPending ? '创建中…' : '创建'}
          </Button>
        </div>
      </form>
    </div>
  )
}
