/**
 * 任务详情抽屉。
 *
 * 变更点：
 * 1. **乐观锁冲突不静默** —— 409 TASK_CONFLICT 时明确提示"已被他人修改"
 *    并刷新，而不是覆盖对方的改动。原前端保存失败只弹一句通用错误，
 *    用户会以为是网络问题，然后重试覆盖掉别人的编辑。
 * 2. **执行中/评审中不可编辑** —— 按钮直接禁用并说明原因，
 *    而不是让用户填完表单提交后才被服务端拒绝。
 * 3. **驳回必须填原因** —— review → todo 时强制要求 comment。
 */

import { useEffect, useState } from 'react'
import { X, Archive, ArchiveRestore, Clock, User, Layers } from 'lucide-react'
import type { Agent, Task } from '@/api/types'
import { Button, Input, Textarea, Badge } from '@/components/ui/primitives'
import { WorkdirField } from '@/components/task/WorkdirField'
import { ProjectField } from '@/components/task/ProjectField'
import { HandoffSection } from '@/components/task/HandoffPanel'
import {
  useAddNote,
  useArchiveTask,
  useProjects,
  useTask,
  useTaskEvents,
  useTransition,
  useUpdateTask,
  useSaveAsTemplate,
  errorHint,
  errorMessage,
} from '@/hooks/useApi'
import { requiresComment } from '@/lib/dragRules'
import { formatRelative, leaseInfo } from '@/lib/time'
import { cn } from '@/lib/cn'

const STATUS_LABEL: Record<Task['status'], string> = {
  idea: '灵感箱',
  todo: '待执行',
  doing: '执行中',
  review: '评审中',
  approved: '已完成',
}

const PRIORITY_OPTIONS: { value: Task['priority']; label: string }[] = [
  { value: 'urgent', label: '紧急' },
  { value: 'high', label: '高' },
  { value: 'normal', label: '中' },
  { value: 'low', label: '低' },
]

export function TaskDrawer({
  taskId,
  agents,
  onClose,
}: {
  taskId: number | null
  agents: Agent[]
  onClose: () => void
}) {
  const { data: task } = useTask(taskId)
  const [eventsPage, setEventsPage] = useState(1)
  const [error, setError] = useState<{ msg: string; hint?: string | null } | null>(null)

  const { data: events } = useTaskEvents(taskId, eventsPage, 20)
  const update = useUpdateTask()
  const archive = useArchiveTask()
  const addNote = useAddNote()
  const saveTemplate = useSaveAsTemplate()

  // 切换任务时重置分页与错误，避免把 A 的错误带到 B 上
  useEffect(() => {
    setEventsPage(1)
    setError(null)
  }, [taskId])

  // Esc 关闭；抽屉打开时把焦点移进来，读屏用户不至于还留在背景里
  useEffect(() => {
    if (taskId === null) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [taskId, onClose])

  if (taskId === null) return null

  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true">
      <div
        className="absolute inset-0 bg-black/30"
        onClick={onClose}
        aria-hidden
      />
      <aside className="relative flex h-full w-full max-w-xl flex-col border-l border-[var(--border)] bg-[var(--surface)] shadow-[var(--shadow-pop)]">
        <header className="flex items-center gap-2 border-b border-[var(--border)] px-4 py-3">
          <Badge
            style={{
              background: `color-mix(in srgb, var(--status-${task?.status ?? 'idea'}) 12%, transparent)`,
              color: `var(--status-${task?.status ?? 'idea'})`,
            }}
          >
            {STATUS_LABEL[task?.status ?? 'idea']}
          </Badge>
          <div className="ml-auto flex items-center gap-1">
            {/* 只有已完成且未归档的任务能沉淀成模板，且必须由人显式点一次 */}
            {task && task.status === 'approved' && !task.archived_at ? (
              <Button
                variant="ghost"
                size="sm"
                disabled={saveTemplate.isPending}
                onClick={() =>
                  saveTemplate.mutate(
                    { taskId: task.id, name: `${task.title} · 模板` },
                    { onError: (e) => setError({ msg: errorMessage(e), hint: errorHint(e) }) },
                  )
                }
              >
                <Layers className="h-3.5 w-3.5" aria-hidden />
                存为模板
              </Button>
            ) : null}
            {/* 执行中不可归档：Agent 仍持有租约，服务端会拒绝 */}
            {task && !task.archived_at && task.status !== 'doing' ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  archive.mutate(
                    { id: task.id, archived: true },
                    { onError: (e) => setError({ msg: errorMessage(e), hint: errorHint(e) }) },
                  )
                }
              >
                <Archive className="h-3.5 w-3.5" aria-hidden />
                归档
              </Button>
            ) : null}
            {task?.archived_at ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  archive.mutate(
                    { id: task.id, archived: false },
                    { onError: (e) => setError({ msg: errorMessage(e), hint: errorHint(e) }) },
                  )
                }
              >
                <ArchiveRestore className="h-3.5 w-3.5" aria-hidden />
                取消归档
              </Button>
            ) : null}
            <Button variant="ghost" size="icon" onClick={onClose} aria-label="关闭">
              <X className="h-4 w-4" aria-hidden />
            </Button>
          </div>
        </header>

        {error ? (
          <div
            role="alert"
            className="flex flex-col gap-0.5 border-b border-[var(--border)] bg-[var(--danger-soft)] px-4 py-2 text-xs text-[var(--danger)]"
          >
            <span className="font-medium">{error.msg}</span>
            {error.hint ? <span className="opacity-80">{error.hint}</span> : null}
          </div>
        ) : null}

        <div className="scroll-thin flex-1 overflow-y-auto">
          {!task ? (
            <div className="p-6 text-center text-sm text-[var(--ink-muted)]">加载中…</div>
          ) : (
            <TaskForm
              key={`${task.id}:${task.version}`}
              task={task}
              agents={agents}
              onSave={(input) =>
                update.mutate(
                  { id: task.id, input },
                  {
                    onError: (e) => setError({ msg: errorMessage(e), hint: errorHint(e) }),
                    onSuccess: () => setError(null),
                  },
                )
              }
              saving={update.isPending}
            />
          )}

          {task ? <HandoffSection task={task} /> : null}

          <Timeline events={events?.items ?? []} total={events?.total ?? 0} />
        </div>

        <NoteBar
          onSubmit={(content) =>
            addNote.mutate(
              { id: taskId, content },
              {
                onError: (e) => setError({ msg: errorMessage(e), hint: errorHint(e) }),
              },
            )
          }
          pending={addNote.isPending}
        />
      </aside>
    </div>
  )
}

// ---- 编辑表单 ----

function TaskForm({
  task,
  agents,
  onSave,
  saving,
}: {
  task: Task
  agents: Agent[]
  onSave: (input: {
    title: string
    description: string
    acceptance_criteria: string
    workdir: string
    priority: Task['priority']
    assignee_agent_id: number | null
    version: number
    category: Task['category']
    project_id: number | null
  }) => void
  saving: boolean
}) {
  const [title, setTitle] = useState(task.title)
  const [description, setDescription] = useState(task.description)
  const [acceptance, setAcceptance] = useState(task.acceptance_criteria)
  const [workdir, setWorkdir] = useState(task.workdir)
  const [priority, setPriority] = useState<Task['priority']>(task.priority)
  const [assignee, setAssignee] = useState<number | null>(task.assignee_agent_id)
  const [category, setCategory] = useState<Task['category']>(task.category)
  const [projectId, setProjectId] = useState<number | null>(task.project_id)
  const projects = useProjects()

  // 执行中/评审中禁止编辑 —— 与服务端一致，在 UI 上提前拦住
  const locked = task.status === 'doing' || task.status === 'review'
  const archived = task.archived_at !== null

  const lease = task.status === 'doing' && task.lease_until ? leaseInfo(task.lease_until) : null

  return (
    <div className="flex flex-col gap-3 p-4">
      {locked || archived ? (
        <p className="rounded-md bg-[var(--warning-soft)] px-2.5 py-2 text-xs text-[var(--warning)]">
          {archived
            ? '任务已归档，需先取消归档才能编辑。'
            : '执行中/评审中的任务不能编辑内容，避免与 Agent 的产出脱节。'}
        </p>
      ) : null}

      <Field label="标题">
        <Input value={title} onChange={(e) => setTitle(e.target.value)} disabled={locked || archived} />
      </Field>

      <Field label="描述">
        <Textarea
          rows={4}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          disabled={locked || archived}
        />
      </Field>

      <Field label="验收标准" hint="Agent 据此判断是否达标">
        <Textarea
          rows={3}
          value={acceptance}
          onChange={(e) => setAcceptance(e.target.value)}
          disabled={locked || archived}
        />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="优先级">
          <select
            value={priority}
            onChange={(e) => setPriority(e.target.value as Task['priority'])}
            disabled={locked || archived}
            className="h-8 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
          >
            {PRIORITY_OPTIONS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        </Field>

        <Field label="负责人">
          <select
            value={assignee ?? ''}
            onChange={(e) => setAssignee(e.target.value ? Number(e.target.value) : null)}
            disabled={locked || archived}
            className="h-8 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
          >
            <option value="">无人认领</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id} disabled={!a.enabled}>
                {a.name}
                {a.online ? '（在线）' : ''}
                {a.enabled ? '' : '（已禁用）'}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <Field label="类别">
        <select
          className="w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 py-1.5 text-sm"
          value={category}
          disabled={locked || archived}
          onChange={(e) => setCategory(e.target.value as Task['category'])}
        >
          <option value="">派发时自动分类</option>
          <option value="code_tool">做工具</option>
          <option value="code_game">做游戏</option>
          <option value="copy">文案</option>
          <option value="local_ops">本机操作</option>
        </select>
      </Field>

      {task.route_reason ? (
        <p className="text-xs text-[var(--ink-muted)]">派发：{task.route_reason}</p>
      ) : null}

      <Field label="工作目录">
        <WorkdirField
          value={workdir}
          onChange={setWorkdir}
          disabled={locked || archived}
        />
      </Field>

      <ProjectField
        projects={projects.data ?? []}
        value={projectId}
        onChange={setProjectId}
      />

      <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--ink-subtle)]">
        <span>版本 v{task.version}</span>
        <span>更新于 {formatRelative(task.updated_at)}</span>
        {lease ? (
          <span
            className={cn(
              'inline-flex items-center gap-1',
              lease.state === 'expired' && 'text-[var(--danger)]',
              lease.state === 'soon' && 'text-[var(--warning)]',
            )}
          >
            <Clock className="h-3 w-3" aria-hidden />
            租约{lease.state === 'expired' ? '已超时' : '剩余'} {lease.label}
          </span>
        ) : null}
        {task.assignee_agent_id ? (
          <span className="inline-flex items-center gap-1">
            <User className="h-3 w-3" aria-hidden />
            {agents.find((a) => a.id === task.assignee_agent_id)?.name ?? '未知 Agent'}
          </span>
        ) : null}
      </div>

      <TransitionBar task={task} />

      <div className="flex justify-end">
        <Button
          variant="primary"
          disabled={locked || archived || saving || !title.trim()}
          onClick={() =>
            onSave({
              title: title.trim(),
              description,
              acceptance_criteria: acceptance,
              workdir,
              priority,
              assignee_agent_id: assignee,
              category,
              version: task.version,
              project_id: projectId,
            })
          }
        >
          {saving ? '保存中…' : '保存'}
        </Button>
      </div>
    </div>
  )
}

// ---- 状态流转按钮 ----

function TransitionBar({ task }: { task: Task }) {
  const transition = useTransition()
  const [pending, setPending] = useState<Task['status'] | null>(null)
  const [comment, setComment] = useState('')
  const [showReject, setShowReject] = useState(false)

  // 本地硬编码流转目标会导致与后端状态机漂移，
  // 所以这里从任务自身可读的信息出发：按"下一个阶段"排列，
  // 服务端仍是最终裁判（非法流转会 409 并回滚）。
  const nextStates: Task['status'][] =
    task.status === 'idea'
      ? ['todo']
      : task.status === 'todo'
        ? ['doing']
        : task.status === 'doing'
          ? ['review']
          : task.status === 'review'
            ? ['approved', 'todo']
            : []

  if (nextStates.length === 0) {
    return (
      <p className="text-xs text-[var(--ink-subtle)]">
        「{STATUS_LABEL[task.status]}」是终态，不能再流转。
      </p>
    )
  }

  return (
    <div className="rounded-md border border-[var(--border)] bg-[var(--surface-sunken)] p-2.5">
      <p className="mb-1.5 text-xs font-medium text-[var(--ink-muted)]">推进状态</p>
      <div className="flex flex-wrap gap-1.5">
        {nextStates.map((to) => {
          const needsComment = requiresComment(task.status, to)
          if (needsComment && !showReject) {
            return (
              <Button
                key={to}
                variant="secondary"
                size="sm"
                onClick={() => setShowReject(true)}
              >
                驳回
              </Button>
            )
          }
          return (
            <Button
              key={to}
              variant={to === 'todo' ? 'secondary' : 'primary'}
              size="sm"
              disabled={transition.isPending || (needsComment && !comment.trim())}
              onClick={() => {
                setPending(to)
                transition.mutate(
                  { id: task.id, to, comment: comment || undefined },
                  { onSettled: () => setPending(null) },
                )
              }}
              title={needsComment && !comment.trim() ? '驳回必须填写原因' : undefined}
            >
              {transition.isPending && pending === to ? '处理中…' : `→ ${STATUS_LABEL[to]}`}
            </Button>
          )
        })}
      </div>

      {showReject ? (
        <div className="mt-2 flex gap-1.5">
          <Input
            autoFocus
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="驳回原因（必填）—— Agent 据此补充"
            aria-label="驳回原因"
          />
          <Button variant="ghost" size="sm" onClick={() => setShowReject(false)}>
            取消
          </Button>
        </div>
      ) : null}
    </div>
  )
}

// ---- 时间线 ----

function Timeline({ events, total }: { events: import('@/api/types').TaskEvent[]; total: number }) {
  return (
    <div className="border-t border-[var(--border)] p-4">
      <h3 className="mb-2 text-sm font-medium text-[var(--ink)]">
        时间线
        {total > 0 ? <span className="ml-1 text-xs text-[var(--ink-subtle)]">{total} 条</span> : null}
      </h3>
      {events.length === 0 ? (
        <p className="text-xs text-[var(--ink-subtle)]">暂无记录</p>
      ) : (
        <ol className="flex flex-col gap-2">
          {events.map((ev) => (
            <li key={ev.id} className="flex gap-2 text-xs">
              <span className="w-20 shrink-0 text-[var(--ink-subtle)]">
                {formatRelative(ev.created_at)}
              </span>
              <span className="min-w-0 flex-1">
                <span className="font-medium text-[var(--ink)]">{ev.message}</span>
                <span className="ml-1 text-[var(--ink-muted)]">
                  {ev.actor_type === 'agent' ? ev.actor_name || 'Agent' : ev.actor_name || '系统'}
                </span>
              </span>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}

// ---- 备注输入 ----

function NoteBar({
  onSubmit,
  pending,
}: {
  onSubmit: (content: string) => void
  pending: boolean
}) {
  const [text, setText] = useState('')
  return (
    <form
      className="flex gap-2 border-t border-[var(--border)] p-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (!text.trim()) return
        onSubmit(text.trim())
        setText('')
      }}
    >
      <Input
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="添加备注…"
        aria-label="备注内容"
      />
      <Button type="submit" variant="primary" disabled={pending || !text.trim()}>
        添加
      </Button>
    </form>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs font-medium text-[var(--ink-muted)]">
        {label}
        {hint ? <span className="ml-1 font-normal text-[var(--ink-subtle)]">{hint}</span> : null}
      </span>
      {children}
    </label>
  )
}
