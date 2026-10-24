import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

export type StatTone = 'default' | 'brand' | 'success' | 'warning' | 'danger'

const toneClass: Record<StatTone, string> = {
  default: 'text-ink',
  brand: 'text-brand-dark',
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-danger',
}

export interface StatProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: StatTone
  className?: string
}

/** A label + mono number pair. Group several with hairline separators. */
export function Stat({ label, value, hint, tone = 'default', className }: StatProps) {
  return (
    <div className={cn('min-w-0', className)}>
      <div className="text-[12px] text-muted">{label}</div>
      <div
        className={cn(
          'mt-1 font-numeric text-[21px] font-medium leading-7 tabular-nums',
          toneClass[tone],
        )}
      >
        {value}
      </div>
      {hint ? <div className="mt-1 text-[11px] leading-5 text-muted/80">{hint}</div> : null}
    </div>
  )
}
