import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

export type TagTone = 'neutral' | 'brand' | 'success' | 'warning' | 'danger'

const tones: Record<TagTone, string> = {
  neutral: 'border-line bg-wash text-muted',
  brand: 'border-brand/25 bg-brand-soft text-brand-dark',
  success: 'border-success/20 bg-success-soft text-success',
  warning: 'border-warning/20 bg-warning-soft text-warning',
  danger: 'border-danger/20 bg-danger-soft text-danger',
}

export interface TagProps {
  children: ReactNode
  tone?: TagTone
  dot?: boolean
  className?: string
}

export function Tag({ children, tone = 'neutral', dot = false, className }: TagProps) {
  return (
    <span
      className={cn(
        'inline-flex h-5 items-center gap-1 rounded-[3px] border px-1.5 text-[11px] leading-none',
        tones[tone],
        className,
      )}
    >
      {dot ? <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" /> : null}
      {children}
    </span>
  )
}
