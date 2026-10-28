import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import { Drawer, Tag } from '@/ui'

import { ErrorBlock, Skeleton } from '@/features/console/components/DataState'
import { formatCount } from '@/features/console/format'
import type { UsageCostRecordDetail } from '@/features/console/api'
import { usageRecordCostQuery } from '@/features/console/api'
import { CopyButton } from '@/features/market/copy-button'
import { shareRecordQuery, type ShareRecordDetail } from '@/features/seller/api'

const text = copy.pages.records.detail

/** Who is looking: decides token vs channel fields and the money wording. */
export type RecordDetailView = 'buyer' | 'seller'

type DetailRecord = UsageCostRecordDetail | ShareRecordDetail

export interface RecordDetailDrawerProps {
  requestId: string | null
  view: RecordDetailView
  onClose: () => void
}

/**
 * 请求详情右侧抽屉，买方（我的调用）和卖方（我卖出的）共用。
 *
 * 拉取与渲染分开：drawer 按 view 选接口，正文由 RecordDetailContent 渲染，
 * 因此正文可以脱离网络单独测试。重试链在这里解析成结构化时间线，页面里
 * 不再出现 JSON 原文。
 */
export function RecordDetailDrawer({ requestId, view, onClose }: RecordDetailDrawerProps) {
  if (!requestId) return null
  return <RecordDetailLoader requestId={requestId} view={view} onClose={onClose} />
}

function RecordDetailLoader({
  requestId,
  view,
  onClose,
}: {
  requestId: string
  view: RecordDetailView
  onClose: () => void
}) {
  const buyer = useQuery({
    ...usageRecordCostQuery(requestId),
    enabled: view === 'buyer',
  })
  const seller = useQuery({
    ...shareRecordQuery(requestId),
    enabled: view === 'seller',
  })
  const query = view === 'buyer' ? buyer : seller
  const detail = query.data as DetailRecord | undefined

  return (
    <Drawer open onOpenChange={(open) => !open && onClose()} title={text.title} description={requestId} width="lg">
      {query.isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-4 w-3/5" />
          <Skeleton className="mt-4 h-24 w-full" />
        </div>
      ) : query.isError ? (
        <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />
      ) : detail ? (
        <RecordDetailContent detail={detail} view={view} />
      ) : null}
    </Drawer>
  )
}

export interface RecordDetailContentProps {
  detail: DetailRecord
  view: RecordDetailView
}

/** Drawer body: 基本信息 / Token 与费用 / 重试链. */
export function RecordDetailContent({ detail, view }: RecordDetailContentProps) {
  const buyer = view === 'buyer'
  const hops = useMemo(
    () => parseRetryChain(detail.retry_chain, view),
    [detail.retry_chain, view],
  )
  const buyerRecord = detail as UsageCostRecordDetail
  const sellerRecord = detail as ShareRecordDetail
  const record = buyer ? buyerRecord : sellerRecord
  const raw = detail.retry_chain?.trim() ?? ''

  return (
    <div className="space-y-7">
      <section>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {text.basic}
        </h3>
        <dl className="mt-3 grid grid-cols-1 gap-x-6 gap-y-3 text-[13px] sm:grid-cols-2">
          <div className="sm:col-span-2">
            <dt className="text-ink-3">{text.requestId}</dt>
            <dd className="mt-0.5 flex items-center gap-1">
              <span className="truncate font-mono text-[13px] text-ink">{record.request_id}</span>
              <CopyButton text={record.request_id} />
            </dd>
          </div>
          <Info label={text.time} value={formatDateTime(record.created_at)} mono />
          <Info label={text.endpoint} value={record.endpoint || text.dash} mono />
          <div className="min-w-0">
            <dt className="text-ink-3">{text.model}</dt>
            <dd className="mt-0.5 flex flex-wrap items-center gap-1">
              <Tag tone="neutral">{record.model}</Tag>
              {record.converted ? <Tag tone="warning">{text.converted}</Tag> : null}
              {record.upstream_model && record.upstream_model !== record.model ? (
                <span className="text-[13px] text-ink-2">
                  → <span className="font-mono">{record.upstream_model}</span>
                </span>
              ) : null}
            </dd>
          </div>
          <div className="min-w-0">
            <dt className="text-ink-3">{buyer ? text.token : text.channel}</dt>
            <dd className="mt-0.5 truncate text-[13px] text-ink">
              {buyer ? (
                buyerRecord.token_name || <span className="text-ink-3">{text.deletedToken}</span>
              ) : (
                <span className="tabular-nums">
                  #{sellerRecord.listing_id}
                  {sellerRecord.key_fingerprint4 ? (
                    <span className="ml-1 text-ink-3">
                      {text.keySuffix(sellerRecord.key_fingerprint4)}
                    </span>
                  ) : null}
                </span>
              )}
            </dd>
          </div>
          <Info
            label={text.group}
            value={(buyer ? buyerRecord.token_group : sellerRecord.channel_group) || text.dash}
          />
          <Info label={text.type} value={record.is_stream ? text.stream : text.nonStream} />
          <div className="min-w-0">
            <dt className="text-ink-3">{text.latency}</dt>
            <dd className="mt-0.5 tabular-nums text-[13px] text-ink">
              <span className="text-ink-3">{text.firstToken} </span>
              <span className={firstTokenClass(record.first_token_ms)}>
                {formatLatency(record.first_token_ms)}
              </span>
              <span className="text-ink-3"> · {text.totalLatency} </span>
              {formatLatency(record.latency_ms)}
            </dd>
          </div>
        </dl>
      </section>

      <section>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {text.tokensAndCost}
        </h3>
        <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 text-[13px] sm:grid-cols-4">
          <Info label={text.input} value={formatCount(record.input_tokens)} mono />
          <Info label={text.output} value={formatCount(record.output_tokens)} mono />
          <Info label={text.cacheRead} value={formatCount(record.cache_read_tokens)} mono />
          <Info label={text.cacheWrite} value={formatCount(record.cache_write_tokens)} mono />
          <Info
            label={text.search}
            value={text.times(formatCount(record.search_count))}
            mono
          />
          <Info label={text.fetch} value={text.times(formatCount(record.fetch_count))} mono />
        </dl>

        <dl className="mt-4 grid grid-cols-1 gap-x-6 gap-y-3 border-t border-line pt-3 text-[13px] sm:grid-cols-3">
          {buyer ? (
            <>
              <Info label={text.official} value={formatYuan(buyerRecord.official_cost_micro)} mono />
              <MoneyInfo
                label={text.paid}
                value={formatYuan(buyerRecord.total_debit_micro)}
                hint={
                  buyerRecord.saved_permille > 0
                    ? text.savedPercent(formatPercent(buyerRecord.saved_permille / 10))
                    : undefined
                }
              />
              <Info label={text.saved} value={formatYuan(buyerRecord.saved_micro)} mono />
            </>
          ) : (
            <>
              <Info label={text.buyerPaid} value={formatYuan(sellerRecord.buyer_paid_micro)} mono />
              <Info label={text.platformFee} value={formatYuan(sellerRecord.platform_fee_micro)} mono />
              <MoneyInfo label={text.sellerCredit} value={formatYuan(sellerRecord.seller_credit_micro)} />
            </>
          )}
        </dl>
        {record.usage_estimated ? (
          <p className="mt-3 border border-warn/25 bg-warn/10 px-3 py-2 text-[13px] text-warn">
            {copy.pages.consoleRecords.detail.estimated}
          </p>
        ) : null}
      </section>

      <section>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {text.retry}
        </h3>
        {raw === '' || raw === '[]' ? (
          <p className="mt-3 text-[13px] text-ink-3">{text.retryEmpty}</p>
        ) : hops.length === 0 ? (
          <p className="mt-3 text-[13px] text-ink-3">{text.retryUnavailable}</p>
        ) : (
          <ol className="mt-3" aria-label={text.retry}>
            {hops.map((hop, index) => (
              <li key={`${hop.seq}-${index}`} className="flex gap-3">
                <div className="flex flex-col items-center pt-1.5">
                  <span
                    className={cn(
                      'h-2 w-2 shrink-0 rounded-full',
                      hop.failed ? 'bg-danger' : 'bg-brand',
                    )}
                    aria-hidden="true"
                  />
                  {index < hops.length - 1 ? (
                    <span className="mt-1 w-px flex-1 bg-line" aria-hidden="true" />
                  ) : null}
                </div>
                <div className={cn('min-w-0 flex-1', index < hops.length - 1 && 'pb-4')}>
                  <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                    <span className="text-[13px] font-medium text-ink">{hop.eventLabel}</span>
                    <span className="text-xs tabular-nums text-ink-3">
                      {hop.latencyMs === undefined ? '' : formatLatency(hop.latencyMs)}
                    </span>
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-3">
                    <span>{hop.channelLabel}</span>
                    {hop.status ? (
                      <Tag tone={hop.failed ? 'danger' : 'neutral'}>{text.statusCode(hop.status)}</Tag>
                    ) : null}
                    {hop.message ? (
                      <span className="min-w-0 break-words text-ink-2">{hop.message}</span>
                    ) : null}
                  </div>
                </div>
              </li>
            ))}
          </ol>
        )}
      </section>
    </div>
  )
}

function Info({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-ink-3">{label}</dt>
      <dd
        className={cn(
          'mt-0.5 truncate text-[13px] text-ink',
          mono && 'tabular-nums',
        )}
      >
        {value}
      </dd>
    </div>
  )
}

function MoneyInfo({
  label,
  value,
  hint,
}: {
  label: string
  value: string
  hint?: string
}) {
  return (
    <div className="min-w-0">
      <dt className="text-ink-3">{label}</dt>
      <dd className="mt-0.5 flex flex-wrap items-baseline gap-x-2">
        <span className="tabular-nums text-[15px] font-medium text-ink">{value}</span>
        {hint ? <span className="text-xs text-ok">{hint}</span> : null}
      </dd>
    </div>
  )
}

/** ms → "0.42s" / "3.4s"; 0 = 未记录 → "—". */
export function formatLatency(ms: number | undefined): string {
  if (ms === undefined || ms <= 0) return text.dash
  const seconds = ms / 1000
  return seconds < 1 ? `${seconds.toFixed(2)}s` : `${seconds.toFixed(1)}s`
}

/** 首字耗时: green < 1s, yellow 1–3s, red > 3s; unrecorded stays muted. */
function firstTokenClass(ms: number): string {
  if (ms <= 0) return 'text-ink-3'
  if (ms < 1000) return 'text-ok'
  if (ms <= 3000) return 'text-warn'
  return 'text-danger'
}

/** 697 permille → 69.7; 370 → "37"; 375 → "37.5". */
function formatPercent(percent: number): string {
  return percent % 1 === 0 ? percent.toFixed(0) : percent.toFixed(1)
}

// ---------------------------------------------------------------------------
// 重试链
// ---------------------------------------------------------------------------

export interface RetryHop {
  seq: number
  /** Raw backend event id, kept for keys and unknown-event fallback. */
  event: string
  eventLabel: string
  channelLabel: string
  status?: string
  message?: string
  latencyMs?: number
  failed: boolean
}

/** Failure-ish events get the danger dot; the rest are routing steps. */
const FAILURE_EVENTS = new Set([
  'error',
  'breaker_429',
  'auto_pause',
  'model_unavailable',
  'plan_quota_exhausted',
  'zero_output',
  'stream_cut',
  'early_exit',
  'settle_shortfall',
  'settle_capped',
  'max_output_writeback_skipped',
])

interface RawHop {
  seq?: unknown
  event?: unknown
  listing_id?: unknown
  detail?: unknown
  at?: unknown
}

/**
 * Parse retry_chain JSON into timeline rows. Malformed input yields [] so the
 * caller can show a plain "record unavailable" line — raw JSON never reaches
 * the screen. Seller chains have listing ids stripped server-side.
 */
export function parseRetryChain(raw: string | null | undefined, view: RecordDetailView): RetryHop[] {
  const trimmed = raw?.trim() ?? ''
  if (!trimmed) return []
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []

  const hops = parsed
    .filter((item): item is RawHop => typeof item === 'object' && item !== null)
    .map((item) => ({
      seq: typeof item.seq === 'number' ? item.seq : 0,
      event: typeof item.event === 'string' ? item.event : '',
      listingId: typeof item.listing_id === 'number' ? item.listing_id : 0,
      detail: typeof item.detail === 'string' ? item.detail : '',
      at: typeof item.at === 'string' ? item.at : '',
    }))
    .sort((a, b) => a.seq - b.seq)

  return hops.map((hop, index) => {
    const next = hops[index + 1]
    const started = Date.parse(hop.at)
    const ended = next ? Date.parse(next.at) : Number.NaN
    const latencyMs =
      Number.isFinite(started) && Number.isFinite(ended) && ended >= started
        ? ended - started
        : undefined
    const { status, message } = parseHopDetail(hop.detail)
    return {
      seq: hop.seq,
      event: hop.event,
      eventLabel: eventLabel(hop.event),
      channelLabel:
        view === 'seller'
          ? text.myChannel
          : hop.listingId > 0
            ? `#${hop.listingId}`
            : text.channel,
      status,
      message,
      latencyMs,
      failed: FAILURE_EVENTS.has(hop.event),
    }
  })
}

/** detail carries "status=500 code=xxx", a stream end reason or a short note. */
function parseHopDetail(detail: string): { status?: string; message?: string } {
  const trimmed = detail.trim()
  if (!trimmed) return {}
  // A detail that happens to be a JSON document is not shown verbatim.
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) return {}

  const status = /status=(\d+)/.exec(trimmed)?.[1]
  const message = trimmed
    .replace(/status=\d+\s*/g, '')
    .replace(/code=\S+\s*/g, '')
    .trim()
  return { status, message: message || undefined }
}

function eventLabel(event: string): string {
  const labels = text.events as Record<string, string>
  return labels[event] ?? event
}
