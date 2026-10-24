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
        'inline-flex h-5 items-center gap-1.5 rounded-[3px] border px-1.5 text-[11px] leading-none',
        isPeak
          ? 'border-warning/25 bg-warning-soft text-warning'
          : 'border-success/20 bg-success-soft text-success',
        className,
      )}
      title={
        countdown
          ? `${copy.peak[tier]} ${copy.peak.nextSwitch} ${countdown}`
          : copy.peak[tier]
      }
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />
      <span>{copy.peak[tier]}</span>
      {countdown ? (
        <span className="border-l border-current/25 pl-1.5 font-numeric text-[10.5px] tabular-nums opacity-85">
          {copy.peak.nextSwitch} {countdown}
        </span>
      ) : null}
    </span>
  )
}
