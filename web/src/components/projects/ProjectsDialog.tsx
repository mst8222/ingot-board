/**
 * 项目管理对话框（025-project-workdirs）。
 *
 * 左列项目列表（含归档）+ 新建；右列基本信息编辑、归档/删除、
 * 目录绑定/解绑。目录绑定复用 WorkdirField（含本机浏览）。
 * 绑定/解绑失败（409 WORKDIR_BOUND / 400 目录不存在）在行内提示。
 */

import { useState } from 'react'
import { Archive, ArchiveRestore, FolderKanban, Plus, Trash2, Unlink, X } from 'lucide-react'
import type { Project } from '@/api/types'
import { Button, Input, Badge } from '@/components/ui/primitives'
import { WorkdirField } from '@/components/task/WorkdirField'
import {
  useProjects,
  useCreateProject,
  useUpdateProject,
  useArchiveProject,
  useUnarchiveProject,
  useDeleteProject,
  useProjectWorkdirs,
  useAddProjectWorkdir,
  useRemoveProjectWorkdir,
  errorHint,
  errorMessage,
} from '@/hooks/useApi'

export function ProjectsDialog({ onClose }: { onClose: () => void }) {
  const projects = useProjects()
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [creating, setCreating] = useState(false)

  const list = projects.data ?? []
  const selected = list.find((p) => p.id === selectedId) ?? null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-label="项目管理">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} aria-hidden />

      <div className="relative flex h-[80vh] w-full max-w-3xl overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-[var(--shadow-pop)]">
        {/* 左列：项目列表 */}
        <div className="flex w-56 shrink-0 flex-col border-r border-[var(--border)]">
          <div className="flex items-center gap-1 px-3 py-2">
            <FolderKanban className="h-4 w-4 text-[var(--ink-muted)]" aria-hidden />
            <h2 className="text-sm font-semibold text-[var(--ink)]">项目</h2>
            <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="关闭">
              <X className="h-4 w-4" aria-hidden />
            </Button>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
            {list.length === 0 && !creating ? (
              <p className="px-2 py-4 text-xs text-[var(--ink-subtle)]">还没有项目，点下方按钮新建。</p>
            ) : null}
            <ul className="flex flex-col gap-0.5">
              {list.map((p) => (
                <li key={p.id}>
                  <button
                    type="button"
                    onClick={() => {
                      setSelectedId(p.id)
                      setCreating(false)
                    }}
                    className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm ${
                      p.id === selectedId ? 'bg-[var(--accent-soft)] text-[var(--ink)]' : 'text-[var(--ink-muted)] hover:bg-[var(--surface-2)]'
                    }`}
                  >
                    <span
                      className="h-2.5 w-2.5 shrink-0 rounded-full"
                      style={{ background: p.color || 'var(--ink-subtle)' }}
                      aria-hidden
                    />
                    <span className="min-w-0 flex-1 truncate">{p.name}</span>
                    {p.archived_at ? <Badge>已归档</Badge> : null}
                  </button>
                </li>
              ))}
            </ul>
          </div>

          <div className="border-t border-[var(--border)] p-2">
            <Button
              variant={creating ? 'secondary' : 'ghost'}
              size="sm"
              className="w-full"
              onClick={() => setCreating((v) => !v)}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
              新建项目
            </Button>
          </div>
        </div>

        {/* 右列：详情 */}
        <div className="min-w-0 flex-1 overflow-y-auto p-4">
          {creating ? (
            <CreateForm
              onCreated={(id) => {
                setCreating(false)
                setSelectedId(id)
              }}
              onCancel={() => setCreating(false)}
            />
          ) : selected ? (
            <ProjectDetail key={selected.id} project={selected} />
          ) : (
            <p className="text-sm text-[var(--ink-subtle)]">从左侧选择一个项目，或新建。</p>
          )}
        </div>
      </div>
    </div>
  )
}

function CreateForm({ onCreated, onCancel }: { onCreated: (id: number) => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [color, setColor] = useState('#6366f1')
  const [error, setError] = useState<{ msg: string; hint?: string | null } | null>(null)
  const create = useCreateProject()

  return (
    <form
      className="flex max-w-sm flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (!name.trim()) return
        create.mutate(
          { name: name.trim(), color },
          {
            onSuccess: (p) => onCreated(p.id),
            onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
          },
        )
      }}
    >
      <h3 className="text-sm font-semibold text-[var(--ink)]">新建项目</h3>
      {error ? <ErrorLine error={error} /> : null}
      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-[var(--ink-muted)]">名称</span>
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：IngotBoard" required />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-[var(--ink-muted)]">颜色</span>
        <input
          type="color"
          value={color}
          onChange={(e) => setColor(e.target.value)}
          className="h-8 w-16 cursor-pointer rounded border border-[var(--border)] bg-transparent"
          aria-label="项目颜色"
        />
      </label>
      <div className="flex gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={create.isPending || !name.trim()}>
          {create.isPending ? '创建中…' : '创建'}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          取消
        </Button>
      </div>
    </form>
  )
}

function ProjectDetail({ project }: { project: Project }) {
  const [name, setName] = useState(project.name)
  const [color, setColor] = useState(project.color || '#6366f1')
  const [notice, setNotice] = useState<{ msg: string; hint?: string | null; ok?: boolean } | null>(null)

  const update = useUpdateProject()
  const archive = useArchiveProject()
  const unarchive = useUnarchiveProject()
  const remove = useDeleteProject()

  const saveInfo = () => {
    update.mutate(
      { id: project.id, name: name.trim(), color },
      {
        onSuccess: () => setNotice({ msg: '已保存', ok: true }),
        onError: (err) => setNotice({ msg: errorMessage(err), hint: errorHint(err) }),
      },
    )
  }

  return (
    <div className="flex flex-col gap-5">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ink)]">
        <span className="h-3 w-3 rounded-full" style={{ background: project.color || 'var(--ink-subtle)' }} aria-hidden />
        {project.name}
        {project.archived_at ? <Badge>已归档</Badge> : null}
      </h3>

      {notice ? (
        <div
          role="alert"
          className={`rounded-md px-2.5 py-2 text-xs ${
            notice.ok
              ? 'bg-[var(--accent-soft)] text-[var(--ink)]'
              : 'bg-[var(--danger-soft)] text-[var(--danger)]'
          }`}
        >
          <div className="font-medium">{notice.msg}</div>
          {notice.hint ? <div className="opacity-80">{notice.hint}</div> : null}
        </div>
      ) : null}

      {/* 基本信息 */}
      <form
        className="flex max-w-sm flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          saveInfo()
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-[var(--ink-muted)]">名称</span>
          <Input value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-[var(--ink-muted)]">颜色</span>
          <input
            type="color"
            value={color}
            onChange={(e) => setColor(e.target.value)}
            className="h-8 w-16 cursor-pointer rounded border border-[var(--border)] bg-transparent"
            aria-label="项目颜色"
          />
        </label>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" size="sm" disabled={update.isPending || !name.trim()}>
            {update.isPending ? '保存中…' : '保存'}
          </Button>
          {project.archived_at ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={unarchive.isPending}
              onClick={() =>
                unarchive.mutate(project.id, {
                  onSuccess: () => setNotice({ msg: '已取消归档', ok: true }),
                  onError: (err) => setNotice({ msg: errorMessage(err), hint: errorHint(err) }),
                })
              }
            >
              <ArchiveRestore className="h-3.5 w-3.5" aria-hidden />
              取消归档
            </Button>
          ) : (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={archive.isPending}
              onClick={() =>
                archive.mutate(project.id, {
                  onSuccess: () => setNotice({ msg: '已归档；看板筛选不再显示它', ok: true }),
                  onError: (err) => setNotice({ msg: errorMessage(err), hint: errorHint(err) }),
                })
              }
            >
              <Archive className="h-3.5 w-3.5" aria-hidden />
              归档
            </Button>
          )}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="text-[var(--danger)]"
            disabled={remove.isPending}
            onClick={() => {
              if (!window.confirm(`删除项目「${project.name}」？其目录绑定会一并清除。`)) return
              remove.mutate(project.id, {
                onSuccess: () => setNotice({ msg: '已删除', ok: true }),
                onError: (err) => setNotice({ msg: errorMessage(err), hint: errorHint(err) }),
              })
            }}
          >
            <Trash2 className="h-3.5 w-3.5" aria-hidden />
            删除
          </Button>
        </div>
      </form>

      <WorkdirSection projectId={project.id} />
    </div>
  )
}

function WorkdirSection({ projectId }: { projectId: number }) {
  const workdirs = useProjectWorkdirs(projectId)
  const [dir, setDir] = useState('')
  const [error, setError] = useState<{ msg: string; hint?: string | null } | null>(null)

  const add = useAddProjectWorkdir()
  const remove = useRemoveProjectWorkdir()

  const bind = () => {
    if (!dir.trim()) return
    add.mutate(
      { projectId, workdir: dir.trim() },
      {
        onSuccess: () => {
          setDir('')
          setError(null)
        },
        onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }),
      },
    )
  }

  return (
    <section className="flex flex-col gap-2 border-t border-[var(--border)] pt-4">
      <h4 className="text-xs font-semibold text-[var(--ink-muted)]">
        工作目录
        <span className="ml-1 font-normal text-[var(--ink-subtle)]">
          绑定后可在手机端按项目聚合查看；不影响任务自身的工作目录
        </span>
      </h4>

      {error ? <ErrorLine error={error} /> : null}

      <ul className="flex flex-col gap-1">
        {(workdirs.data ?? []).map((w) => (
          <li key={w.id} className="flex items-center gap-2 rounded-md bg-[var(--surface-2)] px-2 py-1.5 text-sm">
            <code className="min-w-0 flex-1 truncate text-xs text-[var(--ink)]">{w.workdir}</code>
            <Button
              variant="ghost"
              size="icon"
              aria-label={`解绑 ${w.workdir}`}
              disabled={remove.isPending}
              onClick={() =>
                remove.mutate(
                  { projectId, wid: w.id },
                  { onError: (err) => setError({ msg: errorMessage(err), hint: errorHint(err) }) },
                )
              }
            >
              <Unlink className="h-3.5 w-3.5" aria-hidden />
            </Button>
          </li>
        ))}
        {workdirs.data && workdirs.data.length === 0 ? (
          <li className="text-xs text-[var(--ink-subtle)]">还没有绑定目录。</li>
        ) : null}
      </ul>

      <div className="flex items-end gap-2">
        <div className="min-w-0 flex-1">
          <WorkdirField value={dir} onChange={setDir} placeholder="选择或输入本机绝对路径" />
        </div>
        <Button variant="secondary" size="sm" disabled={add.isPending || !dir.trim()} onClick={bind}>
          <Plus className="h-3.5 w-3.5" aria-hidden />
          绑定
        </Button>
      </div>
    </section>
  )
}

function ErrorLine({ error }: { error: { msg: string; hint?: string | null } }) {
  return (
    <div role="alert" className="rounded-md bg-[var(--danger-soft)] px-2.5 py-2 text-xs text-[var(--danger)]">
      <div className="font-medium">{error.msg}</div>
      {error.hint ? <div className="opacity-80">{error.hint}</div> : null}
    </div>
  )
}
