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
      <label htmlFor={htmlFor} className="mb-2 block text-body font-medium text-ink">
        {label}
      </label>
      {children}
      {error ? (
        <p className="mt-1 text-meta leading-5 text-danger">{error}</p>
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

export interface TextInputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean
  mono?: boolean
}

export function TextInput({ invalid = false, mono = false, className, ...props }: TextInputProps) {
  return (
    <Input
      invalid={invalid}
      mono={mono}
      className={cn('h-12 text-body', className)}
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
    neutral: 'border-white/70 bg-white/45 text-ink-3 backdrop-blur-md',
    warning: 'border-warn/25 bg-warn/10 text-warn',
    brand: 'border-brand/25 bg-brand-soft text-brand-ink',
  }
  return (
    <div className={cn('rounded-md border px-4 py-3', tones[tone], className)}>
      {title ? <p className="text-meta font-medium">{title}</p> : null}
      <div className={cn('text-meta leading-6', title && 'mt-1')}>{children}</div>
    </div>
  )
}
