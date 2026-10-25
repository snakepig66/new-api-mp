import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export interface LogoMarkProps {
  /** Wordmark font size in px; the blue dot stays 8px. */
  size?: number
  className?: string
}

/**
 * Pure-text wordmark: the first two characters of the brand name, tightened,
 * with one 8px brand dot. No SVG.
 */
export function LogoMark({ size = 20, className }: LogoMarkProps) {
  const mark = copy.brand.name.slice(0, 2)

  return (
    <span className={cn('inline-flex items-start text-ink', className)} aria-hidden="true">
      <span
        style={{ fontSize: `${size}px`, lineHeight: 1 }}
        className="font-semibold tracking-[-0.03em]"
      >
        {mark}
      </span>
      <span className="ml-1 mt-0.5 h-2 w-2 shrink-0 rounded-full bg-brand" />
    </span>
  )
}

export interface LogoProps {
  className?: string
  markSize?: number
  /** Optional quiet suffix rendered after the wordmark. */
  subtitle?: string
}

/** Full wordmark: brand name from copy.ts plus the brand dot. */
export function Logo({ className, markSize = 20, subtitle }: LogoProps) {
  return (
    <span className={cn('inline-flex items-start gap-2', className)}>
      <span className="inline-flex items-start">
        <span
          style={{ fontSize: `${markSize}px`, lineHeight: 1 }}
          className="font-semibold tracking-[-0.03em] text-ink"
        >
          {copy.brand.name}
        </span>
        <span className="ml-1 mt-0.5 h-2 w-2 shrink-0 rounded-full bg-brand" aria-hidden="true" />
      </span>
      {subtitle ? (
        <span className="text-[13px] leading-5 text-ink-3">{subtitle}</span>
      ) : null}
    </span>
  )
}
