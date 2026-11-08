import { copy } from '@/copy'
import { Button, Empty } from '@/ui'

export interface QueryErrorProps {
  title: string
  reason: string
  onRetry?: () => void
  retrying?: boolean
  className?: string
}

/**
 * Empty state for a failed query: what failed, and the one action that can
 * fix it. Every public page uses this instead of a bare error line.
 */
export function QueryError({ title, reason, onRetry, retrying = false, className }: QueryErrorProps) {
  return (
    <Empty
      title={title}
      reason={reason}
      next={copy.common.retryHint}
      className={className}
      action={
        onRetry ? (
          <Button variant="secondary" size="sm" loading={retrying} onClick={onRetry}>
            {copy.actions.retry}
          </Button>
        ) : undefined
      }
    />
  )
}
