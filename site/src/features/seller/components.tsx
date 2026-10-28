/**
 * 分享者控制台共用的小组件：骨架、错误态、字段包装。
 * 只组合 ui/ 里的基础组件，不重写设计系统。
 */

import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { Button, Empty, type ToastOptions } from '@/ui'

export function Skeleton({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn('block animate-pulse rounded-sm bg-white/60', className)}
    />
  )
}

export function PageSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-6" aria-busy="true" aria-live="polite">
      <div className="border-b border-line pb-6">
        <Skeleton className="h-7 w-48" />
        <Skeleton className="mt-3 h-4 w-full max-w-xl" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, index) => (
          <div key={index} className="glass p-5">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="mt-2 h-7 w-28" />
          </div>
        ))}
      </div>
      <TableSkeleton rows={rows} />
    </div>
  )
}

export function TableSkeleton({ rows = 5, columns = 5 }: { rows?: number; columns?: number }) {
  return (
    <div className="border-t border-line" aria-busy="true" aria-live="polite">
      {Array.from({ length: rows }, (_, row) => (
        <div key={row} className="flex items-center gap-4 border-b border-line/70 px-3 py-3">
          {Array.from({ length: columns }, (_, column) => (
            <Skeleton
              key={column}
              className={cn('h-3.5', column === 0 ? 'w-40' : 'w-full max-w-24')}
            />
          ))}
        </div>
      ))}
    </div>
  )
}

export function AsyncError({
  title,
  reason,
  onRetry,
  retryLabel,
}: {
  title: string
  reason: string
  onRetry?: () => void
  retryLabel?: string
}) {
  return (
    <Empty
      title={title}
      reason={reason}
      action={
        onRetry ? (
          <Button variant="secondary" onClick={onRetry}>
            {retryLabel ?? copy.actions.retry}
          </Button>
        ) : undefined
      }
    />
  )
}

export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
  className,
}: {
  label: string
  htmlFor?: string
  hint?: ReactNode
  error?: string
  children: ReactNode
  className?: string
}) {
  return (
    <div className={cn('min-w-0', className)}>
      <label htmlFor={htmlFor} className="block text-body font-medium text-ink">
        {label}
      </label>
      <div className="mt-2">{children}</div>
      {error ? (
        <p className="mt-1 text-meta leading-5 text-danger" role="alert">
          {error}
        </p>
      ) : hint ? (
        <p className="mt-1 text-meta leading-5 text-ink-3">{hint}</p>
      ) : null}
    </div>
  )
}

export const selectClass =
  'h-12 w-full rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-4 text-body text-ink transition-colors focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20 disabled:cursor-not-allowed disabled:bg-white/35 disabled:text-ink-3'

export function SectionHeading({
  title,
  lead,
  actions,
}: {
  title: string
  lead?: string
  actions?: ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h2 className="text-title font-semibold tracking-tight text-ink">{title}</h2>
        {lead ? <p className="mt-1 text-meta leading-5 text-ink-3">{lead}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  )
}

export function MonoNumber({ children }: { children: ReactNode }) {
  return <span className="tabular-nums">{children}</span>
}

/**
 * Toast API compatibility: the design system currently returns { show }, while
 * sibling workstreams are migrating to a callable hook. Accept either shape so
 * the seller console keeps working through that transition.
 */
export function notify(toast: unknown, options: ToastOptions): void {
  if (typeof toast === 'function') {
    ;(toast as (next: ToastOptions) => void)(options)
    return
  }
  ;(toast as { show?: (next: ToastOptions) => void } | null)?.show?.(options)
}

/**
 * 「买家 7F3A9C21」——分享记录里只出现匿名代号。
 * Local copy of the identity treatment so the seller console does not depend on
 * a shared ui module that other workstreams are still reshaping.
 */
export function BuyerCode({ code }: { code: string }) {
  return (
    <span className="inline-flex items-baseline gap-1 whitespace-nowrap">
      <span className="text-meta text-ink-3">{copy.identity.buyer}</span>
      <span className="text-meta tabular-nums uppercase tracking-[0.04em] text-ink">
        {code}
      </span>
    </span>
  )
}
