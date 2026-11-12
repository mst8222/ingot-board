/**
 * 应用外壳：顶栏 + 筛选栏 + 看板 + 抽屉。
 *
 * 布局决策：
 * - 顶栏常驻，含连接状态与在线 Agent 数 —— Ingot Board 是"人和 Agent
 *   协作"的工具，Agent 是否在线是核心信息，不该藏在二级页面。
 * - 窄屏：看板不横向滚动，按宽度只显示连续的 1-4 列，顶部页签切换（见 Board）。
 *   原前端在窄屏下把 5 列压成 1 列且列高受限，用户要上下+左右反复滚动才能看完。
 * - 连接状态用颜色 + 文字双重表达，不只靠颜色（色觉障碍用户）。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Moon, Sun, Plus, RefreshCw, Smartphone, Users, WifiOff, Check, FolderKanban, Layers, CalendarClock } from 'lucide-react'
import { Board } from '@/components/board/Board'
import { FilterBar } from '@/components/board/FilterBar'
import { TaskDrawer } from '@/components/task/TaskDrawer'
import { AgentsPanel } from '@/components/agents/AgentsPanel'
import { PhoneAccessPanel } from '@/components/phone/PhoneAccessPanel'
import { NewTaskDialog } from '@/components/task/NewTaskDialog'
import { ProjectsDialog } from '@/components/projects/ProjectsDialog'
import { TemplatesDialog } from '@/components/templates/TemplatesPanel'
import { SchedulesDialog } from '@/components/schedules/SchedulesPanel'
import { PhoneShell } from '@/phone/PhoneShell'
import { usePhoneShell } from '@/phone/usePhoneShell'
import { Button, Spinner } from '@/components/ui/primitives'
import { useAgents, useBoard, useMeta, useMoveTask, useProjects, useTransition } from '@/hooks/useApi'
import { useClockSync } from '@/hooks/useApi'
import { useStreamBridge } from '@/hooks/useStreamBridge'
import { useFilters, isFiltered } from '@/store/filters'
import { useTheme } from '@/store/theme'
import { applyOptimisticMove, revertMove, type DropPlan } from '@/lib/dragRules'
import type { Agent, Board as BoardData, Project, Task, TaskStatus } from '@/api/types'
import { columnsByStatus, qk } from '@/store/query'
import { useQueryClient } from '@tanstack/react-query'

/** 折叠状态持久化的 localStorage 键。 */
const COLLAPSE_KEY = 'ingot.collapsed'

/**
 * 折叠状态，持久化到 localStorage。
 *
 * ⚠️ 默认值来自服务端 meta（approved 应默认折叠），而 meta 是**异步**到达的。
 * 若把 defaults 只放进 useState 的初始化函数，首屏时 defaults 还是空对象，
 * 之后 meta 到了也不会补上 —— 结果就是"已完成"列永远不折叠。
 *
 * 正确做法：用 ref 记住"默认值已经同步过"，在 defaults 到达后补齐一次，
 * 但不覆盖用户已经手动改过的项（localStorage 里的值优先）。
 */
function useCollapsed(defaults: Record<string, boolean>) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try {
      const raw = localStorage.getItem(COLLAPSE_KEY)
      if (raw) return JSON.parse(raw) as Record<string, boolean>
    } catch {
      /* 解析失败用默认值 */
    }
    return {}
  })

  const synced = useRef(false)
  useEffect(() => {
    // 等 meta 真正到达再同步（空对象说明还没加载完）
    if (synced.current || Object.keys(defaults).length === 0) return
    synced.current = true
    setCollapsed((prev) => {
      const next = { ...defaults }
      // 用户显式改过的优先，不被默认值覆盖
      for (const [k, v] of Object.entries(prev)) {
        if (typeof v === 'boolean') next[k] = v
      }
      return next
    })
  }, [defaults])

  const toggle = useCallback((status: TaskStatus) => {
    setCollapsed((prev) => {
      const next = { ...prev, [status]: !prev[status] }
      try {
        localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next))
      } catch {
        /* 隐私模式下写不进去，不影响功能 */
      }
      return next
    })
  }, [])

  return [collapsed, toggle] as const
}

export default function App() {
  const phone = usePhoneShell()
  if (phone) return <PhoneShell />
  return <DesktopApp />
}

function DesktopApp() {
  useClockSync()
  const streamStatus = useStreamBridge()
  const qc = useQueryClient()

  const filters = useFilters()
  const theme = useTheme()

  const meta = useMeta()
  const agents = useAgents()
  const projects = useProjects()
  const board = useBoard(filters)

  const [openTask, setOpenTask] = useState<number | null>(null)
  const [showAgents, setShowAgents] = useState(false)
  const [showPhone, setShowPhone] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [showProjects, setShowProjects] = useState(false)
  const [showTemplates, setShowTemplates] = useState(false)
  const [showSchedules, setShowSchedules] = useState(false)
  const [toast, setToast] = useState<{ msg: string; kind: 'error' | 'info' } | null>(null)

  // 默认折叠由服务端 meta 决定（approved 折叠），避免前端硬编码
  const defaultCollapsed = useMemo(() => {
    const out: Record<string, boolean> = {}
    for (const s of meta.data?.statuses ?? []) {
      out[s.value] = s.collapsed_by_default
    }
    return out
  }, [meta.data])

  const [collapsed, toggleCollapsed] = useCollapsed(defaultCollapsed)

  const agentMap = useMemo(() => {
    const m = new Map<number, Agent>()
    for (const a of agents.data?.items ?? []) m.set(a.id, a)
    return m
  }, [agents.data])

  const projectMap = useMemo(() => {
    const m = new Map<number, Project>()
    for (const p of projects.data ?? []) m.set(p.id, p)
    return m
  }, [projects.data])

  const onlineCount = useMemo(
    () => (agents.data?.items ?? []).filter((a) => a.online && a.enabled).length,
    [agents.data],
  )

  // 提示 4 秒后自动消失；换一条消息会重置计时
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const transition = useTransition()
  const move = useMoveTask()

  /**
   * 拖拽落位：乐观更新 → 请求 → 失败回滚。
   *
   * 回滚必须从**所有**列里摘掉这条任务再插回原位，
   * 否则服务端拒绝后同一条任务会在看板上出现两次。
   */
  const onTransition = useCallback(
    async (plan: DropPlan, original: Task, originalIndex: number) => {
      const key = qk.board(filters)
      const prev = qc.getQueryData<BoardData>(key)
      const detailKey = qk.task(plan.taskId)

      // 1. 乐观更新
      if (prev) {
        const asColumns = columnsByStatus(prev.columns)
        qc.setQueryData(key, {
          ...prev,
          columns: applyOptimisticMove(asColumns, plan),
        })
      }

      // 2. 请求服务端
      try {
        if (plan.from === plan.to) {
          // 同列重排：不改状态，走独立的 move 端点。
          //
          // 以前这里是 `return` —— 只改本地视图、刷新即丢失，
          // 而且没有任何提示，用户以为存上了。SQL 里补了 sort_order
          // 之后才真正持久化（见 migrations/0003）。
          //
          // version 用乐观更新**之前**的原值：move 会 bump 它，
          // 带上乐观更新后的值会自己跟自己冲突。
          await move.mutateAsync({
            id: plan.taskId,
            toIndex: plan.toIndex ?? 0,
            version: original.version,
          })
          return
        }
        await transition.mutateAsync({
          id: plan.taskId,
          to: plan.to,
          toIndex: plan.toIndex ?? undefined,
        })
      } catch (e) {
        // 3. 回滚
        if (prev) {
          qc.setQueryData(key, {
            ...prev,
            columns: revertMove(columnsByStatus(prev.columns), plan.taskId, original, originalIndex),
          })
        }
        qc.setQueryData(detailKey, original)
        void qc.invalidateQueries({ queryKey: key })

        const err = e as { code?: string; message?: string }
        setToast({
          kind: 'error',
          msg:
            err.code === 'INVALID_TRANSITION'
              ? `「${original.title}」不能直接移动到该状态`
              : (err.message ?? '操作失败，已恢复原状'),
        })
      }
    },
    [filters, qc, transition, move],
  )

  return (
    <div className="flex h-full flex-col">
      <TopBar
        onlineCount={onlineCount}
        totalAgents={agents.data?.items.length ?? 0}
        streamState={streamStatus.state}
        onToggleTheme={theme.toggle}
        onShowAgents={() => setShowAgents(true)}
        onShowPhone={() => setShowPhone(true)}
        onShowProjects={() => setShowProjects(true)}
        onShowTemplates={() => setShowTemplates(true)}
        onShowSchedules={() => setShowSchedules(true)}
        onNewTask={() => setShowNew(true)}
      />

      <div className="border-b border-[var(--border)] bg-[var(--surface)] px-3 py-2">
        <FilterBar
          meta={meta.data}
          agents={agents.data?.items ?? []}
          projects={projects.data ?? []}
        />
      </div>

      <main className="min-h-0 flex-1">
        {board.isLoading ? (
          <div className="flex h-full items-center justify-center gap-2 text-sm text-[var(--ink-muted)]">
            <Spinner />
            加载中…
          </div>
        ) : board.isError ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-sm">
            <p className="text-[var(--danger)]">{(board.error as Error)?.message ?? '加载失败'}</p>
            <Button onClick={() => void board.refetch()}>
              <RefreshCw className="h-3.5 w-3.5" aria-hidden />
              重试
            </Button>
          </div>
        ) : !board.data ? null : (
          <Board
            data={board.data}
            agents={agentMap}
            projects={projectMap}
            transitions={meta.data?.human_transitions ?? {}}
            guards={meta.data?.human_transition_guards}
            sort={filters.sort}
            filtered={isFiltered(filters)}
            collapsed={collapsed}
            onToggleCollapse={toggleCollapsed}
            onOpenTask={setOpenTask}
            onTransition={onTransition}
            onGuardBlocked={(block, task) => {
              // 派发未指派：直接打开详情抽屉让用户选Agent，
              // 而不是只弹一句"请先指派"让他自己再去点开卡片找。
              setToast({ kind: 'error', msg: block.message })
              if (block.guard === 'requires_assignee') setOpenTask(task.id)
            }}
            onReorderBlocked={(block) => {
              // 重排会被静默丢弃时必须说清楚原因与出路，
              // 否则用户只会觉得"这个界面坏了"。
              setToast({ kind: 'error', msg: block.message })
            }}
          />
        )}
      </main>

      {openTask !== null ? (
        <TaskDrawer
          taskId={openTask}
          agents={agents.data?.items ?? []}
          onClose={() => setOpenTask(null)}
        />
      ) : null}

      {showAgents ? (
        <AgentsPanel agents={agents.data?.items ?? []} onClose={() => setShowAgents(false)} />
      ) : null}

      {showProjects ? <ProjectsDialog onClose={() => setShowProjects(false)} /> : null}

      {showTemplates ? <TemplatesDialog onClose={() => setShowTemplates(false)} /> : null}

      {showSchedules ? <SchedulesDialog onClose={() => setShowSchedules(false)} /> : null}

      {showPhone ? <PhoneAccessPanel onClose={() => setShowPhone(false)} /> : null}

      {showNew ? (
        <NewTaskDialog
          agents={agents.data?.items ?? []}
          onClose={() => setShowNew(false)}
          onCreated={(id) => {
            setShowNew(false)
            setOpenTask(id)
          }}
        />
      ) : null}

      {toast ? (
        <div
          role="status"
          className={`fixed bottom-4 left-1/2 z-[60] -translate-x-1/2 rounded-lg px-3 py-2 text-sm shadow-[var(--shadow-pop)] ${
            toast.kind === 'error'
              ? 'bg-[var(--danger)] text-white'
              : 'bg-[var(--ink)] text-[var(--surface)]'
          }`}
        >
          {toast.msg}
        </div>
      ) : null}
    </div>
  )
}

function TopBar({
  onlineCount,
  totalAgents,
  streamState,
  onToggleTheme,
  onShowAgents,
  onShowPhone,
  onShowProjects,
  onShowTemplates,
  onShowSchedules,
  onNewTask,
}: {
  onlineCount: number
  totalAgents: number
  streamState: string
  onToggleTheme: () => void
  onShowAgents: () => void
  onShowPhone: () => void
  onShowProjects: () => void
  onShowTemplates: () => void
  onShowSchedules: () => void
  onNewTask: () => void
}) {
  return (
    <header className="flex shrink-0 items-center gap-3 border-b border-[var(--border)] bg-[var(--surface)] px-3 py-2">
      <h1 className="text-sm font-semibold text-[var(--ink)]">Ingot Board</h1>

      <ConnectionBadge state={streamState} />

      <div className="ml-auto flex items-center gap-1">
        <Button variant="ghost" size="sm" onClick={onShowPhone} title="手机连接">
          <Smartphone className="h-3.5 w-3.5" aria-hidden />
          <span className="hidden sm:inline">手机</span>
        </Button>

        <Button variant="ghost" size="sm" onClick={onShowProjects} title="项目管理">
          <FolderKanban className="h-3.5 w-3.5" aria-hidden />
          <span className="hidden sm:inline">项目</span>
        </Button>

        <Button variant="ghost" size="sm" onClick={onShowTemplates} title="任务模板">
          <Layers className="h-3.5 w-3.5" aria-hidden />
          <span className="hidden sm:inline">模板</span>
        </Button>

        <Button variant="ghost" size="sm" onClick={onShowSchedules} title="定时计划">
          <CalendarClock className="h-3.5 w-3.5" aria-hidden />
          <span className="hidden sm:inline">计划</span>
        </Button>

        <Button variant="ghost" size="sm" onClick={onShowAgents} title="Agent 管理">
          <Users className="h-3.5 w-3.5" aria-hidden />
          <span className="hidden sm:inline">Agent</span>
          <span className="text-xs text-[var(--ink-subtle)]">
            {onlineCount}/{totalAgents}
          </span>
        </Button>

        <Button
          variant="ghost"
          size="icon"
          onClick={onToggleTheme}
          aria-label={document.documentElement.classList.contains('dark') ? '切换到亮色' : '切换到暗色'}
        >
          {document.documentElement.classList.contains('dark') ? (
            <Sun className="h-4 w-4" aria-hidden />
          ) : (
            <Moon className="h-4 w-4" aria-hidden />
          )}
        </Button>

        <Button variant="primary" size="sm" onClick={onNewTask}>
          <Plus className="h-3.5 w-3.5" aria-hidden />
          新建
        </Button>
      </div>
    </header>
  )
}

/**
 * 连接状态。
 *
 * 用图标 + 文字 + 颜色三重表达：只看颜色的话，色觉障碍用户无法区分
 * "已连接"和"已断开"这两个都用灰色系的状态。
 */
function ConnectionBadge({ state }: { state: string }) {
  if (state === 'open') {
    return (
      <span
        className="inline-flex items-center gap-1 text-xs text-[var(--success)]"
        title="实时连接正常"
      >
        <Check className="h-3 w-3" aria-hidden />
        <span className="hidden sm:inline">已连接</span>
      </span>
    )
  }
  if (state === 'resync') {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-[var(--warning)]" title="数据已重新同步">
        <RefreshCw className="h-3 w-3" aria-hidden />
        <span className="hidden sm:inline">已重新同步</span>
      </span>
    )
  }
  return (
    <span
      className="inline-flex items-center gap-1 text-xs text-[var(--ink-subtle)]"
      title="正在连接，界面可能不是最新"
    >
      <WifiOff className="h-3 w-3" aria-hidden />
      <span className="hidden sm:inline">连接中</span>
    </span>
  )
}
