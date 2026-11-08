import type { UsageRecord } from '../console/types'
import { formatDateTime } from '@/lib/time'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from '../../ui/table'
import {
  ChannelPillBadge,
  CostPillBadge,
  ModelPillBadge,
  TableSortHeader,
  TimeAndStatusCell,
  TimingMetricCell,
  TokensMetricCell,
} from './record-cells'

/**
 * 买方用量记录 — desktop table (≥ md) and mobile cards (< md).
 *
 * Modern capsule/card layout inspired by modern usage logs.
 * Presentational only: fetching, filters, pagination and drawer live in records.tsx.
 */

export const copy = {
  colModel: '模型',
  colSeller: '卖家',
  colToken: '令牌',
  colTokens: 'TOKENS',
  colLatency: '耗时',
  colCost: '费用',
  colDetail: '详情',
  colTime: '时间',
  deletedToken: '已删除',
  dash: '—',
} as const

/** Token column text: the live name, or a stable reference after deletion. */
function tokenLabel(record: UsageRecord): string {
  if (record.token_name) return record.token_name
  if (record.token_id && record.token_id > 0) return `Key #${record.token_id}（已删除）`
  return copy.deletedToken
}

export interface BuyerRecordsTableProps {
  records: UsageRecord[]
  /** Open one record's detail by request_id. */
  onOpen: (requestId: string) => void
}

export type BuyerRecordCardsProps = BuyerRecordsTableProps

export function BuyerRecordsTable({ records, onOpen }: BuyerRecordsTableProps) {
  return (
    <div className="hidden md:block">
      <Table minWidth={1180}>
        <TableHead>
          <TableRow>
            <TableHeaderCell className="w-[260px] min-w-[240px]">
              <TableSortHeader title={copy.colModel} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[130px] min-w-[120px]">
              <TableSortHeader title={copy.colSeller} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[130px] min-w-[110px]">
              <TableSortHeader title={copy.colToken} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[190px] min-w-[180px]">
              <TableSortHeader title={copy.colTokens} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[130px] min-w-[120px]">
              <TableSortHeader title={copy.colLatency} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[110px] min-w-[100px]">
              <TableSortHeader title={copy.colCost} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[72px] min-w-[72px] text-center">
              <TableSortHeader title={copy.colDetail} />
            </TableHeaderCell>
            <TableHeaderCell className="w-[160px] min-w-[150px]">
              <TableSortHeader title={copy.colTime} />
            </TableHeaderCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {records.map((record) => {
            const isInProgress = record.status === 'in_progress'
            const tokenDisplay = isInProgress ? (
              <span className="font-mono text-meta text-ink-3 whitespace-nowrap">-</span>
            ) : record.token_name ? (
              <span className="font-mono text-meta text-ink-2 truncate max-w-[120px] block whitespace-nowrap" title={record.token_name}>
                {record.token_name}
              </span>
            ) : (
              <span
                className="font-mono text-meta text-ink-2 truncate max-w-[140px] block whitespace-nowrap"
                title={tokenLabel(record)}
              >
                {tokenLabel(record)}
              </span>
            )

            const subtext = record.token_group || record.tier_label || ''

            return (
              <TableRow
                key={record.request_id}
                interactive
                className="h-[68px]"
                onClick={() => onOpen(record.request_id)}
              >
                {/* 1. 模型 */}
                <TableCell className="w-[260px] min-w-[240px] whitespace-nowrap">
                  <ModelPillBadge model={record.model} converted={record.converted} source={record.source} />
                </TableCell>

                {/* 2. 卖家 / 渠道 */}
                <TableCell className="w-[130px] min-w-[120px] whitespace-nowrap">
                  <ChannelPillBadge
                    id={record.seller_id}
                    subtext={subtext || copy.dash}
                    cacheHit={record.cache_read_tokens > 0}
                    isInProgress={isInProgress}
                  />
                </TableCell>

                {/* 3. 令牌 */}
                <TableCell className="w-[130px] min-w-[110px] whitespace-nowrap">{tokenDisplay}</TableCell>

                {/* 4. TOKENS */}
                <TableCell className="w-[190px] min-w-[180px] whitespace-nowrap">
                  <TokensMetricCell
                    input={record.input_tokens}
                    output={record.output_tokens}
                    cacheRead={record.cache_read_tokens}
                    cacheWrite={record.cache_write_tokens}
                    isInProgress={isInProgress}
                  />
                </TableCell>

                {/* 5. 耗时 */}
                <TableCell className="w-[130px] min-w-[120px] whitespace-nowrap">
                  <TimingMetricCell
                    firstTokenMs={record.first_token_ms}
                    latencyMs={record.latency_ms}
                    outputTokens={record.output_tokens}
                    isStream={record.is_stream}
                    isInProgress={isInProgress}
                  />
                </TableCell>

                {/* 6. 费用 */}
                <TableCell className="w-[110px] min-w-[100px] whitespace-nowrap">
                  <CostPillBadge amountMicro={record.total_debit_micro} />
                </TableCell>

                {/* 7. 详情 */}
                <TableCell className="w-[72px] min-w-[72px] text-center whitespace-nowrap">
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation()
                      onOpen(record.request_id)
                    }}
                    className="inline-flex items-center justify-center rounded-md px-2.5 py-1 text-meta font-medium text-brand hover:bg-brand/10 transition-colors whitespace-nowrap"
                  >
                    查看
                  </button>
                </TableCell>

                {/* 8. 时间 + 状态 */}
                <TableCell className="w-[160px] min-w-[150px] whitespace-nowrap">
                  <TimeAndStatusCell
                    createdAt={record.created_at}
                    status={record.status}
                  />
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

/** One compact card; mirrors table cells in mobile layout. */
function BuyerRecordCard({
  record,
  onOpen,
}: {
  record: UsageRecord
  onOpen: (requestId: string) => void
  index?: number
}) {
  const isInProgress = record.status === 'in_progress'
  const isFailed = record.status === 'failed'

  return (
    <button
      type="button"
      onClick={() => onOpen(record.request_id)}
      className="w-full glass-inset rounded-xl p-3.5 text-left transition-all hover:bg-brand/[0.06] active:scale-[0.99] border border-line"
    >
      {/* Top row: Model + Status */}
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0 flex-1">
          <ModelPillBadge model={record.model} converted={record.converted} source={record.source} className="max-w-full" />
        </div>
        {isFailed ? (
          <span className="inline-flex shrink-0 items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-rose-50 text-rose-700 border border-rose-200/80 dark:bg-rose-950/40 dark:text-rose-300 whitespace-nowrap">
            失败
          </span>
        ) : (
          <span className="inline-flex shrink-0 items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-emerald-50 text-emerald-700 border border-emerald-200/80 dark:bg-emerald-950/40 dark:text-emerald-300 whitespace-nowrap">
            消耗
            <span className="sr-only">成功</span>
          </span>
        )}
      </div>

      {/* Row 2: Seller + Token */}
      <div className="mt-3 flex items-center justify-between gap-2 border-t border-line/60 pt-2.5">
        <ChannelPillBadge
          id={record.seller_id}
          subtext={record.token_group || record.tier_label || copy.dash}
          cacheHit={record.cache_read_tokens > 0}
          isInProgress={isInProgress}
        />
        <div className="text-right">
          <span className="text-caption text-ink-3 block whitespace-nowrap">令牌</span>
          {isInProgress ? (
            <span className="font-mono text-meta text-ink-3 whitespace-nowrap">-</span>
          ) : record.token_name ? (
            <span className="font-mono text-meta text-ink-2 truncate max-w-[120px] block whitespace-nowrap" title={record.token_name}>
              {record.token_name}
            </span>
          ) : (
            <span className="text-caption text-ink-3 whitespace-nowrap" title={tokenLabel(record)}>
              {tokenLabel(record)}
            </span>
          )}
        </div>
      </div>

      {/* Row 3: Timing + Tokens */}
      <div className="mt-3 flex items-center justify-between gap-2">
        <TimingMetricCell
          firstTokenMs={record.first_token_ms}
          latencyMs={record.latency_ms}
          outputTokens={record.output_tokens}
          isStream={record.is_stream}
          isInProgress={isInProgress}
        />
        <div className="text-right">
          <TokensMetricCell
            input={record.input_tokens}
            output={record.output_tokens}
            cacheRead={record.cache_read_tokens}
            cacheWrite={record.cache_write_tokens}
            isInProgress={isInProgress}
            className="items-end"
          />
        </div>
      </div>

      {/* Row 4: Cost + Time */}
      <div className="mt-3 flex items-center justify-between gap-2 border-t border-line/60 pt-2.5">
        <span className="font-mono text-caption text-ink-3 tabular-nums whitespace-nowrap">
          {formatDateTime(record.created_at)}
        </span>
        <CostPillBadge amountMicro={record.total_debit_micro} />
      </div>
    </button>
  )
}

export function BuyerRecordCards({ records, onOpen }: BuyerRecordCardsProps) {
  return (
    <div className="flex flex-col gap-2.5 md:hidden">
      {records.map((record, index) => (
        <BuyerRecordCard
          key={record.request_id}
          record={record}
          onOpen={onOpen}
          index={index}
        />
      ))}
    </div>
  )
}
