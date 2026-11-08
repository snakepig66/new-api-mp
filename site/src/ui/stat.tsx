import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

/**
 * `success` / `warning` are compatibility aliases for untouched pages.
 * The palette has one accent: success maps to brand blue, warning to ink.
 */
export type StatTone = 'default' | 'brand' | 'success' | 'warning' | 'danger'

const toneClass: Record<StatTone, string> = {
  default: 'text-ink',
  brand: 'text-brand-ink',
  success: 'text-brand-ink',
  warning: 'text-warn',
  danger: 'text-danger',
}

export interface StatProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: StatTone
  className?: string
}

/** A label + sans tabular number. Group several in glass cards. */
export function Stat({ label, value, hint, tone = 'default', className }: StatProps) {
  return (
    <div className={cn('min-w-0 grid grid-rows-[auto_auto_auto] gap-y-1.5', className)}>
      <div className="text-meta leading-5 text-ink-3">{label}</div>
      <div
        className={cn(
          'text-h1 font-semibold leading-tight tracking-tight tabular-nums',
          toneClass[tone],
        )}
      >
        {value}
      </div>
      {hint ? <div className="text-meta leading-5 text-ink-3">{hint}</div> : null}
    </div>
  )
}
