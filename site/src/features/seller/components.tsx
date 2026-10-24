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
      className={cn('block animate-pulse rounded-[3px] bg-wash', className)}
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
          <div key={index} className="border border-line bg-surface p-4">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="mt-2 h-6 w-28" />
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
      <label htmlFor={htmlFor} className="block text-[12px] font-medium text-ink">
        {label}
      </label>
      <div className="mt-1.5">{children}</div>
      {error ? (
        <p className="mt-1 text-[11.5px] leading-5 text-danger" role="alert">
          {error}
        </p>
      ) : hint ? (
        <p className="mt-1 text-[11.5px] leading-5 text-muted">{hint}</p>
      ) : null}
    </div>
  )
}

export const selectClass =
  'h-9 w-full rounded-[4px] border border-line-strong bg-surface px-2.5 text-[13.5px] text-ink transition-colors focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/15 disabled:cursor-not-allowed disabled:bg-wash disabled:text-muted'

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
        <h2 className="text-[15px] font-semibold tracking-tight text-ink">{title}</h2>
        {lead ? <p className="mt-1 text-[12px] leading-5 text-muted">{lead}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  )
}

export function MonoNumber({ children }: { children: ReactNode }) {
  return <span className="font-numeric tabular-nums">{children}</span>
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
      <span className="text-[11px] text-muted">{copy.identity.buyer}</span>
      <span className="font-numeric text-[13px] uppercase tabular-nums tracking-[0.04em] text-ink">
        {code}
      </span>
    </span>
  )
}
