import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'
import { Input } from '@/ui'

export interface FieldProps {
  label: ReactNode
  htmlFor?: string
  hint?: ReactNode
  error?: string | null
  children: ReactNode
  className?: string
}

/** Label + control + hint/error, with the console's compact rhythm. */
export function Field({ label, htmlFor, hint, error, children, className }: FieldProps) {
  return (
    <div className={cn('min-w-0', className)}>
      <label htmlFor={htmlFor} className="mb-1.5 block text-[12.5px] font-medium text-ink">
        {label}
      </label>
      {children}
      {error ? (
        <p className="mt-1 text-[11.5px] leading-5 text-danger">{error}</p>
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

export interface TextInputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean
  mono?: boolean
}

export function TextInput({ invalid = false, mono = true, className, ...props }: TextInputProps) {
  return (
    <Input
      invalid={invalid}
      mono={mono}
      className={cn('h-9 text-[13px]', className)}
      {...props}
    />
  )
}

/** A plain bordered notice used for warnings and "coming soon" panels. */
export function Notice({
  tone = 'neutral',
  title,
  children,
  className,
}: {
  tone?: 'neutral' | 'warning' | 'brand'
  title?: ReactNode
  children: ReactNode
  className?: string
}) {
  const tones = {
    neutral: 'border-line bg-wash text-muted',
    warning: 'border-warning/25 bg-warning-soft text-warning',
    brand: 'border-brand/25 bg-brand-soft text-brand-dark',
  }
  return (
    <div className={cn('border px-4 py-3', tones[tone], className)}>
      {title ? <p className="text-[12.5px] font-medium">{title}</p> : null}
      <div className={cn('text-[12.5px] leading-6', title && 'mt-1')}>{children}</div>
    </div>
  )
}
