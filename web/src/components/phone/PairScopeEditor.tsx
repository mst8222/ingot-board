/**
 * 配对设备范围编辑器（026-pair-scope-ui）。
 *
 * 受控组件：单一真相源是 value.ops / value.project_ids，简化视图与
 * 高级视图都是它的投影 —— 切换视图不产生编辑事件，收起高级不重置选择。
 * 父层（PhoneAccessPanel）负责初始化为缺省 15 op，并决定何时下发 body。
 */
import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { DeviceScopeOp, DeviceScopes, Project } from '@/api/types'
import { OP_GROUPS, PROJECT_MGMT_OPS } from './pairScope'

interface PairScopeEditorProps {
  value: DeviceScopes
  onChange: (next: DeviceScopes) => void
  projects: Project[]
  disabled?: boolean
}

const checkboxClass = 'h-4 w-4 shrink-0 accent-[var(--accent)]'

export function PairScopeEditor({ value, onChange, projects, disabled }: PairScopeEditorProps) {
  const [advanced, setAdvanced] = useState(false)
  const ops = value.ops

  const has = (op: DeviceScopeOp) => ops.includes(op)

  const toggleOp = (op: DeviceScopeOp) => {
    onChange({
      ops: has(op) ? ops.filter((o) => o !== op) : [...ops, op],
      project_ids: value.project_ids,
    })
  }

  const toggleGroup = (group: (typeof OP_GROUPS)[number], on: boolean) => {
    const rest = ops.filter((o) => !group.ops.includes(o))
    onChange({ ops: on ? [...rest, ...group.ops] : rest, project_ids: value.project_ids })
  }

  const toggleProject = (id: number) => {
    onChange({
      ops,
      project_ids: value.project_ids.includes(id)
        ? value.project_ids.filter((p) => p !== id)
        : [...value.project_ids, id],
    })
  }

  const mgmtAll = PROJECT_MGMT_OPS.every(has)
  const mgmtSome = PROJECT_MGMT_OPS.some(has)
  const toggleMgmt = () => {
    const rest = ops.filter((o) => !PROJECT_MGMT_OPS.includes(o))
    onChange({ ops: mgmtAll ? rest : [...rest, ...PROJECT_MGMT_OPS], project_ids: value.project_ids })
  }

  // 简化视图的项目列表：归档项目不出现
  const activeProjects = projects.filter((p) => p.archived_at == null)

  return (
    <section className="flex flex-col gap-2 rounded-md border border-[var(--border)] p-3">
      <h3 className="text-xs font-semibold">设备范围</h3>
      <p className="text-xs text-[var(--ink-muted)]">
        基础手机操作始终可用；范围随配对码签发，配对后不可更改。
      </p>

      <fieldset className="flex flex-col gap-1">
        <legend className="text-xs font-medium">可访问的项目</legend>
        {activeProjects.length === 0 ? (
          <p className="text-xs text-[var(--ink-muted)]">暂无项目。先在顶栏「项目」里创建。</p>
        ) : (
          activeProjects.map((p) => (
            <label key={p.id} className="flex min-h-11 items-center gap-2 text-sm">
              <input
                type="checkbox"
                className={checkboxClass}
                checked={value.project_ids.includes(p.id)}
                disabled={disabled}
                onChange={() => toggleProject(p.id)}
              />
              {p.name}
            </label>
          ))
        )}
        {value.project_ids.length === 0 ? (
          <p className="text-xs text-[var(--ink-muted)]">
            未选择项目时，手机无法操作挂项目的任务；手填目录的任务不受限。
          </p>
        ) : null}
      </fieldset>

      <label className="flex min-h-11 items-center gap-2 text-sm">
        <input
          type="checkbox"
          className={checkboxClass}
          checked={mgmtAll}
          ref={(el) => {
            if (el) el.indeterminate = !mgmtAll && mgmtSome
          }}
          disabled={disabled || advanced}
          onChange={toggleMgmt}
        />
        允许管理项目
        {advanced ? (
          <span className="text-xs text-[var(--ink-muted)]">（已按组细化，见高级）</span>
        ) : (
          <span className="text-xs text-[var(--ink-muted)]">（项目与目录的增删改）</span>
        )}
      </label>

      {value.project_ids.length > 0 && !has('listProjects') ? (
        <p className="text-xs text-[var(--ink-muted)]">已授权项目但未开放项目列表，手机上看不到项目名。</p>
      ) : null}

      <button
        type="button"
        className="flex min-h-11 items-center gap-1 text-xs text-[var(--ink-muted)]"
        aria-expanded={advanced}
        onClick={() => setAdvanced(!advanced)}
      >
        {advanced ? <ChevronDown className="h-3.5 w-3.5" aria-hidden /> : <ChevronRight className="h-3.5 w-3.5" aria-hidden />}
        {advanced ? '收起高级' : '高级'}
      </button>

      {advanced ? (
        <div className="flex flex-col gap-3">
          {OP_GROUPS.map((group) => {
            const allOn = group.ops.every(has)
            return (
              <fieldset key={group.key} className="flex flex-col gap-1">
                <legend className="flex items-center gap-2 text-xs font-semibold">
                  {group.title}
                  <button
                    type="button"
                    className="min-h-6 rounded px-1 text-xs text-[var(--accent)]"
                    onClick={() => toggleGroup(group, !allOn)}
                  >
                    {allOn ? '清空' : '全选'}
                  </button>
                </legend>
                <div className="grid grid-cols-2 gap-1">
                  {group.ops.map((op) => (
                    <label key={op} className="flex min-h-9 items-center gap-1.5 text-xs">
                      <input
                        type="checkbox"
                        className={checkboxClass}
                        checked={has(op)}
                        disabled={disabled}
                        onChange={() => toggleOp(op)}
                      />
                      {group.labels[op]}
                    </label>
                  ))}
                </div>
              </fieldset>
            )
          })}
        </div>
      ) : null}
    </section>
  )
}
