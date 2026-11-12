/**
 * 配对设备范围的常量与摘要（026-pair-scope-ui）。
 *
 * op 清单与 internal/domain/scope.go 的注册表一一对应：
 * 15 个缺省手机操作（013）+ 9 个项目/目录管理操作（024/025）。
 */
import type { DeviceScopeOp, DeviceScopes, Project } from '@/api/types'

/** 缺省范围：013 已允许的手机人类操作，签发面板初始值即此清单 */
export const DEFAULT_PHONE_OPS: readonly DeviceScopeOp[] = [
  'health',
  'getMeta',
  'getBoard',
  'listTasks',
  'getTask',
  'createTask',
  'updateTask',
  'transitionTask',
  'addTaskNote',
  'listTaskEvents',
  'archiveTask',
  'unarchiveTask',
  'listAgents',
  'streamEvents',
  'listWorkdirs',
]

/** 项目与目录管理 op（024/025，缺省范围之外），「允许管理项目」开关的聚合口径 */
export const PROJECT_MGMT_OPS: readonly DeviceScopeOp[] = [
  'listProjects',
  'createProject',
  'updateProject',
  'archiveProject',
  'unarchiveProject',
  'deleteProject',
  'listProjectWorkdirs',
  'addProjectWorkdir',
  'removeProjectWorkdir',
]

export interface OpGroup {
  key: string
  title: string
  ops: readonly DeviceScopeOp[]
  labels: Record<string, string>
}

/** 高级区的操作分组：4 组覆盖全部 24 个 op，且与上面两个清单不重叠 */
export const OP_GROUPS: readonly OpGroup[] = [
  {
    key: 'read',
    title: '连接与查看',
    ops: [
      'health',
      'getMeta',
      'getBoard',
      'listTasks',
      'getTask',
      'listTaskEvents',
      'listAgents',
      'streamEvents',
      'listWorkdirs',
    ],
    labels: {
      health: '连接健康',
      getMeta: '元信息',
      getBoard: '看板',
      listTasks: '任务列表',
      getTask: '任务详情',
      listTaskEvents: '任务事件',
      listAgents: 'Agent 列表',
      streamEvents: '事件流',
      listWorkdirs: '最近目录',
    },
  },
  {
    key: 'task',
    title: '任务操作',
    ops: ['createTask', 'updateTask', 'transitionTask', 'addTaskNote', 'archiveTask', 'unarchiveTask'],
    labels: {
      createTask: '新建任务',
      updateTask: '编辑任务',
      transitionTask: '推进/派发任务',
      addTaskNote: '追加备注',
      archiveTask: '归档任务',
      unarchiveTask: '取消归档',
    },
  },
  {
    key: 'project',
    title: '项目管理',
    ops: ['listProjects', 'createProject', 'updateProject', 'archiveProject', 'unarchiveProject', 'deleteProject'],
    labels: {
      listProjects: '项目列表',
      createProject: '新建项目',
      updateProject: '编辑项目',
      archiveProject: '归档项目',
      unarchiveProject: '取消归档项目',
      deleteProject: '删除项目',
    },
  },
  {
    key: 'workdir',
    title: '项目目录',
    ops: ['listProjectWorkdirs', 'addProjectWorkdir', 'removeProjectWorkdir'],
    labels: {
      listProjectWorkdirs: '查看项目目录',
      addProjectWorkdir: '绑定目录',
      removeProjectWorkdir: '解绑目录',
    },
  },
]

/** 全部 24 个 op（分组并集），供全选等操作使用 */
export const ALL_OPS: readonly DeviceScopeOp[] = OP_GROUPS.flatMap((g) => [...g.ops])

/** 判断 ops 是否恰好等于缺省范围（15 个手机操作） */
export function isDefaultOps(ops: readonly DeviceScopeOp[]): boolean {
  return ops.length === DEFAULT_PHONE_OPS.length && DEFAULT_PHONE_OPS.every((op) => ops.includes(op))
}

export interface ScopeSummary {
  badge: string
  detail?: string
}

/** 设备范围摘要：存量设备字段缺失 → 全量权限；等于缺省 → 默认范围；否则数量 + 项目名 */
export function describeScopes(
  scopes: DeviceScopes | undefined,
  projects: Pick<Project, 'id' | 'name'>[],
): ScopeSummary {
  if (!scopes) return { badge: '全量权限' }
  if (isDefaultOps(scopes.ops) && scopes.project_ids.length === 0) return { badge: '默认范围' }
  const names = scopes.project_ids.map((id) => projects.find((p) => p.id === id)?.name ?? `#${id}`)
  return {
    badge:
      scopes.project_ids.length > 0
        ? `${scopes.ops.length} 操作 · ${scopes.project_ids.length} 项目`
        : `${scopes.ops.length} 操作`,
    detail: names.length > 0 ? `可访问项目：${names.join('、')}` : undefined,
  }
}
