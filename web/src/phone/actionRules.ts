/**
 * 手机任务底部的主操作。状态边只来自人类可走的流转，不用拖拽。
 */

import type { TaskStatus } from '@/api/types'

export function phonePrimaryActions(status: TaskStatus): { to: TaskStatus; label: string }[] {
  switch (status) {
    case 'idea':
      return [{ to: 'todo', label: '派发' }]
    case 'todo':
      return [{ to: 'idea', label: '收回' }]
    case 'review':
      return [
        { to: 'approved', label: '通过' },
        { to: 'todo', label: '驳回' },
      ]
    default:
      return []
  }
}

export function actionNeedsComment(status: TaskStatus, to: TaskStatus): boolean {
  return status === 'review' && to === 'todo'
}
