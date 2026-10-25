import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatCountdown, parseApiTime, secondsUntil, type ApiTime } from '@/lib/time'

export type PeakTier = 'peak' | 'offpeak'

export interface PeakBadgeProps {
  tier: PeakTier
  /** RFC 3339 string or Unix seconds of the next tier switch. */
  nextSwitchAt?: ApiTime | null
  className?: string
}

/**
 * Peak / off-peak badge with a live countdown to the next switch.
 * No fill: peak is ink text + an orange dot, off-peak grey text + grey dot.
 * The countdown is deliberately not an aria-live region: it ticks every second.
 */
export function PeakBadge({ tier, nextSwitchAt = null, className }: PeakBadgeProps) {
  const isPeak = tier === 'peak'
  const [now, setNow] = useState(() => Date.now())
  const target = nextSwitchAt === null ? null : parseApiTime(nextSwitchAt)

  useEffect(() => {
    if (!nextSwitchAt) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [nextSwitchAt])

  const remaining = target ? secondsUntil(target, now) : null
  const countdown =
    remaining === null
      ? null
      : remaining <= 0
        ? copy.peak.imminent
        : formatCountdown(remaining)

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 whitespace-nowrap text-[13px] leading-none',
        isPeak ? 'text-ink' : 'text-ink-2',
        className,
      )}
      title={
        countdown
          ? `${copy.peak[tier]} ${copy.peak.nextSwitch} ${countdown}`
          : copy.peak[tier]
      }
    >
      <span
        className={cn('h-1.5 w-1.5 rounded-full', isPeak ? 'bg-warn' : 'bg-ink-3')}
        aria-hidden="true"
      />
      <span>{copy.peak[tier]}</span>
      {countdown ? (
        <span className="text-[13px] tabular-nums text-ink-3">
          {copy.peak.nextSwitch} {countdown}
        </span>
      ) : null}
    </span>
  )
}
