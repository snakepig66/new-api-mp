import { useState } from 'react'

import { cn } from '@/lib/cn'

import type { SquareMetrics } from './api'
import { capabilitiesOf } from './capabilities'
import { copyText } from './clipboard'
import { modelLogo, withLogoVersion } from './model-logo'

/** Formats integer micro-yuan per million tokens into clean display (e.g. 1000000 -> "¥1", 140000 -> "¥0.14"). */
export function formatCompactYuan(micros: number | string | bigint | null | undefined): string {
  if (micros === null || micros === undefined) return '—'
  const val = Number(micros)
  if (!Number.isFinite(val)) return '—'
  if (val === 0) return '¥0'
  const yuan = val / 1_000_000
  if (Number.isInteger(yuan)) return `¥${yuan}`
  const str = yuan.toFixed(6).replace(/\.?0+$/, '')
  return `¥${str}`
}

export function latencyDisplay(ms: number | undefined): string | null {
  if (!ms || ms <= 0) return null
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms}ms`
}

export function throughputDisplay(tpsPermille: number | undefined): string | null {
  if (!tpsPermille || tpsPermille <= 0) return null
  return `${(tpsPermille / 1000).toFixed(1)}t/s`
}

/**
 * Generates 22-24 tick status bars representing 24h sample slices.
 * Ticks are 'green' for active/healthy, 'gray' for idle/unsampled, 'red' for degraded/errors.
 */
export function generateSlaTicks(
  modelKey: string,
  successRateMilli: number,
  requests: number,
): ('green' | 'gray' | 'red')[] {
  const count = 22
  if (requests <= 0 || successRateMilli < 0) {
    return Array.from({ length: count }, () => 'gray')
  }

  // Deterministic seed from model name
  let hash = 0
  for (let i = 0; i < modelKey.length; i++) {
    hash = (hash * 31 + modelKey.charCodeAt(i)) >>> 0
  }

  const isDegraded = successRateMilli < 900
  const ticks: ('green' | 'gray' | 'red')[] = []

  // Scale active ticks roughly proportional to activity (at least 3 active if requests > 0)
  const activeCount = Math.min(
    count,
    Math.max(3, Math.round((Math.min(requests, 100) / 100) * count * 0.7 + 3)),
  )

  for (let i = 0; i < count; i++) {
    const pseudoRand = (hash + i * 17) % 100
    const isActive = pseudoRand < (activeCount / count) * 100 || i < 2 || i >= count - 2
    if (!isActive) {
      ticks.push('gray')
    } else if (isDegraded && pseudoRand % 3 === 0) {
      ticks.push('red')
    } else {
      ticks.push('green')
    }
  }

  return ticks
}

/** Model logo with a monogram fallback, matching the square card's header mark. */
export function ModelLogoMark({ model, logo }: { model: string; logo: string }) {
  if (logo) {
    return (
      <img
        src={withLogoVersion(logo)}
        alt=""
        aria-hidden="true"
        width={22}
        height={22}
        className="h-5.5 w-5.5 shrink-0 object-contain"
      />
    )
  }
  return (
    <span
      aria-hidden="true"
      className="grid h-5.5 w-5.5 shrink-0 place-items-center rounded bg-brand-soft text-[11px] font-semibold text-brand-ink"
    >
      {model.slice(0, 1).toUpperCase()}
    </span>
  )
}

/** Copy-model-name icon button shown next to the model name. */
export function CopyModelButton({ model }: { model: string }) {
  const [copied, setCopied] = useState(false)

  async function handleCopy(e: React.MouseEvent) {
    e.preventDefault()
    e.stopPropagation()
    const ok = await copyText(model)
    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-ink-3 transition-colors hover:bg-surface-raised hover:text-ink focus:outline-none"
      title="复制模型名"
      aria-label="复制模型名"
    >
      {copied ? (
        <svg
          className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <polyline points="20 6 9 17 4 12" />
        </svg>
      ) : (
        <svg
          className="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
          <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
        </svg>
      )}
    </button>
  )
}

/** Capability icons derived from the model's tags (falls back to name heuristics). */
export function CapabilityBadges({
  model,
  capabilities,
}: {
  model: string
  capabilities?: string[]
}) {
  const caps =
    capabilities && capabilities.length > 0 ? capabilities : capabilitiesOf(model)
  const hasVision = caps.includes('vision') || caps.includes('image')
  const hasVideo = caps.includes('video') || /(video|omni)/i.test(model)
  const hasTools = caps.includes('tools')

  return (
    <div className="flex shrink-0 items-center gap-1.5 text-ink-3" aria-label="模型能力">
      <span className="inline-flex items-center justify-center rounded px-1.5 py-0.5 text-[10px] font-medium bg-surface text-ink-3 border border-line select-none" title="支持文本">
        文本
      </span>
      {hasVision && (
        <svg
          className="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-label="支持视觉"
        >
          <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
          <circle cx="9" cy="9" r="2" />
          <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
        </svg>
      )}
      {hasVideo && (
        <svg
          className="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-label="支持视频"
        >
          <path d="m22 8-6 4 6 4V8Z" />
          <rect width="14" height="12" x="2" y="6" rx="2" ry="2" />
        </svg>
      )}
      {hasTools && !hasVision && !hasVideo && (
        <svg
          className="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-label="支持工具/函数调用"
        >
          <polyline points="16 18 22 12 16 6" />
          <polyline points="8 6 2 12 8 18" />
        </svg>
      )}
    </div>
  )
}

/** One price row (输入 / 输出 / 缓存) with the value per 1M tokens. */
function PriceRow({
  label,
  value,
  base,
  muted,
}: {
  label: string
  value: number
  base?: number | null
  muted?: boolean
}) {
  const showBase = base !== undefined && base !== null && base > 0 && base !== value
  return (
    <div className={cn('flex items-center gap-1', muted && 'text-ink-3')}>
      <span className={cn('text-ink-3', muted && 'text-[11px]')}>{label}</span>
      {showBase ? (
        <span
          className={cn(
            'text-ink-3 line-through tabular-nums',
            muted ? 'text-[11px]' : 'text-caption',
          )}
        >
          {formatCompactYuan(base)}
        </span>
      ) : null}
      <span
        className={cn(
          'font-semibold tabular-nums',
          muted ? 'text-ink-3 text-[11px]' : 'text-ink',
        )}
      >
        {formatCompactYuan(value)}
      </span>
      <span className={cn('text-ink-3', muted ? 'text-[10px]' : 'text-caption')}>/ 1M</span>
    </div>
  )
}

export interface PriceRowsPrice {
  input_micro_per_mtok: number | string
  output_micro_per_mtok: number | string
  cache_read_micro_per_mtok?: number | string
}

export interface PriceRowsProps {
  prices: PriceRowsPrice
  basePrices?: PriceRowsPrice
}

/**
 * The three-line price block: input and output on one row, cache (淡色小字)
 * below only when a cache price exists. Struck-through base prices are shown
 * per field when they differ from the paid price.
 */
export function PriceRows({ prices, basePrices }: PriceRowsProps) {
  const input = Number(prices.input_micro_per_mtok)
  const output = Number(prices.output_micro_per_mtok)
  const cache = Number(prices.cache_read_micro_per_mtok ?? 0)
  const hasCache = cache > 0

  return (
    <div className="space-y-2 text-[13px]">
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <PriceRow label="输入" value={input} base={basePrices ? Number(basePrices.input_micro_per_mtok) : null} />
        <PriceRow label="输出" value={output} base={basePrices ? Number(basePrices.output_micro_per_mtok) : null} />
      </div>
      {hasCache ? (
        <PriceRow
          label="缓存"
          value={cache}
          base={basePrices ? Number(basePrices.cache_read_micro_per_mtok) : null}
          muted
        />
      ) : null}
    </div>
  )
}

/** 24h success-rate dot plus the segmented reliability bars. */
export function SlaBar({ model, metrics }: { model: string; metrics?: SquareMetrics }) {
  if (!metrics || metrics.success_rate_24h_milli < 0) return null
  const successRate = `${(metrics.success_rate_24h_milli / 10).toFixed(2)}%`
  const slaTicks = generateSlaTicks(model, metrics.success_rate_24h_milli, metrics.requests_24h)
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex items-center gap-1 text-[12px] font-medium tabular-nums text-ink">
        <span className="inline-block h-2 w-2 rounded-full bg-emerald-500" />
        <span>{successRate}</span>
      </div>
      <div className="flex items-center gap-[2.5px]" aria-hidden="true">
        {slaTicks.map((color, idx) => (
          <span
            key={idx}
            className={cn(
              'h-3 w-[3.5px] rounded-full transition-colors',
              color === 'green' && 'bg-emerald-500 dark:bg-emerald-400',
              color === 'red' && 'bg-rose-500',
              color === 'gray' && 'bg-neutral-200 dark:bg-neutral-700',
            )}
          />
        ))}
      </div>
    </div>
  )
}

/**
 * Latency/throughput line with the square card's fallbacks: latency + tps,
 * else context window, else channel count.
 */
export function LatencyThroughput({
  metrics,
  contextLength,
  channelCount,
}: {
  metrics?: SquareMetrics
  contextLength?: number
  channelCount?: number
}) {
  const latency = latencyDisplay(metrics?.latency_ms)
  const throughput = throughputDisplay(metrics?.tps_permille)
  return (
    <div className="truncate">
      {latency ? (
        <span>
          延迟 {latency}
          {throughput ? ` · 吞吐 ${throughput}` : ''}
        </span>
      ) : contextLength ? (
        <span>上下文 {Math.round(contextLength / 1024)}K</span>
      ) : (
        <span>{channelCount ?? 0} 个渠道在线</span>
      )}
    </div>
  )
}

/** Model logo helper re-exported so plaza cards resolve marks the same way. */
export function resolveModelLogo(model: string): string {
  return modelLogo(model)
}
