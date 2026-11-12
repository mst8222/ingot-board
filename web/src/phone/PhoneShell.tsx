import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import type { Agent, Task, TaskStatus } from '@/api/types'
import { Button, Input, Textarea } from '@/components/ui/primitives'
import {
  errorMessage,
  useAddNote,
  useAgents,
  useArchiveTask,
  useClockSync,
  useCreateTask,
  usePairDevice,
  useTask,
  useTaskEvents,
  useTasks,
  useTransition,
  useWorkdirs,
} from '@/hooks/useApi'
import { useStreamBridge } from '@/hooks/useStreamBridge'
import { PhoneActions } from './PhoneActions'
import { enableReviewPush } from './reviewPush'

type Tab = 'active' | 'review' | 'new' | 'done' | 'agents'

const STATUS_LABEL: Record<TaskStatus, string> = {
  idea: '灵感',
  todo: '待执行',
  doing: '执行中',
  review: '待评审',
  approved: '已完成',
}

function listFilter(status: TaskStatus[]) {
  return { status, archived: 'exclude' as const, sort: 'manual' as const, page: 1, page_size: 100 }
}

declare global {
  interface Window {
    AgentTodoAndroid?: { onPaired: (token: string) => void }
  }
}

export function PhoneShell() {
  useClockSync()
  useStreamBridge()
  const agents = useAgents()
  const probe = useTasks(listFilter(['idea', 'todo', 'doing', 'review', 'approved']))
  const pair = usePairDevice()
  const [tab, setTab] = useState<Tab>('active')
  const [taskId, setTaskId] = useState<number | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const code = new URLSearchParams(window.location.search).get('code')
  const pairStarted = useRef(false)

  useEffect(() => {
    if (!code || pairStarted.current) return
    pairStarted.current = true
    pair.mutate(code, {
      onSuccess: (res) => {
        window.AgentTodoAndroid?.onPaired(res.token)
        const url = new URL(window.location.href)
        url.searchParams.delete('code')
        window.history.replaceState(null, '', url.pathname + url.search + url.hash)
      },
    })
  }, [code, pair])

  const blocked = probe.error instanceof ApiError && probe.error.status === 401
  if (code && !pair.isSuccess) {
    return (
      <Gate
        title={pair.isError ? errorMessage(pair.error) : '正在配对…'}
        detail="配对成功后即可下达任务。"
      />
    )
  }
  if (blocked && !code) {
    return <Gate title="请用桌面上的二维码打开" detail="手机不保存任务，需要先和这台电脑配对。" />
  }

  const agentItems = agents.data?.items ?? []
  return (
    <div className="flex h-full flex-col" data-testid="phone-shell">
      <header className="flex items-center gap-2 border-b border-[var(--border)] px-3 py-2 pt-[max(0.5rem,env(safe-area-inset-top))]">
        <h1 className="text-sm font-semibold">Ingot Board</h1>
        <Button variant="ghost" className="ml-auto h-11 min-h-11" onClick={() => void notify(setNotice)}>
          通知
        </Button>
      </header>
      {notice ? (
        <p className="bg-[var(--surface-hover)] px-3 py-2 text-xs text-[var(--ink-muted)]" role="status">
          {notice}
        </p>
      ) : null}
      <main className="min-h-0 flex-1 overflow-y-auto">
        {taskId !== null ? (
          <PhoneTask taskId={taskId} agents={agentItems} onBack={() => setTaskId(null)} />
        ) : tab === 'new' ? (
          <PhoneNew agents={agentItems} onCreated={(id) => { setTaskId(id); setTab('active') }} />
        ) : tab === 'agents' ? (
          <PhoneAgentList agents={agentItems} />
        ) : (
          <PhoneList tab={tab} agents={agentItems} onOpen={setTaskId} />
        )}
      </main>
      {taskId === null ? (
        <nav className="grid grid-cols-5 border-t border-[var(--border)] bg-[var(--surface)] pb-[env(safe-area-inset-bottom)]">
          <TabButton current={tab} id="active" label="进行中" onSelect={setTab} />
          <TabButton current={tab} id="review" label="待处理" onSelect={setTab} />
          <TabButton current={tab} id="new" label="新建" onSelect={setTab} />
          <TabButton current={tab} id="done" label="结果" onSelect={setTab} />
          <TabButton current={tab} id="agents" label="Agent" onSelect={setTab} />
        </nav>
      ) : null}
    </div>
  )
}

function Gate({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center" data-testid="phone-shell">
      <h1 className="text-base font-semibold">{title}</h1>
      <p className="text-sm text-[var(--ink-muted)]">{detail}</p>
    </div>
  )
}

async function notify(setNotice: (value: string) => void) {
  try {
    await enableReviewPush()
    setNotice('已开启待评审通知')
  } catch (e) {
    setNotice(errorMessage(e))
  }
}

function TabButton({
  current,
  id,
  label,
  onSelect,
}: {
  current: Tab
  id: Tab
  label: string
  onSelect: (id: Tab) => void
}) {
  const on = current === id
  return (
    <button
      type="button"
      className={`min-h-11 text-xs ${on ? 'font-semibold text-[var(--accent)]' : 'text-[var(--ink-muted)]'}`}
      onClick={() => onSelect(id)}
    >
      {label}
    </button>
  )
}

function PhoneList({
  tab,
  agents,
  onOpen,
}: {
  tab: Tab
  agents: Agent[]
  onOpen: (id: number) => void
}) {
  const status: TaskStatus[] =
    tab === 'review' ? ['review'] : tab === 'done' ? ['approved'] : ['idea', 'todo', 'doing']
  const q = useTasks(listFilter(status))
  const byId = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents])
  if (q.isLoading) return <p className="p-4 text-sm text-[var(--ink-muted)]">加载中…</p>
  if (q.isError) return <p className="p-4 text-sm text-[var(--danger)]">{errorMessage(q.error)}</p>
  const items = q.data?.items ?? []
  if (items.length === 0) return <p className="p-4 text-sm text-[var(--ink-muted)]">这里还没有任务</p>
  return (
    <ul className="flex flex-col">
      {items.map((task) => {
        const agent = task.assignee_agent_id ? byId.get(task.assignee_agent_id) : undefined
        const waiting = task.status === 'todo' && agent && !agent.online
        return (
          <li key={task.id} className="border-b border-[var(--border)]">
            <button
              type="button"
              className="flex min-h-11 w-full flex-col items-start gap-1 px-3 py-3 text-left"
              onClick={() => onOpen(task.id)}
            >
              <span className="text-sm font-medium">{task.title}</span>
              <span className="text-xs text-[var(--ink-muted)]">
                {STATUS_LABEL[task.status]}
                {agent ? ` · ${agent.name}` : ''}
                {waiting ? ` · 等待本机 ${agent.name} 上线后领取` : ''}
              </span>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

function PhoneTask({
  taskId,
  onBack,
}: {
  taskId: number
  agents: Agent[]
  onBack: () => void
}) {
  const task = useTask(taskId)
  const events = useTaskEvents(taskId, 1, 30)
  const transition = useTransition()
  const note = useAddNote()
  const archive = useArchiveTask()
  const [comment, setComment] = useState('')
  const [error, setError] = useState<string | null>(null)
  const current = task.data
  return (
    <div className="flex h-full flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        <Button variant="ghost" className="h-11 min-h-11" onClick={onBack}>
          返回
        </Button>
        {current ? (
          <>
            <h2 className="mt-2 text-base font-semibold">{current.title}</h2>
            <p className="mt-1 text-xs text-[var(--ink-muted)]">
              {STATUS_LABEL[current.status]}
              {current.workdir ? ` · ${current.workdir}` : ''}
            </p>
            {current.description ? <p className="mt-3 whitespace-pre-wrap text-sm">{current.description}</p> : null}
            {current.acceptance_criteria ? (
              <p className="mt-3 whitespace-pre-wrap text-sm text-[var(--ink-muted)]">{current.acceptance_criteria}</p>
            ) : null}
            <ul className="mt-4 flex flex-col gap-2">
              {(events.data?.items ?? []).map((ev) => (
                <li key={ev.id} className="text-xs text-[var(--ink-muted)]">
                  <span className="text-[var(--ink)]">{ev.actor_name}</span> {ev.message}
                </li>
              ))}
            </ul>
            {current.status === 'approved' ? (
              <Button
                className="mt-4 h-11 min-h-11"
                onClick={() => archive.mutate({ id: current.id, archived: !current.archived_at })}
              >
                {current.archived_at ? '取消归档' : '归档'}
              </Button>
            ) : null}
            {error ? <p className="mt-3 text-sm text-[var(--danger)]">{error}</p> : null}
          </>
        ) : (
          <p className="text-sm text-[var(--ink-muted)]">加载中…</p>
        )}
      </div>
      {current ? (
        <PhoneActions
          status={current.status}
          comment={comment}
          pending={transition.isPending || note.isPending}
          onComment={setComment}
          onNote={() => {
            note.mutate(
              { id: current.id, content: comment.trim() },
              {
                onSuccess: () => {
                  setComment('')
                  setError(null)
                },
                onError: (e) => setError(errorMessage(e)),
              },
            )
          }}
          onTransition={(to) => {
            transition.mutate(
              { id: current.id, to, comment: comment.trim() || undefined },
              {
                onSuccess: () => {
                  setComment('')
                  setError(null)
                },
                onError: (e) => setError(errorMessage(e)),
              },
            )
          }}
        />
      ) : null}
    </div>
  )
}

function PhoneNew({ agents, onCreated }: { agents: Agent[]; onCreated: (id: number) => void }) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [workdir, setWorkdir] = useState('')
  const [assignee, setAssignee] = useState<number | ''>('')
  const [error, setError] = useState<string | null>(null)
  const dirs = useWorkdirs()
  const create = useCreateTask()
  const transition = useTransition()

  const submit = (dispatch: boolean) => {
    if (!title.trim()) return
    create.mutate(
      {
        title: title.trim(),
        description,
        workdir,
        priority: 'normal',
        assignee_agent_id: assignee === '' ? null : assignee,
      },
      {
        onSuccess: (task: Task) => {
          if (!dispatch) {
            onCreated(task.id)
            return
          }
          transition.mutate(
            { id: task.id, to: 'todo' },
            {
              onSuccess: () => onCreated(task.id),
              onError: (e) => {
                setError(errorMessage(e))
                onCreated(task.id)
              },
            },
          )
        },
        onError: (e) => setError(errorMessage(e)),
      },
    )
  }

  return (
    <form
      className="flex flex-col gap-3 p-3"
      onSubmit={(e) => {
        e.preventDefault()
        submit(true)
      }}
    >
      <label className="flex flex-col gap-1 text-xs">
        标题
        <Input className="h-11 min-h-11" value={title} onChange={(e) => setTitle(e.target.value)} required />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        描述
        <Textarea rows={4} value={description} onChange={(e) => setDescription(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        工作目录
        <Input
          className="h-11 min-h-11"
          list="phone-workdirs"
          value={workdir}
          onChange={(e) => setWorkdir(e.target.value)}
          placeholder="桌面机器上的绝对路径"
        />
        <datalist id="phone-workdirs">
          {(dirs.data?.items ?? []).map((ref) => (
            <option key={ref.workdir} value={ref.workdir} />
          ))}
        </datalist>
      </label>
      <label className="flex flex-col gap-1 text-xs">
        Agent
        <select
          className="min-h-11 rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
          value={assignee}
          onChange={(e) => setAssignee(e.target.value === '' ? '' : Number(e.target.value))}
        >
          <option value="">交给现有路由</option>
          {agents
            .filter((a) => a.enabled)
            .map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
                {a.online ? '' : '（离线）'}
              </option>
            ))}
        </select>
      </label>
      {error ? <p className="text-sm text-[var(--danger)]">{error}</p> : null}
      <Button type="submit" variant="primary" className="h-11 min-h-11" disabled={create.isPending || transition.isPending}>
        创建并派发
      </Button>
      <Button className="h-11 min-h-11" disabled={create.isPending} onClick={() => submit(false)}>
        只记为灵感
      </Button>
    </form>
  )
}

function PhoneAgentList({ agents }: { agents: Agent[] }) {
  if (agents.length === 0) return <p className="p-4 text-sm text-[var(--ink-muted)]">还没有登记 Agent</p>
  return (
    <ul className="flex flex-col">
      {agents.map((a) => (
        <li key={a.id} className="border-b border-[var(--border)] px-3 py-3">
          <p className="text-sm font-medium">{a.name}</p>
          <p className="text-xs text-[var(--ink-muted)]">
            {a.kind} · {a.online ? '在线' : '离线'} · 额度 {a.quota}
          </p>
        </li>
      ))}
    </ul>
  )
}
