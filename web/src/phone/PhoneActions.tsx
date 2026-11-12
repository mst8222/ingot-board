import type { TaskStatus } from '@/api/types'
import { Button, Textarea } from '@/components/ui/primitives'
import { actionNeedsComment, phonePrimaryActions } from './actionRules'

export function PhoneActions({
  status,
  comment,
  pending,
  onComment,
  onTransition,
  onNote,
}: {
  status: TaskStatus
  comment: string
  pending: boolean
  onComment: (value: string) => void
  onTransition: (to: TaskStatus) => void
  onNote: () => void
}) {
  const actions = phonePrimaryActions(status)
  return (
    <div className="flex flex-col gap-2 border-t border-[var(--border)] bg-[var(--surface)] p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
      <Textarea
        rows={2}
        value={comment}
        onChange={(e) => onComment(e.target.value)}
        placeholder="备注，驳回时必填"
        aria-label="备注"
      />
      <div className="flex flex-wrap gap-2">
        {actions.map((action) => (
          <Button
            key={action.to + action.label}
            variant={action.label === '驳回' ? 'danger' : 'primary'}
            className="h-11 min-h-11 flex-1"
            disabled={pending || (actionNeedsComment(status, action.to) && !comment.trim())}
            onClick={() => onTransition(action.to)}
          >
            {action.label}
          </Button>
        ))}
        <Button variant="secondary" className="h-11 min-h-11 flex-1" disabled={pending || !comment.trim()} onClick={onNote}>
          备注
        </Button>
      </div>
    </div>
  )
}
