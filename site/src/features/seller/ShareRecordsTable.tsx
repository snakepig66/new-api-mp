/**
 * 卖方「我卖出的」记录 — desktop table (≥ md) and mobile cards (< md).
 *
 * Presentational only: fetching, filters (URL search params via
 * validateSearch), pagination and the detail drawer live in records.tsx.
 * Clicking a row/card calls onOpen(request_id); how the detail opens is the
 * page's decision.
 */

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import { Tag } from '@/ui'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from '@/ui'

import { formatCount } from '@/features/console/format'

import type { ShareRecord } from './api'
import { BuyerCode } from './components'

const page = copy.pages.sellerRecords
const columns = page.columns
const tokens = page.tokens

export interface ShareRecordsTableProps {
  records: ShareRecord[]
  /** Open one record's detail by request_id. */
  onOpen: (requestId: string) => void
}

/** Mobile cards take the same props as the table. */
export type ShareRecordCardsProps = ShareRecordsTableProps

/** ms → "0.42s" / "3.4s"; 0 = 未记录 → "—". */
function formatLatency(ms: number): string {
  if (ms <= 0) return page.dash
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

function isSuccess(record: ShareRecord): boolean {
  return record.status === 'success'
}

/** Model badge + optional 「转换」 marker. */
function ModelCell({ record }: { record: ShareRecord }) {
  return (
    <span className="inline-flex items-center gap-1">
      <Tag tone="neutral">{record.model}</Tag>
      {record.converted && <Tag tone="warning">{page.converted}</Tag>}
    </span>
  )
}

/** #listing_id plus the key fingerprint tail when the backend has one. */
function ChannelCell({ record }: { record: ShareRecord }) {
  return (
    <div className="tabular-nums">
      <div className="text-[13px] text-ink">#{record.listing_id}</div>
      {record.key_fingerprint4 ? (
        <div className="text-xs text-ink-3">{page.keySuffix(record.key_fingerprint4)}</div>
      ) : null}
    </div>
  )
}

/** 输入 → 输出, cache read/write as a subline. */
function TokensCell({ record }: { record: ShareRecord }) {
  const hasCache = record.cache_read_tokens > 0 || record.cache_write_tokens > 0
  return (
    <div>
      <div className="tabular-nums">
        {tokens.pair(formatCount(record.input_tokens), formatCount(record.output_tokens))}
      </div>
      {hasCache && (
        <div className="text-xs text-ink-3 tabular-nums">
          {tokens.cacheRead} {formatCount(record.cache_read_tokens)} · {tokens.cacheWrite}{' '}
          {formatCount(record.cache_write_tokens)}
        </div>
      )}
    </div>
  )
}

/** 首字 / 总耗时; 首字 colors by threshold. */
function LatencyCell({ record }: { record: ShareRecord }) {
  return (
    <div className="tabular-nums">
      <span className={firstTokenClass(record.first_token_ms)}>
        {formatLatency(record.first_token_ms)}
      </span>
      {' / '}
      {formatLatency(record.latency_ms)}
    </div>
  )
}

/** 收益 big, 买家实付 + 平台费 as a subline. */
function IncomeCell({ record }: { record: ShareRecord }) {
  return (
    <div>
      <div className="font-medium tabular-nums">{formatYuan(record.seller_credit_micro)}</div>
      <div className="text-xs tabular-nums text-ink-3">
        {page.income.buyerPaid} {formatYuan(record.buyer_paid_micro)} ·{' '}
        {page.income.platformFee} {formatYuan(record.platform_fee_micro)}
      </div>
    </div>
  )
}

function StatusCell({ record }: { record: ShareRecord }) {
  return isSuccess(record) ? (
    <Tag tone="success">{page.status.success}</Tag>
  ) : (
    <Tag tone="danger">{page.status.failed}</Tag>
  )
}

export function ShareRecordsTable({ records, onOpen }: ShareRecordsTableProps) {
  return (
    <div className="hidden md:block">
      <Table minWidth={1120}>
        <TableHead>
          <TableRow>
            <TableHeaderCell>{columns.time}</TableHeaderCell>
            <TableHeaderCell>{columns.model}</TableHeaderCell>
            <TableHeaderCell>{columns.buyer}</TableHeaderCell>
            <TableHeaderCell>{columns.channelGroup}</TableHeaderCell>
            <TableHeaderCell>{columns.channel}</TableHeaderCell>
            <TableHeaderCell numeric>{columns.tokens}</TableHeaderCell>
            <TableHeaderCell numeric>{columns.latency}</TableHeaderCell>
            <TableHeaderCell>{columns.type}</TableHeaderCell>
            <TableHeaderCell numeric>{columns.income}</TableHeaderCell>
            <TableHeaderCell>{columns.status}</TableHeaderCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {records.map((record) => (
            <TableRow
              key={record.request_id}
              interactive
              onClick={() => onOpen(record.request_id)}
            >
              <TableCell className="whitespace-nowrap text-ink-3">
                {formatDateTime(record.created_at)}
              </TableCell>
              <TableCell>
                <ModelCell record={record} />
              </TableCell>
              <TableCell>
                <BuyerCode code={record.buyer_code} />
              </TableCell>
              <TableCell>{record.channel_group || page.dash}</TableCell>
              <TableCell>
                <ChannelCell record={record} />
              </TableCell>
              <TableCell numeric>
                <TokensCell record={record} />
              </TableCell>
              <TableCell numeric>
                <LatencyCell record={record} />
              </TableCell>
              <TableCell>{record.is_stream ? page.stream : page.nonStream}</TableCell>
              <TableCell numeric>
                <IncomeCell record={record} />
              </TableCell>
              <TableCell>
                <StatusCell record={record} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

/** One compact card; mirrors the table columns in a stacked layout. */
function ShareRecordCard({
  record,
  onOpen,
}: {
  record: ShareRecord
  onOpen: (requestId: string) => void
}) {
  return (
    <button
      type="button"
      onClick={() => onOpen(record.request_id)}
      className="w-full rounded-lg border border-line p-3 text-left transition-colors hover:bg-bg-soft"
    >
      <div className="flex items-center justify-between gap-2">
        <ModelCell record={record} />
        <StatusCell record={record} />
      </div>
      <div className="mt-1 text-xs text-ink-3">{formatDateTime(record.created_at)}</div>
      <div className="mt-2 flex items-center justify-between gap-2 text-xs text-ink-3">
        <BuyerCode code={record.buyer_code} />
        <span className="tabular-nums">
          {record.is_stream ? page.stream : page.nonStream} ·{' '}
          <span className={firstTokenClass(record.first_token_ms)}>
            {formatLatency(record.first_token_ms)}
          </span>{' '}
          / {formatLatency(record.latency_ms)}
        </span>
      </div>
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-ink-2">
        <span>
          {columns.channelGroup}: {record.channel_group || page.dash}
        </span>
        <span className="tabular-nums">
          {columns.channel}: #{record.listing_id}
          {record.key_fingerprint4 ? ` · ${page.keySuffix(record.key_fingerprint4)}` : ''}
        </span>
      </div>
      <div className="mt-2 flex items-end justify-between gap-2">
        <div className="text-xs tabular-nums text-ink-2">
          <div>
            {tokens.pair(formatCount(record.input_tokens), formatCount(record.output_tokens))}
          </div>
          {(record.cache_read_tokens > 0 || record.cache_write_tokens > 0) && (
            <div className="text-ink-3">
              {tokens.cacheRead} {formatCount(record.cache_read_tokens)} · {tokens.cacheWrite}{' '}
              {formatCount(record.cache_write_tokens)}
            </div>
          )}
        </div>
        <IncomeCell record={record} />
      </div>
    </button>
  )
}

export function ShareRecordCards({ records, onOpen }: ShareRecordCardsProps) {
  return (
    <div className="flex flex-col gap-2 md:hidden">
      {records.map((record) => (
        <ShareRecordCard key={record.request_id} record={record} onOpen={onOpen} />
      ))}
    </div>
  )
}
