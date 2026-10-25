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

/** A label + sans tabular number. Group several with hairline separators. */
export function Stat({ label, value, hint, tone = 'default', className }: StatProps) {
  return (
    <div className={cn('min-w-0', className)}>
      <div className="text-[13px] leading-5 text-ink-3">{label}</div>
      <div
        className={cn(
          'mt-1.5 text-[32px] font-semibold leading-tight tracking-tight tabular-nums',
          toneClass[tone],
        )}
      >
        {value}
      </div>
      {hint ? <div className="mt-1.5 text-[13px] leading-5 text-ink-3">{hint}</div> : null}
    </div>
  )
}
