import { cn } from '@/lib/cn'

/** Three-state health used by the square and the seller console (P0 palette). */
export type StatusLevel = 'available' | 'degraded' | 'offline'

const TONES: Record<StatusLevel, string> = {
  available: 'bg-[var(--color-ok)]',
  degraded: 'bg-warn',
  offline: 'bg-ink-3',
}

const LABELS: Record<StatusLevel, string> = {
  available: '可用',
  degraded: '降级',
  offline: '离线',
}

/**
 * A small filled dot for channel/model health, with an optional label.
 *
 * The dot is the only place these colours appear: success green is a status
 * signal, not a second brand accent (the brand stays #4d6bfe). `unknown`
 * renders the neutral dot without a label so a missing sample never reads as
 * "offline".
 */
export function StatusDot({
  level,
  label,
  showLabel = false,
  className,
}: {
  level: StatusLevel | 'unknown'
  label?: string
  showLabel?: boolean
  className?: string
}) {
  const tone = level === 'unknown' ? 'bg-line-strong' : TONES[level]
  const text = label ?? (level === 'unknown' ? '' : LABELS[level])
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-[13px] text-ink-3', className)}>
      <span aria-hidden="true" className={cn('h-2 w-2 shrink-0 rounded-full', tone)} />
      {showLabel && text ? <span>{text}</span> : null}
      {!showLabel && text ? <span className="sr-only">{text}</span> : null}
    </span>
  )
}
