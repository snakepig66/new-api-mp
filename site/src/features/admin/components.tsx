/**
 * 运营后台共用小组件：骨架、错误态、筛选 Tab、二次守卫与确认弹窗。
 * 只组合 ui/ 基础组件，不改设计系统。
 */

import { Link, useRouterState } from '@tanstack/react-router'
import type {
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { cn } from '@/lib/cn'
import { Button, Dialog, Empty, Input } from '@/ui'

import { adminErrorMessage } from './errors'

export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={cn('animate-pulse rounded-sm bg-white/60 motion-reduce:animate-none', className)}
    />
  )
}

export function TableSkeleton({ rows = 6, columns = 6 }: { rows?: number; columns?: number }) {
  return (
    <div className="space-y-2 border-t border-line pt-3" aria-busy="true">
      {Array.from({ length: rows }, (_, row) => (
        <div
          key={row}
          className="grid gap-3"
          style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
        >
          {Array.from({ length: columns }, (_, column) => (
            <Skeleton key={column} className="h-4" />
          ))}
        </div>
      ))}
    </div>
  )
}

export function PageSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div className="space-y-6" aria-busy="true" aria-label={copy.pages.adminCommon.loading}>
      <div className="flex items-center gap-4 border-b border-line pb-3">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-4 w-16" />
        <Skeleton className="h-4 w-20" />
      </div>
      <TableSkeleton rows={rows} />
    </div>
  )
}

export function ErrorBlock({
  error,
  onRetry,
  className,
}: {
  error: unknown
  onRetry?: () => void
  className?: string
}) {
  const message = adminErrorMessage(error)
  const code = error instanceof ApiError && error.code ? error.code : null
  return (
    <div role="alert" className={cn('rounded-md border border-danger/25 bg-danger/10 px-4 py-3.5', className)}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="text-meta font-medium text-danger">{copy.pages.adminCommon.loadError}</p>
          <p className="mt-1 text-meta leading-5 text-danger/90">{message}</p>
          {code ? (
            <p className="mt-1 tabular-nums text-meta text-danger/70">
              {copy.pages.adminCommon.errorCode}
              {code}
            </p>
          ) : null}
        </div>
        {onRetry ? (
          <Button variant="secondary" size="sm" className="shrink-0" onClick={onRetry}>
            {copy.pages.adminCommon.retry}
          </Button>
        ) : null}
      </div>
    </div>
  )
}

export function Section({
  title,
  hint,
  action,
  children,
  className,
}: {
  title: ReactNode
  hint?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
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

export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
  className,
}: {
  label: ReactNode
  htmlFor?: string
  hint?: ReactNode
  error?: string | null
  children: ReactNode
  className?: string
}) {
  return (
    <div className={cn('min-w-0', className)}>
      <label htmlFor={htmlFor} className="mb-2 block text-body font-medium text-ink">
        {label}
      </label>
      {children}
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

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'h-12 w-full rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-4 text-body text-ink transition-colors',
        'focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20',
        'disabled:cursor-not-allowed disabled:bg-white/35 disabled:text-ink-3',
        className,
      )}
      {...props}
    />
  )
}

export function TextInput({
  invalid = false,
  mono = false,
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean; mono?: boolean }) {
  return (
    <Input
      invalid={invalid}
      mono={mono}
      className={cn('h-12 text-body', className)}
      {...props}
    />
  )
}

/** Multi-line field on the same soft bed as TextInput. */
export function TextArea({
  invalid = false,
  className,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement> & { invalid?: boolean }) {
  return (
    <textarea
      aria-invalid={invalid || undefined}
      className={cn(
        'w-full resize-y rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-4 py-3 text-body leading-6 text-ink transition-colors',
        'placeholder:text-ink-3',
        'focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20',
        'disabled:cursor-not-allowed disabled:bg-white/35 disabled:text-ink-3',
        'aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger/10',
        className,
      )}
      {...props}
    />
  )
}

export function Notice({
  tone = 'neutral',
  title,
  children,
  className,
}: {
  tone?: 'neutral' | 'warning' | 'brand'
  title?: ReactNode
  children?: ReactNode
  className?: string
}) {
  const tones = {
    neutral: 'border-white/70 bg-white/45 text-ink-3 backdrop-blur-md',
    warning: 'border-warn/25 bg-warn/10 text-warn',
    brand: 'border-brand/25 bg-brand-soft text-brand-ink',
  }
  return (
    <div className={cn('rounded-md border px-4 py-3', tones[tone], className)} role={tone === 'warning' ? 'status' : undefined}>
      {title ? <p className="text-meta font-medium">{title}</p> : null}
      {children ? <div className={cn('text-meta leading-6', title && 'mt-1')}>{children}</div> : null}
    </div>
  )
}

/** 二级守卫：父路由只保证 role>=admin，敏感后台页必须 root。 */
export function RootOnly({ children }: { children: ReactNode }) {
  const { isRoot } = useAuth()
  if (!isRoot) {
    return (
      <Empty
        title={copy.pages.adminCommon.rootRequiredTitle}
        reason={copy.pages.adminCommon.rootRequiredReason}
        next={copy.pages.adminCommon.rootRequiredNext}
      />
    )
  }
  return <>{children}</>
}

export interface FilterTabOption<T extends string> {
  value: T
  label: string
}

export function FilterTabs<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: {
  options: readonly FilterTabOption<T>[]
  value: T
  onChange: (next: T) => void
  ariaLabel: string
}) {
  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      className="mb-5 flex flex-wrap items-center gap-1.5"
    >
      {options.map((option) => {
        const active = option.value === value
        return (
          <button
            key={option.value}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onChange(option.value)}
            className={cn(
              'inline-flex h-9 shrink-0 items-center rounded-full px-3.5 text-body font-medium transition-colors',
              active
                ? 'bg-brand-soft text-brand-ink'
                : 'text-ink-2 hover:bg-brand/[0.06] hover:text-ink',
            )}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}

export type OpsTabPath =
  | '/console/ops/models'
  | '/console/ops/pricing'
  | '/console/ops/pricing/per-call'
  | '/console/ops/review'
  | '/console/ops/review/payouts'
  | '/console/ops/review/trust-pool'
  | '/console/ops/metrics'
  | '/console/ops/trace'
  | '/console/ops/tools'
  | '/console/ops/tools/reversal'
  | '/console/ops/tools/risk'

export interface OpsTabLink {
  to: OpsTabPath
  label: string
}

/** 运营页面分组的可分享标签页导航。 */
export function OpsTabNav({
  tabs,
  ariaLabel,
}: {
  tabs: readonly OpsTabLink[]
  ariaLabel: string
}) {
  let pathname = ''
  let hasRouter = false
  try {
    pathname = useRouterState({ select: (state) => state?.location?.pathname ?? '' })
    hasRouter = true
  } catch {
    pathname = typeof window !== 'undefined' ? window.location?.pathname ?? '' : ''
  }
  const current = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  return (
    <nav aria-label={ariaLabel} className="mb-5 flex flex-wrap items-center gap-1.5">
      {tabs.map((tab) => {
        const active = current === tab.to
        const linkClass = cn(
          'inline-flex h-9 shrink-0 items-center whitespace-nowrap rounded-full px-3.5 text-body transition-colors',
          active
            ? 'bg-brand-soft font-medium text-brand-ink'
            : 'text-ink-2 hover:bg-brand/[0.06] hover:text-ink',
        )
        if (hasRouter) {
          return (
            <Link
              key={tab.to}
              to={tab.to}
              aria-current={active ? 'page' : undefined}
              className={linkClass}
            >
              {tab.label}
            </Link>
          )
        }
        return (
          <a
            key={tab.to}
            href={tab.to}
            aria-current={active ? 'page' : undefined}
            className={linkClass}
          >
            {tab.label}
          </a>
        )
      })}
    </nav>
  )
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  tone = 'danger',
  loading = false,
  onConfirm,
  onClose,
  children,
}: {
  open: boolean
  title: string
  description?: string
  confirmLabel: string
  cancelLabel?: string
  tone?: 'danger' | 'primary'
  loading?: boolean
  onConfirm: () => void
  onClose: () => void
  children?: ReactNode
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={title}
      description={description}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={loading}>
            {cancelLabel ?? copy.pages.adminCommon.cancel}
          </Button>
          <Button
            variant={tone === 'danger' ? 'danger' : 'primary'}
            loading={loading}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      {children}
    </Dialog>
  )
}
