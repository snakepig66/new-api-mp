import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { ApiError } from '@/lib/api'
import { Button } from '@/ui'

/** Pulsing neutral block used while a panel loads. */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={cn('animate-pulse rounded-sm bg-white/60 motion-reduce:animate-none', className)}
    />
  )
}

export function StatSkeleton() {
  return (
    <div>
      <Skeleton className="h-3.5 w-16" />
      <Skeleton className="mt-2 h-6 w-28" />
      <Skeleton className="mt-2 h-3 w-20" />
    </div>
  )
}

export function TableSkeleton({ rows = 5, columns = 5 }: { rows?: number; columns?: number }) {
  return (
    <div className="space-y-2 border-t border-line pt-3" aria-hidden="true">
      {Array.from({ length: rows }, (_, row) => (
        <div key={row} className="grid gap-3" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
          {Array.from({ length: columns }, (_, column) => (
            <Skeleton key={column} className="h-4" />
          ))}
        </div>
      ))}
    </div>
  )
}

export function PageSkeleton() {
  return (
    <div className="space-y-6" aria-busy="true" aria-label={copy.pages.consoleShared.loading}>
      <div>
        <Skeleton className="h-6 w-40" />
        <Skeleton className="mt-3 h-3.5 w-72 max-w-full" />
      </div>
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatSkeleton />
        <StatSkeleton />
        <StatSkeleton />
        <StatSkeleton />
      </div>
      <TableSkeleton />
    </div>
  )
}

export interface ErrorBlockProps {
  error: unknown
  onRetry?: () => void
  className?: string
}

/**
 * Error state for one panel. ApiError already carries the Chinese message
 * mapped from the backend code in lib/api.ts.
 */
export function ErrorBlock({ error, onRetry, className }: ErrorBlockProps) {
  const message = error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint
  const code = error instanceof ApiError && error.code ? error.code : null
  return (
    <div
      role="alert"
      className={cn('border border-danger/25 bg-danger/10 px-4 py-3.5', className)}
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="text-meta font-medium text-danger">
            {copy.pages.consoleShared.errorTitle}
          </p>
          <p className="mt-1 text-meta leading-5 text-danger/90">{message}</p>
          {code ? (
            <p className="mt-1 tabular-nums text-meta text-danger/70">
              {copy.pages.consoleShared.errorCode}
              {code}
            </p>
          ) : null}
        </div>
        {onRetry ? (
          <Button variant="secondary" size="sm" className="shrink-0" onClick={onRetry}>
            {copy.pages.consoleShared.retry}
          </Button>
        ) : null}
      </div>
    </div>
  )
}

export interface SectionProps {
  title: ReactNode
  hint?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}

/** A white console card: 12px radius, one hairline, no shadow. */
export function Section({ title, hint, action, children, className }: SectionProps) {
  return (
    <section className={cn('min-w-0 glass p-6', className)}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-line pb-4">
        <div className="flex min-w-0 flex-wrap items-baseline gap-2.5">
          <h2 className="text-title font-semibold tracking-tight text-ink">{title}</h2>
          {hint ? <span className="text-meta text-ink-3">{hint}</span> : null}
        </div>
        {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
      </div>
      <div className="pt-4">{children}</div>
    </section>
  )
}
