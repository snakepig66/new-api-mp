import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export interface EmptyProps {
  title?: string
  /** Why the panel is empty. Required: empty states explain themselves. */
  reason: string
  /** What the reader can do next. */
  next?: ReactNode
  action?: ReactNode
  className?: string
}

/**
 * Empty state with a mandatory reason and (usually) a next step.
 * No illustration: the heading and the next action carry the message.
 */
export function Empty({ title, reason, next, action, className }: EmptyProps) {
  return (
    <div className={cn('glass-inset px-6 py-10', className)}>
      <div className="min-w-0 max-w-xl">
        <h3 className="text-title font-semibold tracking-tight text-ink">
          {title ?? copy.common.emptyTitle}
        </h3>
        <p className="mt-2 text-body leading-6 text-ink-3">{reason}</p>
        {next ? (
          <p className="mt-3 text-body leading-6 text-ink-3">
            <span className="font-medium text-ink">{copy.common.nextStep}：</span>
            {next}
          </p>
        ) : null}
        {action ? <div className="mt-5 flex flex-wrap items-center gap-3">{action}</div> : null}
      </div>
    </div>
  )
}
