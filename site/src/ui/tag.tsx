import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

/**
 * `success` and `warning` are deprecated aliases kept only so untouched pages
 * keep compiling: success renders like brand (blue) and warning like neutral.
 * Page components should express those semantics with their own dot + text.
 */
export type TagTone = 'neutral' | 'brand' | 'success' | 'warning' | 'danger'

const tones: Record<TagTone, string> = {
  neutral: 'bg-white/60 text-ink-2 ring-1 ring-inset ring-line',
  brand: 'bg-brand-soft text-brand-ink',
  success: 'bg-brand-soft text-brand-ink',
  warning: 'bg-white/60 text-ink-2 ring-1 ring-inset ring-line',
  danger: 'bg-danger/10 text-danger',
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
        'inline-flex h-[22px] items-center gap-1.5 rounded-full px-2.5 text-[12px] leading-none',
        tones[tone],
        className,
      )}
    >
      {dot ? <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" /> : null}
      {children}
    </span>
  )
}
