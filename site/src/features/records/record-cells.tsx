import type { ReactNode } from 'react'

import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import { ModelName } from '@/ui/model-name'
import { cn } from '@/lib/cn'

import { getFirstTokenColor, getTotalLatencyColor } from './latency-colors'

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

/** Header label with optional sort indicator "⇅" */
export function TableSortHeader({
  title,
  numeric = false,
  sortable = false,
}: {
  title: string
  numeric?: boolean
  sortable?: boolean
}) {
  return (
    <span className={cn('inline-flex items-center gap-1 select-none', numeric && 'justify-end')}>
      <span>{title}</span>
      {sortable ? <span className="text-[11px] text-ink-3/40 font-mono font-normal">⇅</span> : null}
    </span>
  )
}

/** Model pill capsule with vendor icon and optional route/converted marker */
export function ModelPillBadge({
  model,
  converted = false,
  source,
  className,
}: {
  model: string
  converted?: boolean
  source?: 'plaza' | 'market' | string
  className?: string
}) {
  return (
    <div className={cn('inline-flex items-center gap-1.5 whitespace-nowrap', className)}>
      <span
        className="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface/90 px-2.5 py-1 text-meta font-medium text-ink shadow-2xs backdrop-blur-xs whitespace-nowrap"
        title={model}
      >
        <ModelName model={model} size="sm" className="whitespace-nowrap" />
      </span>
      {source ? (
        <span
          className={cn(
            'inline-flex shrink-0 items-center px-1.5 py-0.5 rounded text-[11px] font-medium border whitespace-nowrap',
            source === 'plaza'
              ? 'bg-brand/10 text-brand-ink border-brand/20'
              : 'bg-surface text-ink-3 border-line',
          )}
        >
          {source === 'plaza' ? '广场' : '市场'}
        </span>
      ) : null}
      {converted && (
        <span
          className="inline-flex shrink-0 items-center text-ink-3 hover:text-ink transition-colors whitespace-nowrap"
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
  cacheHit = false,
  isInProgress = false,
}: {
  id?: string | number
  subtext?: ReactNode
  variant?: 'blue' | 'amber'
  /** 星星只在本次请求命中缓存时出现 */
  cacheHit?: boolean
  isInProgress?: boolean
}) {
  if (isInProgress) {
    return (
      <div className="inline-flex w-fit items-center whitespace-nowrap">
        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-caption font-medium border shadow-2xs bg-blue-50 text-brand border-brand/20 dark:bg-blue-950/40 dark:text-blue-300 dark:border-blue-800/50 whitespace-nowrap">
          进行中
        </span>
      </div>
    )
  }

  const isBlue = variant === 'blue'
  return (
    <div className="flex flex-col gap-0.5 whitespace-nowrap">
      <div className="inline-flex w-fit items-center gap-1 whitespace-nowrap">
        <span
          className={cn(
            'inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-caption font-medium border shadow-2xs whitespace-nowrap',
            isBlue
              ? 'bg-blue-50 text-brand border-brand/20 dark:bg-blue-950/40 dark:text-blue-300 dark:border-blue-800/50'
              : 'bg-amber-50 text-amber-700 border-amber-200/80 dark:bg-amber-950/40 dark:text-amber-300 dark:border-amber-800/50'
          )}
        >
          <span>#{id}</span>
          {cacheHit && (
          <svg
            className="h-2.5 w-2.5 fill-amber-400 text-amber-500 shrink-0"
            viewBox="0 0 24 24"
            fill="currentColor"
            aria-hidden="true"
          >
            <path d="M12 2l2.4 7.4 7.6 2.6-7.6 2.6L12 22l-2.4-7.4L2 12l7.6-2.6L12 2z" />
          </svg>
          )}
        </span>
      </div>
      {subtext && (
        <span
          className="text-caption text-ink-3 truncate max-w-[140px] leading-tight whitespace-nowrap block"
          title={typeof subtext === 'string' ? subtext : undefined}
        >
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
  className,
}: {
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: number
  isInProgress?: boolean
  className?: string
}) {
  const promptTotal = input + cacheRead + cacheWrite
  const isZero = promptTotal === 0 && output === 0

  if (isInProgress || isZero) {
    return <span className="font-mono text-meta text-ink-3 whitespace-nowrap">-</span>
  }

  return (
    <div className={cn('flex flex-col gap-0.5 whitespace-nowrap', className)}>
      <span className="font-mono text-meta font-medium tabular-nums text-ink leading-tight whitespace-nowrap">
        {formatNum(promptTotal)} / {formatNum(output)}
      </span>
      {(cacheRead > 0 || cacheWrite > 0) && (
        <div className="flex items-center gap-1.5 text-[11px] text-ink-3/70 font-mono tabular-nums leading-tight whitespace-nowrap">
          {cacheRead > 0 && <span className="whitespace-nowrap">缓存命中 {formatNum(cacheRead)}</span>}
          {cacheWrite > 0 && <span className="whitespace-nowrap">写入 {formatNum(cacheWrite)}</span>}
        </div>
      )}
    </div>
  )
}

/** Dual-color vertical indicator bar + First token & Total duration */
export function TimingMetricCell({
  firstTokenMs,
  latencyMs,
  outputTokens = 0,
  isStream,
  isInProgress = false,
  className,
}: {
  firstTokenMs: number
  latencyMs: number
  /** Output tokens; used for throughput-based total-duration coloring. */
  outputTokens?: number
  isStream: boolean
  isInProgress?: boolean
  className?: string
}) {
  if (isInProgress) {
    const durationText =
      formatDuration(latencyMs) === '—' ? '0.0s' : formatDuration(latencyMs)
    return (
      <div className={cn('flex items-stretch gap-2 whitespace-nowrap', className)}>
        <span
          aria-hidden="true"
          className="flex w-1 shrink-0 flex-col overflow-hidden rounded-full my-0.5 bg-warn"
        />
        <div className="flex min-w-0 flex-col justify-center gap-0.5 text-xs leading-tight tabular-nums whitespace-nowrap">
          <div className="flex items-baseline gap-1.5 whitespace-nowrap">
            <span className="text-ink-3 shrink-0 whitespace-nowrap">耗时</span>
            <span className="font-medium text-warn whitespace-nowrap">
              {durationText}
            </span>
          </div>
        </div>
      </div>
    )
  }

  const showFirstToken = isStream
  const ftColors = getFirstTokenColor(firstTokenMs)
  const totalColors = getTotalLatencyColor(latencyMs, outputTokens)

  const firstTokenText =
    !showFirstToken || firstTokenMs <= 0
      ? '—'
      : formatDuration(firstTokenMs)

  const totalTimeText = formatDuration(latencyMs)

  return (
    <div className={cn('flex items-stretch gap-2 whitespace-nowrap', className)}>
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
      <div className="flex min-w-0 flex-col justify-center gap-0.5 text-xs leading-tight tabular-nums whitespace-nowrap">
        {showFirstToken && (
          <div className="flex items-baseline gap-1.5 whitespace-nowrap">
            <span className="text-ink-3 shrink-0 whitespace-nowrap">首字</span>
            <span className={cn('font-medium whitespace-nowrap', ftColors.text)}>
              {firstTokenText}
            </span>
          </div>
        )}
        <div className="flex items-baseline gap-1.5 whitespace-nowrap">
          <span className="text-ink-3 shrink-0 whitespace-nowrap">耗时</span>
          <span className={cn('font-medium whitespace-nowrap', totalColors.text)}>
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
        'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md border border-line bg-surface/90 font-mono text-meta font-medium shadow-2xs tabular-nums text-ink backdrop-blur-xs whitespace-nowrap',
        className
      )}
    >
      <span className="whitespace-nowrap">{display}</span>
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
  className,
}: {
  sellerCreditMicro: number | string
  buyerPaidMicro?: number | string
  platformFeeMicro?: number | string
  className?: string
}) {
  const isZero = !sellerCreditMicro || Number(sellerCreditMicro) === 0
  const formatted = formatYuan(sellerCreditMicro)

  return (
    <div className={cn('flex flex-col gap-0.5 whitespace-nowrap', className)}>
      <div className="inline-flex w-fit items-center whitespace-nowrap">
        {isZero ? (
          <span className="font-mono font-medium text-meta tabular-nums text-emerald-600 dark:text-emerald-400 whitespace-nowrap">
            ¥0
          </span>
        ) : (
          <span className="inline-flex items-center px-2 py-0.5 rounded-md bg-cyan-50 text-cyan-700 border border-cyan-200/80 dark:bg-cyan-950/40 dark:text-cyan-300 dark:border-cyan-800/50 font-mono font-medium text-meta tabular-nums shadow-2xs whitespace-nowrap">
            {formatted}
          </span>
        )}
      </div>
      {!isZero && buyerPaidMicro != null && platformFeeMicro != null && (
        <span className="text-[11px] text-ink-3 tabular-nums truncate max-w-[180px] whitespace-nowrap block" title={`实付 ${formatYuan(buyerPaidMicro)} · 平台费 ${formatYuan(platformFeeMicro)}`}>
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
  isSeller = false,
  className,
}: {
  createdAt: string
  status: string
  isSeller?: boolean
  className?: string
}) {
  const isFailed = status === 'failed'

  return (
    <div className={cn('flex min-w-0 flex-col gap-1 whitespace-nowrap', className)}>
      <span className="font-mono text-caption text-ink-2 tabular-nums whitespace-nowrap">
        {formatDateTime(createdAt)}
      </span>
      <div className="inline-flex w-fit items-center whitespace-nowrap">
        {isFailed ? (
          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-rose-50 text-rose-700 border border-rose-200/80 dark:bg-rose-950/40 dark:text-rose-300 whitespace-nowrap">
            失败
          </span>
        ) : (
          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-emerald-50 text-emerald-700 border border-emerald-200/80 dark:bg-emerald-950/40 dark:text-emerald-300 whitespace-nowrap">
            {isSeller ? '成交' : '消耗'}
            <span className="sr-only">成功</span>
          </span>
        )}
      </div>
    </div>
  )
}
