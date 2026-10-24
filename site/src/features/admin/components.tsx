/**
 * 运营后台共用小组件：骨架、错误态、筛选 Tab、二次守卫与确认弹窗。
 * 只组合 ui/ 基础组件，不改设计系统。
 */

import type {
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
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
      className={cn('animate-pulse rounded-[3px] bg-wash motion-reduce:animate-none', className)}
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
    <div role="alert" className={cn('border border-danger/25 bg-danger-soft px-4 py-3.5', className)}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="text-[13px] font-medium text-danger">{copy.pages.adminCommon.loadError}</p>
          <p className="mt-1 text-[12.5px] leading-5 text-danger/90">{message}</p>
          {code ? (
            <p className="mt-1 font-numeric text-[11px] text-danger/70">
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
    <section className={cn('min-w-0', className)}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-line pb-2.5">
        <div className="flex min-w-0 items-baseline gap-2.5">
          <h2 className="text-[14px] font-semibold tracking-tight text-ink">{title}</h2>
          {hint ? <span className="text-[11.5px] text-muted">{hint}</span> : null}
        </div>
        {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
      </div>
      <div className="pt-3.5">{children}</div>
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
      <label htmlFor={htmlFor} className="mb-1.5 block text-[12.5px] font-medium text-ink">
        {label}
      </label>
      {children}
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

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'h-9 w-full rounded-[4px] border border-line-strong bg-surface px-2.5 text-[13.5px] text-ink transition-colors',
        'focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/15',
        'disabled:cursor-not-allowed disabled:bg-wash disabled:text-muted',
        className,
      )}
      {...props}
    />
  )
}

export function TextInput({
  invalid = false,
  mono = true,
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean; mono?: boolean }) {
  return (
    <Input
      invalid={invalid}
      mono={mono}
      className={cn('h-9 text-[13px]', className)}
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
    neutral: 'border-line bg-wash text-muted',
    warning: 'border-warning/25 bg-warning-soft text-warning',
    brand: 'border-brand/25 bg-brand-soft text-brand-dark',
  }
  return (
    <div className={cn('border px-4 py-3', tones[tone], className)} role={tone === 'warning' ? 'status' : undefined}>
      {title ? <p className="text-[12.5px] font-medium">{title}</p> : null}
      {children ? <div className={cn('text-[12.5px] leading-6', title && 'mt-1')}>{children}</div> : null}
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
      className="-mx-1 mb-5 flex items-center gap-1 overflow-x-auto border-b border-line"
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
              '-mb-px shrink-0 border-b-2 px-3 py-2 text-[13px] transition-colors',
              active
                ? 'border-brand-dark font-medium text-ink'
                : 'border-transparent text-muted hover:border-line-strong hover:text-ink',
            )}
          >
            {option.label}
          </button>
        )
      })}
    </div>
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
