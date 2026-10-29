import type { ReactNode } from 'react'

import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import { ModelName } from '@/ui/model-name'
import { cn } from '@/lib/cn'

/** Formats a number with standard commas (e.g. 294,938). */
export function formatNum(n: number | undefined | null): string {
  if (n == null) return '0'
  return Number(n).toLocaleString('en-US')
}

/** Formats ms duration into human readable seconds/minutes string:
 * - 0 or negative -> "—"
 * - < 1000ms -> "0.42s"
 * - < 60000ms -> "21.0s"
 * - >= 60000ms -> "1m 16s"
 */
export function formatDuration(ms: number): string {
  if (ms <= 0) return '—'
  const totalSeconds = ms / 1000
  if (totalSeconds < 1) {
    return `${totalSeconds.toFixed(2)}s`
  }
  if (totalSeconds < 60) {
    return `${totalSeconds.toFixed(1)}s`
  }
  const minutes = Math.floor(totalSeconds / 60)
  const remainderSeconds = Math.round(totalSeconds % 60)
  return `${minutes}m ${remainderSeconds}s`
}

/** Status color thresholds for first token latency:
 * < 1s: emerald
 * 1s - 3s: amber
 * > 3s: rose
 */
export function getFirstTokenColor(ms: number): {
  text: string
  bar: string
} {
  if (ms <= 0) {
    return {
      text: 'text-ink-3',
      bar: 'bg-slate-300 dark:bg-slate-700',
    }
  }
  if (ms < 1000) {
    return {
      text: 'text-emerald-600 dark:text-emerald-400',
      bar: 'bg-emerald-500',
    }
  }
  if (ms <= 3000) {
    return {
      text: 'text-amber-600 dark:text-amber-400',
      bar: 'bg-amber-500',
    }
  }
  return {
    text: 'text-rose-600 dark:text-rose-400',
    bar: 'bg-rose-500',
  }
}

/** Status color thresholds for total latency:
 * < 3s: emerald
 * 3s - 10s: amber
 * > 10s: rose
 */
export function getTotalLatencyColor(ms: number): {
  text: string
  bar: string
} {
  if (ms <= 0) {
    return {
      text: 'text-ink-3',
      bar: 'bg-slate-300 dark:bg-slate-700',
    }
  }
  if (ms < 3000) {
    return {
      text: 'text-emerald-600 dark:text-emerald-400',
      bar: 'bg-emerald-500',
    }
  }
  if (ms <= 10000) {
    return {
      text: 'text-amber-600 dark:text-amber-400',
      bar: 'bg-amber-500',
    }
  }
  return {
    text: 'text-rose-600 dark:text-rose-400',
    bar: 'bg-rose-500',
  }
}

/** Header label with sort indicator "⇅" */
export function TableSortHeader({ title, numeric = false }: { title: string; numeric?: boolean }) {
  return (
    <span className={cn('inline-flex items-center gap-1 select-none', numeric && 'justify-end')}>
      <span>{title}</span>{' '}
      <span className="text-[11px] text-ink-3/40 font-mono font-normal">⇅</span>
    </span>
  )
}

/** Model pill capsule with vendor icon and optional route/converted marker */
export function ModelPillBadge({
  model,
  converted = false,
  className,
}: {
  model: string
  converted?: boolean
  className?: string
}) {
  return (
    <div className={cn('inline-flex items-center gap-1.5', className)}>
      <span className="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface/90 px-2.5 py-1 text-meta font-medium text-ink shadow-2xs backdrop-blur-xs">
        <ModelName model={model} size="sm" />
      </span>
      {converted && (
        <span
          className="inline-flex items-center text-ink-3 hover:text-ink transition-colors"
          title="模型转换"
        >
          {/* ⇄ icon */}
          <svg
            className="h-3.5 w-3.5 stroke-current"
            viewBox="0 0 24 24"
            fill="none"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="m16 3 4 4-4 4" />
            <path d="M20 7H4" />
            <path d="m8 21-4-4 4-4" />
            <path d="M4 17h16" />
          </svg>
          <span className="sr-only">转换</span>
        </span>
      )}
    </div>
  )
}

/** Channel/seller pill badge with sparkle icon and optional subtitle */
export function ChannelPillBadge({
  id,
  subtext,
  variant = 'blue',
  isInProgress = false,
}: {
  id?: string | number
  subtext?: ReactNode
  variant?: 'blue' | 'amber'
  isInProgress?: boolean
}) {
  if (isInProgress) {
    return (
      <div className="inline-flex w-fit items-center">
        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-caption font-medium border shadow-2xs bg-blue-50 text-brand border-brand/20 dark:bg-blue-950/40 dark:text-blue-300 dark:border-blue-800/50">
          进行中
        </span>
      </div>
    )
  }

  const isBlue = variant === 'blue'
  return (
    <div className="flex flex-col gap-0.5">
      <div className="inline-flex w-fit items-center gap-1">
        <span
          className={cn(
            'inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-caption font-medium border shadow-2xs',
            isBlue
              ? 'bg-blue-50 text-brand border-brand/20 dark:bg-blue-950/40 dark:text-blue-300 dark:border-blue-800/50'
              : 'bg-amber-50 text-amber-700 border-amber-200/80 dark:bg-amber-950/40 dark:text-amber-300 dark:border-amber-800/50'
          )}
        >
          <span>#{id}</span>
          <svg
            className="h-2.5 w-2.5 fill-amber-400 text-amber-500 shrink-0"
            viewBox="0 0 24 24"
            fill="currentColor"
            aria-hidden="true"
          >
            <path d="M12 2l2.4 7.4 7.6 2.6-7.6 2.6L12 22l-2.4-7.4L2 12l7.6-2.6L12 2z" />
          </svg>
        </span>
      </div>
      {subtext && (
        <span className="text-caption text-ink-3 truncate max-w-[150px] leading-tight">
          {subtext}
        </span>
      )}
    </div>
  )
}

/** Tokens display:
 * Line 1: `input / output`
 * Line 2: `缓存↓ read` and/or `↑ write`
 */
export function TokensMetricCell({
  input,
  output,
  cacheRead = 0,
  cacheWrite = 0,
  isInProgress = false,
}: {
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: number
  isInProgress?: boolean
}) {
  const isZero = input === 0 && output === 0
  const hasCache = cacheRead > 0 || cacheWrite > 0

  if (isInProgress || isZero) {
    return <span className="font-mono text-meta text-ink-3">-</span>
  }

  return (
    <div className="flex flex-col gap-0.5">
      <span className="font-mono text-meta font-medium tabular-nums text-ink leading-tight">
        {formatNum(input)} / {formatNum(output)}
      </span>
      {hasCache && (
        <div className="flex items-center gap-1.5 text-[11px] text-ink-3 tabular-nums leading-tight">
          {cacheRead > 0 && <span>缓存↓ {formatNum(cacheRead)}</span>}
          {cacheWrite > 0 && <span>↑ {formatNum(cacheWrite)}</span>}
        </div>
      )}
    </div>
  )
}

/** Dual-color vertical indicator bar + First token & Total duration */
export function TimingMetricCell({
  firstTokenMs,
  latencyMs,
  isStream,
  isInProgress = false,
}: {
  firstTokenMs: number
  latencyMs: number
  isStream: boolean
  isInProgress?: boolean
}) {
  if (isInProgress) {
    const durationText =
      formatDuration(latencyMs) === '—' ? '0.0s' : formatDuration(latencyMs)
    return (
      <div className="flex items-stretch gap-2">
        <span
          aria-hidden="true"
          className="flex w-1 shrink-0 flex-col overflow-hidden rounded-full my-0.5 bg-amber-500"
        />
        <div className="flex min-w-0 flex-col justify-center gap-0.5 text-xs leading-tight tabular-nums">
          <div className="flex items-baseline gap-1.5">
            <span className="text-ink-3 shrink-0">耗时</span>
            <span className="font-medium text-amber-600 dark:text-amber-400">
              {durationText}
            </span>
          </div>
        </div>
      </div>
    )
  }

  const showFirstToken = isStream
  const ftColors = getFirstTokenColor(firstTokenMs)
  const totalColors = getTotalLatencyColor(latencyMs)

  const firstTokenText =
    !showFirstToken || firstTokenMs <= 0
      ? '—'
      : formatDuration(firstTokenMs)

  const totalTimeText = formatDuration(latencyMs)

  return (
    <div className="flex items-stretch gap-2">
      {/* Dual-color vertical bar */}
      <span
        aria-hidden="true"
        className={cn(
          'flex w-1 shrink-0 flex-col overflow-hidden rounded-full my-0.5',
          !showFirstToken && totalColors.bar
        )}
      >
        {showFirstToken && (
          <>
            <span className={cn('flex-1 min-h-[14px]', ftColors.bar)} />
            <span className={cn('flex-1 min-h-[14px]', totalColors.bar)} />
          </>
        )}
      </span>

      {/* Two-row text */}
      <div className="flex min-w-0 flex-col justify-center gap-0.5 text-xs leading-tight tabular-nums">
        {showFirstToken && (
          <div className="flex items-baseline gap-1.5">
            <span className="text-ink-3 shrink-0">首字</span>
            <span className={cn('font-medium', ftColors.text)}>
              {firstTokenText}
            </span>
          </div>
        )}
        <div className="flex items-baseline gap-1.5">
          <span className="text-ink-3 shrink-0">耗时</span>
          <span className={cn('font-medium', totalColors.text)}>
            {totalTimeText}
          </span>
        </div>
      </div>
    </div>
  )
}

/** Cost capsule badge with document icon */
export function CostPillBadge({
  amountMicro,
  className,
}: {
  amountMicro: number | string
  className?: string
}) {
  const formatted = formatYuan(amountMicro)
  const display = formatted === '¥0' ? '¥ 0' : formatted
  return (
    <div
      className={cn(
        'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md border border-line bg-surface/90 font-mono text-meta font-medium shadow-2xs tabular-nums text-ink backdrop-blur-xs',
        className
      )}
    >
      <span>{display}</span>
      {/* File/document icon */}
      <svg
        className="h-3.5 w-3.5 text-ink-3/70 shrink-0"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
        <polyline points="14 2 14 8 20 8" />
        <line x1="16" y1="13" x2="8" y2="13" />
        <line x1="16" y1="17" x2="8" y2="17" />
      </svg>
    </div>
  )
}

/** Income badge for seller records */
export function IncomePillBadge({
  sellerCreditMicro,
  buyerPaidMicro,
  platformFeeMicro,
}: {
  sellerCreditMicro: number | string
  buyerPaidMicro?: number | string
  platformFeeMicro?: number | string
}) {
  const isZero = !sellerCreditMicro || Number(sellerCreditMicro) === 0
  const formatted = formatYuan(sellerCreditMicro)

  return (
    <div className="flex flex-col gap-0.5">
      <div className="inline-flex w-fit items-center">
        {isZero ? (
          <span className="font-mono font-medium text-meta tabular-nums text-emerald-600 dark:text-emerald-400">
            ¥0
          </span>
        ) : (
          <span className="inline-flex items-center px-2 py-0.5 rounded-md bg-cyan-50 text-cyan-700 border border-cyan-200/80 dark:bg-cyan-950/40 dark:text-cyan-300 dark:border-cyan-800/50 font-mono font-medium text-meta tabular-nums shadow-2xs">
            {formatted}
          </span>
        )}
      </div>
      {!isZero && buyerPaidMicro != null && platformFeeMicro != null && (
        <span className="text-[11px] text-ink-3 tabular-nums truncate max-w-[180px]">
          实付 {formatYuan(buyerPaidMicro)} · 平台费 {formatYuan(platformFeeMicro)}
        </span>
      )}
    </div>
  )
}

/** Time and Status combined cell */
export function TimeAndStatusCell({
  createdAt,
  status,
}: {
  createdAt: string
  status: string
}) {
  const isFailed = status === 'failed'

  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="font-mono text-caption text-ink-2 tabular-nums whitespace-nowrap">
        {formatDateTime(createdAt)}
      </span>
      <div className="inline-flex w-fit items-center">
        {isFailed ? (
          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-rose-50 text-rose-700 border border-rose-200/80 dark:bg-rose-950/40 dark:text-rose-300">
            失败
          </span>
        ) : (
          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-emerald-50 text-emerald-700 border border-emerald-200/80 dark:bg-emerald-950/40 dark:text-emerald-300">
            消耗
            <span className="sr-only">成功</span>
          </span>
        )}
      </div>
    </div>
  )
}
