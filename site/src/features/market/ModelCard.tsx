import { Link } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import type { SquareModel, SquareModelDetail, SquareTierPrices } from './api'
import { AddToKeyMenu } from './AddToKeyMenu'
import { capabilitiesOf } from './capabilities'
import { copyText } from './clipboard'
import { modelLogo, withLogoVersion } from './model-logo'
import { vendorLabel, vendorLogo, type VendorLookup } from './vendors'

export interface ModelCardProps {
  model: SquareModel
  detail?: SquareModelDetail
  vendorMeta?: VendorLookup
  showAddToKey?: boolean
  className?: string
}

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices.flat
}

function originVendorOf(model: SquareModel): string {
  return model.origin_vendor || model.vendor
}

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

function calculateDiscountPercent(officialMicro: number, currentMicro: number): number | null {
  if (officialMicro <= 0 || currentMicro <= 0 || officialMicro <= currentMicro) return null
  const pct = Math.round((1 - currentMicro / officialMicro) * 100)
  return pct > 0 && pct < 100 ? pct : null
}

function latencyDisplay(ms: number | undefined): string | null {
  if (!ms || ms <= 0) return null
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms}ms`
}

function throughputDisplay(tpsPermille: number | undefined): string | null {
  if (!tpsPermille || tpsPermille <= 0) return null
  return `${(tpsPermille / 1000).toFixed(1)}t/s`
}

/**
 * Generates 22-24 tick status bars representing 24h sample slices.
 * Ticks are 'green' for active/healthy, 'gray' for idle/unsampled, 'red' for degraded/errors.
 */
function generateSlaTicks(modelKey: string, successRateMilli: number, requests: number): ('green' | 'gray' | 'red')[] {
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
  const activeCount = Math.min(count, Math.max(3, Math.round(Math.min(requests, 100) / 100 * count * 0.7 + 3)))

  for (let i = 0; i < count; i++) {
    const pseudoRand = ((hash + i * 17) % 100)
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

export function ModelCard({
  model,
  detail,
  vendorMeta,
  showAddToKey = false,
  className,
}: ModelCardProps) {
  const [copied, setCopied] = useState(false)
  const origin = originVendorOf(model)
  const row = currentPrices(model)
  const official = detail?.official_prices?.flat
  const metrics = model.metrics
  const logo = modelLogo(model.model) || vendorLogo(origin, vendorMeta)

  const capabilities = model.capabilities && model.capabilities.length > 0
    ? model.capabilities
    : capabilitiesOf(model.model)

  const hasVision = capabilities.includes('vision') || capabilities.includes('image')
  const hasVideo = capabilities.includes('video') || /(video|omni)/i.test(model.model)
  const hasTools = capabilities.includes('tools')

  async function handleCopy(e: React.MouseEvent) {
    e.preventDefault()
    e.stopPropagation()
    const ok = await copyText(model.model)
    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  // Pricing analysis
  const inputCur = row ? Number(row.input_micro_per_mtok) : 0
  const outputCur = row ? Number(row.output_micro_per_mtok) : 0
  const cacheCur = row ? Number(row.cache_read_micro_per_mtok) : 0

  const inputOff = official ? Number(official.input_micro_per_mtok) : 0
  const outputOff = official ? Number(official.output_micro_per_mtok) : 0
  const cacheOff = official ? Number(official.cache_read_micro_per_mtok) : 0

  const inputDiscount = calculateDiscountPercent(inputOff, inputCur)
  const outputDiscount = calculateDiscountPercent(outputOff, outputCur)
  const cacheDiscount = calculateDiscountPercent(cacheOff, cacheCur)
  const hasCache = cacheCur > 0 || cacheOff > 0

  const hasDiscountPricing = Boolean(
    inputDiscount !== null || outputDiscount !== null || cacheDiscount !== null,
  )

  const isDynamicPricing = !hasDiscountPricing && model.buyer_price_source === 'market' && model.listing_count > 0

  const latency = latencyDisplay(metrics?.latency_ms)
  const throughput = throughputDisplay(metrics?.tps_permille)

  const successRate = metrics && metrics.success_rate_24h_milli >= 0
    ? `${(metrics.success_rate_24h_milli / 10).toFixed(2)}%`
    : null

  const slaTicks = metrics && metrics.success_rate_24h_milli >= 0
    ? generateSlaTicks(model.model, metrics.success_rate_24h_milli, metrics.requests_24h)
    : null

  return (
    <div
      className={cn(
        'group relative flex flex-col justify-between rounded-2xl border border-line bg-surface p-5 transition-all',
        'hover:border-brand/40 hover:shadow-md hover:-translate-y-0.5',
        className,
      )}
    >
      <div>
        {/* Header: Logo, Model Name, Copy Icon, Capabilities */}
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2.5">
            {logo ? (
              <img
                src={withLogoVersion(logo)}
                alt=""
                aria-hidden="true"
                width={22}
                height={22}
                className="h-5.5 w-5.5 shrink-0 object-contain"
              />
            ) : (
              <span
                aria-hidden="true"
                className="grid h-5.5 w-5.5 shrink-0 place-items-center rounded bg-brand-soft text-[11px] font-semibold text-brand-ink"
              >
                {model.model.slice(0, 1).toUpperCase()}
              </span>
            )}
            <Link
              to="/models/$id"
              params={{ id: model.model }}
              className="truncate text-[15px] font-semibold text-ink transition-colors group-hover:text-brand-ink"
              title={model.model}
            >
              {model.model}
            </Link>
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
          </div>

          {/* Capability badges */}
          <div className="flex shrink-0 items-center gap-1.5 text-ink-3" aria-label="模型能力">
            {/* Text badge */}
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
        </div>

        {/* Subtitle: Vendor */}
        <p className="mt-1 text-caption text-ink-3">
          {vendorLabel(origin, vendorMeta)}
        </p>

        {/* Pricing Rows */}
        <div className="mt-4 space-y-2 text-[13px]">
          {row ? (
            hasDiscountPricing ? (
              <>
                {/* Input row */}
                <div className="flex items-center gap-1.5 flex-wrap">
                  <span className="text-ink-3 shrink-0">输入</span>
                  {inputOff > inputCur && (
                    <>
                      <span className="line-through text-ink-3 tabular-nums text-caption">
                        {formatCompactYuan(inputOff)}
                      </span>
                      <span className="text-ink-3 text-caption">→</span>
                    </>
                  )}
                  <span className="font-semibold text-ink tabular-nums">
                    {formatCompactYuan(inputCur)}
                  </span>
                  <span className="text-ink-3 text-caption">/ 1M</span>
                  {inputDiscount !== null && (
                    <span className="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[11px] font-medium bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400">
                      <svg className="h-2.5 w-2.5" viewBox="0 0 16 16" fill="currentColor">
                        <path d="M2 2a1 1 0 0 1 1-1h4.586a1 1 0 0 1 .707.293l6.414 6.414a1 1 0 0 1 0 1.414l-4.586 4.586a1 1 0 0 1-1.414 0L2.293 8.293A1 1 0 0 1 2 7.586V2zm3.5 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z" />
                      </svg>
                      -{inputDiscount}%
                    </span>
                  )}
                </div>

                {/* Output row */}
                <div className="flex items-center gap-1.5 flex-wrap">
                  <span className="text-ink-3 shrink-0">输出</span>
                  {outputOff > outputCur && (
                    <>
                      <span className="line-through text-ink-3 tabular-nums text-caption">
                        {formatCompactYuan(outputOff)}
                      </span>
                      <span className="text-ink-3 text-caption">→</span>
                    </>
                  )}
                  <span className="font-semibold text-ink tabular-nums">
                    {formatCompactYuan(outputCur)}
                  </span>
                  <span className="text-ink-3 text-caption">/ 1M</span>
                  {outputDiscount !== null && (
                    <span className="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[11px] font-medium bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400">
                      <svg className="h-2.5 w-2.5" viewBox="0 0 16 16" fill="currentColor">
                        <path d="M2 2a1 1 0 0 1 1-1h4.586a1 1 0 0 1 .707.293l6.414 6.414a1 1 0 0 1 0 1.414l-4.586 4.586a1 1 0 0 1-1.414 0L2.293 8.293A1 1 0 0 1 2 7.586V2zm3.5 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z" />
                      </svg>
                      -{outputDiscount}%
                    </span>
                  )}
                </div>

                {/* Cache row */}
                {hasCache ? (
                  <div className="flex items-center gap-1.5 flex-wrap">
                    <span className="text-ink-3 shrink-0">缓存</span>
                    {cacheOff > cacheCur && (
                      <>
                        <span className="line-through text-ink-3 tabular-nums text-caption">
                          {formatCompactYuan(cacheOff)}
                        </span>
                        <span className="text-ink-3 text-caption">→</span>
                      </>
                    )}
                    <span className="font-semibold text-ink tabular-nums">
                      {formatCompactYuan(cacheCur)}
                    </span>
                    <span className="text-ink-3 text-caption">/ 1M</span>
                    {cacheDiscount !== null && (
                      <span className="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[11px] font-medium bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400">
                        <svg className="h-2.5 w-2.5" viewBox="0 0 16 16" fill="currentColor">
                          <path d="M2 2a1 1 0 0 1 1-1h4.586a1 1 0 0 1 .707.293l6.414 6.414a1 1 0 0 1 0 1.414l-4.586 4.586a1 1 0 0 1-1.414 0L2.293 8.293A1 1 0 0 1 2 7.586V2zm3.5 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z" />
                        </svg>
                        -{cacheDiscount}%
                      </span>
                    )}
                  </div>
                ) : null}
              </>
            ) : (
              <>
                {/* Dynamic or Official flat pricing */}
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <div className="flex items-center gap-1">
                    <span className="text-ink-3">输入</span>
                    <span className="font-semibold text-ink tabular-nums">{formatCompactYuan(inputCur)}</span>
                    <span className="text-ink-3 text-caption">/ 1M</span>
                  </div>
                  <div className="flex items-center gap-1">
                    <span className="text-ink-3">输出</span>
                    <span className="font-semibold text-ink tabular-nums">{formatCompactYuan(outputCur)}</span>
                    <span className="text-ink-3 text-caption">/ 1M</span>
                  </div>
                </div>
                {hasCache ? (
                  <div className="flex items-center gap-1">
                    <span className="text-ink-3">缓存</span>
                    <span className="font-semibold text-ink tabular-nums">{formatCompactYuan(cacheCur)}</span>
                    <span className="text-ink-3 text-caption">/ 1M</span>
                  </div>
                ) : null}
                {isDynamicPricing ? (
                  <div className="pt-1">
                    <span className="inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400">
                      动态计费
                    </span>
                  </div>
                ) : model.buyer_price_source === 'official' || model.listing_count === 0 ? (
                  <div className="pt-1">
                    <span className="inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium bg-brand-soft text-brand-ink">
                      {copy.pages.models.officialPrice}
                    </span>
                  </div>
                ) : null}
              </>
            )
          ) : (
            <p className="text-meta text-ink-3">{copy.common.none}</p>
          )}
        </div>
      </div>

      {/* Card Bottom: SLA / Metrics & Footer */}
      <div className="mt-5 pt-3 border-t border-line/60 space-y-2">
        {/* SLA Bar */}
        {slaTicks && successRate ? (
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-1 text-[12px] font-medium tabular-nums text-ink">
              <span className="inline-block h-2 w-2 rounded-full bg-emerald-500" />
              <span>{successRate}</span>
            </div>
            {/* Segmented tick bars */}
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
        ) : null}

        {/* Latency & Throughput footer */}
        <div className="flex items-center justify-between text-caption text-ink-3">
          <div className="truncate">
            {latency ? (
              <span>
                延迟 {latency}
                {throughput ? ` · 吞吐 ${throughput}` : ''}
              </span>
            ) : model.context_length ? (
              <span>上下文 {Math.round(model.context_length / 1024)}K</span>
            ) : (
              <span>{model.channel_count} 个渠道在线</span>
            )}
          </div>

          {showAddToKey && (
            <div onClick={(e) => e.stopPropagation()} className="shrink-0 ml-2">
              <AddToKeyMenu model={model.model} hasSupply={model.listing_count > 0} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
