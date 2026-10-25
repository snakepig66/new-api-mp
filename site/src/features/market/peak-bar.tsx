import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatTime, parseApiTime, type ApiTime } from '@/lib/time'

/**
 * The weekday billing windows in Asia/Shanghai, in minutes from midnight.
 * Weekends and public holidays are off-peak all day (see docs billing).
 */
export const PEAK_WINDOWS: { start: number; end: number }[] = [
  { start: 9 * 60, end: 12 * 60 },
  { start: 14 * 60, end: 18 * 60 },
]

const DAY_MINUTES = 24 * 60

const shanghaiClock = new Intl.DateTimeFormat('en-US', {
  timeZone: 'Asia/Shanghai',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

/** Minutes since midnight in Asia/Shanghai, independent of the browser's zone. */
export function shanghaiMinuteOfDay(now: Date = new Date()): number {
  const parts: Record<string, string> = {}
  for (const part of shanghaiClock.formatToParts(now)) {
    if (part.type !== 'literal') parts[part.type] = part.value
  }
  return Number(parts.hour) * 60 + Number(parts.minute)
}

export interface PeakBarProps {
  /** RFC 3339 string or Unix seconds of the next tier switch. */
  nextSwitchAt?: ApiTime | null
  className?: string
}

/**
 * One 24-hour rail: orange peak windows, grey off-peak, a blue "now" marker.
 * The marker ticks every minute so the page does not re-render every second.
 */
export function PeakBar({ nextSwitchAt = null, className }: PeakBarProps) {
  const [minute, setMinute] = useState(() => shanghaiMinuteOfDay())

  useEffect(() => {
    const timer = window.setInterval(() => setMinute(shanghaiMinuteOfDay()), 60_000)
    return () => window.clearInterval(timer)
  }, [])

  const nowPercent = (minute / DAY_MINUTES) * 100
  const next = nextSwitchAt === null ? null : parseApiTime(nextSwitchAt)

  return (
    <div className={cn('min-w-0', className)}>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-[13px] text-ink-3">
        <span>
          {copy.pages.modelDetail.peakBar.peak} 09:00–12:00 · 14:00–18:00
        </span>
        {next ? (
          <span className="tabular-nums">
            {copy.pages.modelDetail.peakBar.next} {formatTime(next)}
          </span>
        ) : null}
      </div>

      <div className="relative mt-3 h-2.5 rounded-full bg-bg-soft" aria-hidden="true">
        {PEAK_WINDOWS.map((window) => (
          <span
            key={window.start}
            className="absolute inset-y-0 bg-warn/70"
            style={{
              left: `${(window.start / DAY_MINUTES) * 100}%`,
              width: `${((window.end - window.start) / DAY_MINUTES) * 100}%`,
            }}
          />
        ))}
        <span
          className="absolute -top-1 bottom-[-4px] w-0.5 -translate-x-1/2 rounded-full bg-brand"
          style={{ left: `${nowPercent}%` }}
        />
      </div>

      <div className="mt-2 flex items-center justify-between text-[12px] text-ink-3">
        <span>00:00</span>
        <span>06:00</span>
        <span>12:00</span>
        <span>18:00</span>
        <span>24:00</span>
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-ink-2">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-warn" aria-hidden="true" />
          {copy.pages.modelDetail.peakBar.peak}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-ink-3" aria-hidden="true" />
          {copy.pages.modelDetail.peakBar.offpeak}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-3 w-0.5 rounded-full bg-brand" aria-hidden="true" />
          {copy.pages.modelDetail.peakBar.now}
        </span>
      </div>

      <p className="mt-2 text-[13px] leading-6 text-ink-3">
        {copy.pages.modelDetail.peakBar.note}
      </p>
    </div>
  )
}
