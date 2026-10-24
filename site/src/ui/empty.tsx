import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import { LogoMark } from './logo'

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
 * Empty screens are an invitation to act, not a mood.
 */
export function Empty({ title, reason, next, action, className }: EmptyProps) {
  return (
    <div className={cn('border border-line bg-surface px-5 py-7 sm:px-7', className)}>
      <div className="flex items-start gap-3">
        <LogoMark size={20} className="mt-0.5 shrink-0 text-brand/60" />
        <div className="min-w-0">
          <h3 className="text-[15px] font-semibold tracking-tight text-ink">
            {title ?? copy.common.emptyTitle}
          </h3>
          <p className="mt-1 text-[13px] leading-6 text-muted">{reason}</p>
          {next ? (
            <p className="mt-3 text-[13px] leading-6 text-muted">
              <span className="font-medium text-ink">{copy.common.nextStep}：</span>
              {next}
            </p>
          ) : null}
          {action ? <div className="mt-4">{action}</div> : null}
        </div>
      </div>
    </div>
  )
}
