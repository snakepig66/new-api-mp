import type { UsageRecord } from '../console/types'
import { formatCount } from '../console/format'
import { formatYuan } from '../../lib/money'
import { formatDateTime } from '../../lib/time'
import { SellerId } from '../../ui/identity'
import { Tag } from '../../ui/tag'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from '../../ui/table'

/**
 * 买方用量记录 — desktop table (≥ md) and mobile cards (< md).
 *
 * Presentational only: fetching, filters (URL search params via
 * validateSearch), pagination and the detail drawer live in records.tsx.
 * Clicking a row/card calls onOpen(request_id); how the detail opens is the
 * page's decision.
 */

// TODO: move into features/records/copy.ts with the rest of the records copy.
const copy = {
  colTime: '时间',
  colModel: '模型',
  colSeller: '卖家',
  colGroup: '分组',
  colToken: '令牌',
  colTokens: 'Tokens',
  colLatency: '耗时',
  colType: '类型',
  colCost: '费用',
  colStatus: '状态',
  converted: '转换',
  deletedToken: '已删除',
  cacheRead: '缓存读',
  cacheWrite: '缓存写',
  stream: '流式',
  nonStream: '非流',
  save: '省',
  success: '成功',
  failed: '失败',
  /** Empty-value placeholder: empty 分组, unrecorded latency. */
  dash: '—',
} as const

export interface BuyerRecordsTableProps {
  records: UsageRecord[]
  /** Open one record's detail by request_id. */
  onOpen: (requestId: string) => void
}

/** Mobile cards take the same props as the table. */
export type BuyerRecordCardsProps = BuyerRecordsTableProps

/** ms → "0.42s" / "3.4s"; 0 = 未记录 → "—". */
function formatLatency(ms: number): string {
  if (ms <= 0) return copy.dash
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

/** saved_permille (‰, 1000 = 100%) → "省 37%" / "省 37.5%". */
function formatSaved(permille: number): string {
  const pct = permille / 10
  return `${copy.save} ${pct % 1 === 0 ? pct.toFixed(0) : pct.toFixed(1)}%`
}

function isSuccess(record: UsageRecord): boolean {
  return record.status === 'success'
}

/** Model badge + optional 「转换」 marker. */
function ModelCell({ record }: { record: UsageRecord }) {
  return (
    <span className="inline-flex items-center gap-1">
      <Tag tone="neutral">{record.model}</Tag>
      {record.converted && <Tag tone="warning">{copy.converted}</Tag>}
    </span>
  )
}

/** 输入 → 输出, cache read/write as a subline. */
function TokensCell({ record }: { record: UsageRecord }) {
  const hasCache = record.cache_read_tokens > 0 || record.cache_write_tokens > 0
  return (
    <div>
      <div className="tabular-nums">
        {formatCount(record.input_tokens)} → {formatCount(record.output_tokens)}
      </div>
      {hasCache && (
        <div className="text-xs text-ink-3 tabular-nums">
          {copy.cacheRead} {formatCount(record.cache_read_tokens)} · {copy.cacheWrite}{' '}
          {formatCount(record.cache_write_tokens)}
        </div>
      )}
    </div>
  )
}

/** 首字 / 总耗时; 首字 colors by threshold. */
function LatencyCell({ record }: { record: UsageRecord }) {
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

/** 实付 big, 划线官方价 + 省 xx% as a subline. */
function CostCell({ record }: { record: UsageRecord }) {
  const showOfficial = record.official_cost_micro > record.total_debit_micro
  const showSaved = record.saved_permille > 0 && record.official_cost_micro > 0
  return (
    <div>
      <div className="font-medium tabular-nums">
        {formatYuan(record.total_debit_micro)}
      </div>
      {showOfficial && (
        <div className="text-xs text-ink-3 tabular-nums">
          <span className="line-through">{formatYuan(record.official_cost_micro)}</span>
          {showSaved && (
            <span className="ml-1 text-ok">{formatSaved(record.saved_permille)}</span>
          )}
        </div>
      )}
    </div>
  )
}

function StatusCell({ record }: { record: UsageRecord }) {
  // No retry info in list items (no retry field); retries show in the detail only.
  return isSuccess(record) ? (
    <Tag tone="success">{copy.success}</Tag>
  ) : (
    <Tag tone="danger">{copy.failed}</Tag>
  )
}

export function BuyerRecordsTable({ records, onOpen }: BuyerRecordsTableProps) {
  return (
    <div className="hidden md:block">
      <Table minWidth={960}>
        <TableHead>
          <TableRow>
            <TableHeaderCell>{copy.colTime}</TableHeaderCell>
            <TableHeaderCell>{copy.colModel}</TableHeaderCell>
            <TableHeaderCell>{copy.colSeller}</TableHeaderCell>
            <TableHeaderCell>{copy.colGroup}</TableHeaderCell>
            <TableHeaderCell>{copy.colToken}</TableHeaderCell>
            <TableHeaderCell numeric>{copy.colTokens}</TableHeaderCell>
            <TableHeaderCell numeric>{copy.colLatency}</TableHeaderCell>
            <TableHeaderCell>{copy.colType}</TableHeaderCell>
            <TableHeaderCell numeric>{copy.colCost}</TableHeaderCell>
            <TableHeaderCell>{copy.colStatus}</TableHeaderCell>
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
                <SellerId id={record.seller_id} />
              </TableCell>
              <TableCell>{record.token_group || copy.dash}</TableCell>
              <TableCell>
                {record.token_name || <span className="text-ink-3">{copy.deletedToken}</span>}
              </TableCell>
              <TableCell numeric>
                <TokensCell record={record} />
              </TableCell>
              <TableCell numeric>
                <LatencyCell record={record} />
              </TableCell>
              <TableCell>{record.is_stream ? copy.stream : copy.nonStream}</TableCell>
              <TableCell numeric>
                <CostCell record={record} />
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

/** One compact card; mirrors the table columns in a two-column stack. */
function BuyerRecordCard({
  record,
  onOpen,
}: {
  record: UsageRecord
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
        <SellerId id={record.seller_id} />
        <span className="tabular-nums">
          {record.is_stream ? copy.stream : copy.nonStream} ·{' '}
          <span className={firstTokenClass(record.first_token_ms)}>
            {formatLatency(record.first_token_ms)}
          </span>{' '}
          / {formatLatency(record.latency_ms)}
        </span>
      </div>
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-ink-2">
        <span>
          {copy.colGroup}: {record.token_group || copy.dash}
        </span>
        <span>
          {copy.colToken}:{' '}
          {record.token_name || <span className="text-ink-3">{copy.deletedToken}</span>}
        </span>
      </div>
      <div className="mt-2 flex items-end justify-between gap-2">
        <div className="text-xs tabular-nums">
          <div>
            {formatCount(record.input_tokens)} → {formatCount(record.output_tokens)}
            {(record.cache_read_tokens > 0 || record.cache_write_tokens > 0) && (
              <span className="ml-1 text-ink-3">
                ({copy.cacheRead} {formatCount(record.cache_read_tokens)} · {copy.cacheWrite}{' '}
                {formatCount(record.cache_write_tokens)})
              </span>
            )}
          </div>
        </div>
        <CostCell record={record} />
      </div>
    </button>
  )
}

export function BuyerRecordCards({ records, onOpen }: BuyerRecordCardsProps) {
  return (
    <div className="flex flex-col gap-2 md:hidden">
      {records.map((record) => (
        <BuyerRecordCard key={record.request_id} record={record} onOpen={onOpen} />
      ))}
    </div>
  )
}
