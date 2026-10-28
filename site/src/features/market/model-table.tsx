import { Link } from '@tanstack/react-router'
import { Fragment, useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  StatusDot,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  type StatusLevel,
} from '@/ui'

import type {
  SquareChannel,
  SquareModel,
  SquareModelDetail,
  SquareMetrics,
  SquareTierPrices,
} from './api'
import { AddToKeyMenu } from './AddToKeyMenu'
import { vendorLabel, vendorLogo, type VendorLookup } from './vendors'

export interface ModelTableProps {
  models: SquareModel[]
  /** Detail enrichment for 官方价 and the expanded channel list. */
  details?: Map<string, SquareModelDetail>
  detailsLoading?: boolean
  /** Vendor catalogue metadata; enables display names and logos. */
  vendorMeta?: VendorLookup
  /** Square-only row action; the homepage table stays read-only. */
  showAddToKey?: boolean
  loading?: boolean
  skeletonRows?: number
  minWidth?: number
  /** Rows start expanded (used by tests and deep links). */
  defaultExpanded?: string | null
}

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices.flat
}

/** The standard model's 原厂 slug, with a fallback for older payloads. */
export function originVendorOf(model: SquareModel): string {
  return model.origin_vendor || model.vendor
}

/** A rate in thousandths → "99.2%"; -1 renders the em-less dash placeholder. */
function rateText(milli: number | undefined): string | null {
  if (milli === undefined || milli < 0) return null
  return `${(milli / 10).toFixed(1)}%`
}

function latencyText(ms: number | undefined): string | null {
  if (!ms || ms <= 0) return null
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms`
}

function statusLevel(metrics: SquareMetrics | undefined): StatusLevel | 'unknown' {
  return metrics?.status ?? 'unknown'
}

/** Buyer price of one channel: official × coefficient × multiplier. */
function channelInputPrice(officialInput: number | string | undefined, channel: SquareChannel): number | null {
  const base = Number(officialInput)
  if (!Number.isFinite(base) || base <= 0 || channel.min_multiplier_milli <= 0) return null
  const coefficient = channel.coefficient_milli > 0 ? channel.coefficient_milli : 1000
  return Math.round((base * coefficient * channel.min_multiplier_milli) / 1_000_000)
}

/** Saving versus the official price, in percent. */
function savingOf(official: number | string | undefined, market: number | string | undefined): number | null {
  const o = Number(official)
  const m = Number(market)
  if (!Number.isFinite(o) || o <= 0 || !Number.isFinite(m) || m < 0 || m >= o) return null
  return Math.round((1 - m / o) * 100)
}

function skeletonCell(width = 'w-16') {
  return <div className={`h-3.5 animate-pulse rounded-sm bg-line ${width}`} />
}

function Missing() {
  return <span className="text-ink-3">{copy.common.none}</span>
}

const CAPABILITY_LABELS: Record<string, string> = {
  text: '文本',
  tools: '工具',
  cache: '缓存',
  vision: '视觉',
  image: '图像',
  embedding: '向量',
  reasoning: '推理',
  search: '搜索',
}

function CapabilityTags({ capabilities }: { capabilities?: string[] }) {
  if (!capabilities || capabilities.length === 0) return null
  return (
    <span className="flex flex-wrap items-center gap-1">
      {capabilities.map((cap) => (
        <Tag key={cap}>{CAPABILITY_LABELS[cap] ?? cap}</Tag>
      ))}
    </span>
  )
}

/** One full-width row holding the expansion. */
function ExpandedRow({
  model,
  detail,
  detailLoading,
  officialInput,
  columns,
}: {
  model: SquareModel
  detail?: SquareModelDetail
  detailLoading: boolean
  officialInput: number | string | undefined
  columns: number
}) {
  const t = copy.pages.models.expand
  const official = detail?.official_prices?.flat
  const market = currentPrices(model)
  const rows: { key: string; label: string; official?: number | string; market?: number | string }[] = [
    { key: 'input', label: t.input, official: official?.input_micro_per_mtok, market: market?.input_micro_per_mtok },
    { key: 'cacheRead', label: t.cacheRead, official: official?.cache_read_micro_per_mtok, market: market?.cache_read_micro_per_mtok },
    { key: 'cacheWrite', label: t.cacheWrite, official: official?.cache_write_micro_per_mtok, market: market?.cache_write_micro_per_mtok },
    { key: 'output', label: t.output, official: official?.output_micro_per_mtok, market: market?.output_micro_per_mtok },
  ]
  const metrics = model.metrics
  const rate = (milli: number | undefined) => rateText(milli) ?? t.noSample
  const latency = latencyText(metrics?.latency_ms)
  const tps = metrics && metrics.tps_permille > 0 ? (metrics.tps_permille / 1000).toFixed(1) : null
  const cache = metrics && metrics.cache_hit_permille >= 0 ? `${(metrics.cache_hit_permille / 10).toFixed(0)}%` : null

  return (
    <TableRow interactive={false} className="bg-brand-soft/30">
      <TableCell colSpan={columns} className="px-4 py-5">
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
          <div>
            <h3 className="text-[13px] font-medium text-ink-3">{t.priceTable}</h3>
            <div className="mt-2 overflow-hidden glass-inset rounded-sm">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className="text-ink-3">
                    <th className="px-3 py-2 text-left font-normal">{t.metric}</th>
                    <th className="px-3 py-2 text-right font-normal">{t.official}</th>
                    <th className="px-3 py-2 text-right font-normal">{t.market}</th>
                    <th className="px-3 py-2 text-right font-normal">{copy.pages.models.columns.saving}</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => {
                    const saving = savingOf(row.official, row.market)
                    return (
                      <tr key={row.key} className="border-t border-line">
                        <td className="px-3 py-2 text-ink-2">{row.label}</td>
                        <td className="px-3 py-2 text-right tabular-nums text-ink-3">
                          {row.official === undefined ? t.noSample : formatYuan(row.official)}
                        </td>
                        <td className="px-3 py-2 text-right tabular-nums text-ink">
                          {row.market === undefined ? t.noSample : formatYuan(row.market)}
                        </td>
                        <td className="px-3 py-2 text-right tabular-nums">
                          {saving === null ? (
                            <span className="text-ink-3">{copy.common.none}</span>
                          ) : (
                            <span className="rounded-full bg-brand-soft px-2 py-0.5 text-[12px] text-brand-ink">
                              {copy.pages.models.savingShort(saving)}
                            </span>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>

            <h3 className="mt-5 text-[13px] font-medium text-ink-3">{t.channelsTitle}</h3>
            {detailLoading ? (
              <div className="mt-2 space-y-2">
                <SkeletonLine />
                <SkeletonLine />
              </div>
            ) : detail && detail.channels.length > 0 ? (
              <div className="mt-2 overflow-hidden glass-inset rounded-sm">
                <table className="w-full text-[13px]">
                  <thead>
                    <tr className="text-ink-3">
                      <th className="px-3 py-2 text-left font-normal">{t.channel}</th>
                      <th className="px-3 py-2 text-right font-normal">{t.success}</th>
                      <th className="px-3 py-2 text-right font-normal">{t.latency}</th>
                      <th className="px-3 py-2 text-right font-normal">{t.listings}</th>
                      <th className="px-3 py-2 text-right font-normal">{t.channelPrice}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {detail.channels.map((channel) => {
                      const price = channelInputPrice(officialInput, channel)
                      return (
                        <tr key={channel.channel_slug} className="border-t border-line">
                          <td className="px-3 py-2">
                            <div className="flex min-w-0 items-center gap-2">
                              <span className="truncate text-ink">{channel.name || channel.channel_slug}</span>
                              <Tag tone={channel.kind === 'official' ? 'brand' : 'neutral'}>
                                {channel.kind === 'official'
                                  ? t.officialTag
                                  : channel.kind === 'plan'
                                    ? t.planTag
                                    : t.thirdPartyTag}
                              </Tag>
                              <StatusDot level={channel.metrics?.status ?? 'unknown'} />
                            </div>
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums text-ink-2">
                            {channel.metrics ? rateText(channel.metrics.success_rate_24h_milli) ?? t.noSample : t.noSample}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums text-ink-2">
                            {latencyText(channel.metrics?.latency_ms) ?? t.noSample}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums text-ink-2">
                            {channel.listing_count}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums text-ink">
                            {price === null ? <Missing /> : `${formatYuan(price)}${copy.pages.models.perMillion}`}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="mt-2 text-[13px] text-ink-3">{t.noChannels}</p>
            )}
          </div>

          <div>
            <h3 className="text-[13px] font-medium text-ink-3">{t.reliability}</h3>
            {metrics ? (
              <dl className="mt-2 grid grid-cols-2 gap-x-6 gap-y-2 glass-inset rounded-sm px-4 py-3 text-[13px]">
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.realtime}</dt>
                  <dd className="tabular-nums text-ink">{rate(metrics.success_rate_1h_milli)}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.day24}</dt>
                  <dd className="tabular-nums text-ink">{rate(metrics.success_rate_24h_milli)}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.day7}</dt>
                  <dd className="tabular-nums text-ink">{rate(metrics.success_rate_7d_milli)}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.requests}</dt>
                  <dd className="tabular-nums text-ink">{metrics.requests_24h}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.latency}</dt>
                  <dd className="tabular-nums text-ink">{latency ?? t.noSample}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.tps}</dt>
                  <dd className="tabular-nums text-ink">{tps ?? t.noSample}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.cache}</dt>
                  <dd className="tabular-nums text-ink">{cache ?? t.noSample}</dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-ink-3">{t.lastSuccess}</dt>
                  <dd className="tabular-nums text-ink">{formatTime(metrics.last_success_at) ?? t.noSample}</dd>
                </div>
              </dl>
            ) : (
              <p className="mt-2 text-[13px] text-ink-3">{t.noSample}</p>
            )}

            <dl className="mt-3 flex flex-wrap items-center gap-x-6 gap-y-2 text-[13px]">
              {model.context_length ? (
                <div className="flex items-center gap-2">
                  <dt className="text-ink-3">{t.context}</dt>
                  <dd className="tabular-nums text-ink">{formatContext(model.context_length)}</dd>
                </div>
              ) : null}
              <div className="flex items-center gap-2">
                <dt className="text-ink-3">{t.protocols}</dt>
                <dd className="flex gap-1">
                  <Tag>OpenAI</Tag>
                  <Tag>Anthropic</Tag>
                </dd>
              </div>
              <Link
                to="/models/$id"
                params={{ id: model.model }}
                className="text-brand hover:underline"
              >
                {t.detailLink} →
              </Link>
            </dl>
          </div>
        </div>
      </TableCell>
    </TableRow>
  )
}

function SkeletonLine() {
  return <div className="h-8 w-full animate-pulse rounded-sm bg-line" />
}

function formatTime(value: string | null | undefined): string | null {
  if (!value) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function formatContext(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 === 0 ? 0 : 1)}M`
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K`
  return String(tokens)
}

/**
 * The model board shared by the homepage (top 6) and the full square.
 *
 * One row is one standard model: upstream channels are aggregated behind it.
 * Clicking a row expands the official-vs-market price comparison, the per-channel
 * reliability and the context/protocol facts in place; the model name links
 * to the detail page that carries the trend chart and code samples.
 */
export function ModelTable({
  models,
  details,
  detailsLoading = false,
  vendorMeta,
  showAddToKey = false,
  loading = false,
  skeletonRows = 6,
  minWidth = 1040,
  defaultExpanded = null,
}: ModelTableProps) {
  const columns = copy.pages.models.columns
  const [expanded, setExpanded] = useState<string | null>(defaultExpanded)

  // P0: a column with no data anywhere in the table is hidden outright.
  const anyMetrics = models.some((model) => model.metrics)
  const anyOfficial = Boolean(
    details && [...details.values()].some((detail) => Object.keys(detail.official_prices ?? {}).length > 0),
  )
  const columnCount =
    2 + // model, input price + output price are stacked in the price cell
    (anyOfficial ? 1 : 0) +
    (anyMetrics ? 3 : 0) +
    1 + // channels
    (showAddToKey ? 1 : 0)

  const headCells = (
    <>
      <TableHeaderCell>{columns.model}</TableHeaderCell>
      <TableHeaderCell numeric>{columns.price}</TableHeaderCell>
      {anyOfficial ? <TableHeaderCell numeric>{columns.saving}</TableHeaderCell> : null}
      {anyMetrics ? <TableHeaderCell numeric>{columns.success}</TableHeaderCell> : null}
      {anyMetrics ? <TableHeaderCell numeric>{columns.latency}</TableHeaderCell> : null}
      <TableHeaderCell numeric>{columns.channels}</TableHeaderCell>
      {showAddToKey ? <TableHeaderCell>{columns.actions}</TableHeaderCell> : null}
    </>
  )

  return (
    <Table minWidth={minWidth}>
      <TableHead>
        <TableRow interactive={false}>{headCells}</TableRow>
      </TableHead>
      <TableBody>
        {loading
          ? Array.from({ length: skeletonRows }, (_, index) => (
              <TableRow
                key={`skeleton-${index}`}
                className="[&>td]:h-14 [&>td]:py-0"
                aria-hidden="true"
              >
                <TableCell>
                  <div className="space-y-1.5">
                    {skeletonCell('w-40')}
                    {skeletonCell('w-16')}
                  </div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-8')}</div>
                </TableCell>
                {showAddToKey ? (
                  <TableCell>
                    <div className="flex justify-end">{skeletonCell('w-16')}</div>
                  </TableCell>
                ) : null}
              </TableRow>
            ))
          : models.map((model) => {
              const origin = originVendorOf(model)
              const row = currentPrices(model)
              const detail = details?.get(model.model)
              const official = detail?.official_prices?.flat
              const saving = savingOf(official?.input_micro_per_mtok, row?.input_micro_per_mtok)
              const metrics = model.metrics
              const isOpen = expanded === model.model
              const logo = vendorLogo(origin, vendorMeta)

              return (
                <Fragment key={model.model}>
                  <TableRow
                    interactive
                    className="[&>td]:h-14 [&>td]:py-0"
                    onClick={() => setExpanded(isOpen ? null : model.model)}
                  >
                    <TableCell>
                      <div className="flex min-w-0 items-center gap-2.5">
                        {logo ? (
                          <img
                            src={logo}
                            alt=""
                            aria-hidden="true"
                            className="h-6 w-6 shrink-0 rounded-sm"
                          />
                        ) : (
                          <span
                            aria-hidden="true"
                            className="grid h-6 w-6 shrink-0 place-items-center rounded-sm bg-brand-soft text-[12px] font-semibold text-brand-ink"
                          >
                            {model.model.slice(0, 1).toUpperCase()}
                          </span>
                        )}
                        <div className="min-w-0">
                          <Link
                            to="/models/$id"
                            params={{ id: model.model }}
                            onClick={(event) => event.stopPropagation()}
                            className="block truncate text-[15px] font-medium text-ink transition-colors hover:text-brand-ink"
                          >
                            {model.model}
                          </Link>
                          <span className="flex min-w-0 items-center gap-1.5">
                            <span className="truncate text-[13px] text-ink-3">
                              {vendorLabel(origin, vendorMeta)}
                            </span>
                            <CapabilityTags capabilities={model.capabilities} />
                          </span>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell numeric>
                      {row ? (
                        <div className="leading-tight tabular-nums">
                          <div className="text-ink">{formatYuan(row.input_micro_per_mtok)}</div>
                          <div className="mt-0.5 text-[13px] text-ink-3">
                            {copy.pages.models.outputPrefix} {formatYuan(row.output_micro_per_mtok)}
                          </div>
                          {model.buyer_price_source === 'official' || model.listing_count === 0 ? (
                            <Tag tone="brand" className="mt-1">
                              {copy.pages.models.officialPrice}
                            </Tag>
                          ) : null}
                        </div>
                      ) : (
                        <Missing />
                      )}
                    </TableCell>
                    {anyOfficial ? (
                      <TableCell numeric>
                        {saving === null ? (
                          <Missing />
                        ) : (
                          <span className="inline-flex rounded-full bg-brand-soft px-2 py-0.5 text-[12px] tabular-nums text-brand-ink">
                            {copy.pages.models.savingShort(saving)}
                          </span>
                        )}
                      </TableCell>
                    ) : null}
                    {anyMetrics ? (
                      <TableCell numeric>
                        <span className="tabular-nums text-ink-2">
                          {rateText(metrics?.success_rate_24h_milli) ?? <Missing />}
                        </span>
                      </TableCell>
                    ) : null}
                    {anyMetrics ? (
                      <TableCell numeric>
                        <span className="tabular-nums text-ink-2">
                          {latencyText(metrics?.latency_ms) ?? <Missing />}
                        </span>
                      </TableCell>
                    ) : null}
                    <TableCell numeric>
                      <span className="inline-flex items-center gap-2 tabular-nums">
                        <StatusDot level={statusLevel(metrics)} />
                        <span>{model.channel_count}</span>
                      </span>
                    </TableCell>
                    {showAddToKey ? (
                      <TableCell className="relative z-10 w-px whitespace-nowrap">
                        <span onClick={(event) => event.stopPropagation()}>
                          <AddToKeyMenu model={model.model} hasSupply={model.listing_count > 0} />
                        </span>
                      </TableCell>
                    ) : null}
                  </TableRow>
                  {isOpen ? (
                    <ExpandedRow
                      model={model}
                      detail={detail}
                      detailLoading={detailsLoading}
                      officialInput={official?.input_micro_per_mtok}
                      columns={columnCount}
                    />
                  ) : null}
                </Fragment>
              )
            })}
      </TableBody>
    </Table>
  )
}
