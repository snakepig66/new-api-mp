import type { UsageRecord } from '../console/types'
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

export interface BuyerRecordsTableProps {
  records: UsageRecord[]
  /** Open one record's detail by request_id. */
  onOpen: (requestId: string) => void
}

export type BuyerRecordCardsProps = BuyerRecordsTableProps

export function BuyerRecordsTable({ records, onOpen }: BuyerRecordsTableProps) {
  return (
    <div className="hidden md:block">
      <Table minWidth={1040}>
        <TableHead>
          <TableRow>
            <TableHeaderCell>
              <TableSortHeader title={copy.colModel} />
            </TableHeaderCell>
            <TableHeaderCell>
              <TableSortHeader title={copy.colSeller} />
            </TableHeaderCell>
            <TableHeaderCell>
              <TableSortHeader title={copy.colToken} />
            </TableHeaderCell>
            <TableHeaderCell>
              <TableSortHeader title={copy.colTokens} />
            </TableHeaderCell>
            <TableHeaderCell>
              <TableSortHeader title={copy.colLatency} />
            </TableHeaderCell>
            <TableHeaderCell>
              <TableSortHeader title={copy.colCost} />
            </TableHeaderCell>
            <TableHeaderCell className="text-center">
              <TableSortHeader title={copy.colDetail} />
            </TableHeaderCell>
            <TableHeaderCell>
              <TableSortHeader title={copy.colTime} />
            </TableHeaderCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {records.map((record, index) => {
            const isInProgress = record.status === 'in_progress'
            const tokenDisplay = isInProgress ? (
              <span className="font-mono text-meta text-ink-3">-</span>
            ) : record.token_name ? (
              <span className="font-mono text-meta text-ink-2 truncate max-w-[120px] block" title={record.token_name}>
                {record.token_name}
              </span>
            ) : (
              <span className="text-caption text-ink-3">{copy.deletedToken}</span>
            )

            const subtext = record.token_group || record.tier_label || ''

            return (
              <TableRow
                key={record.request_id}
                interactive
                onClick={() => onOpen(record.request_id)}
              >
                {/* 1. 模型 */}
                <TableCell>
                  <ModelPillBadge model={record.model} converted={record.converted} />
                </TableCell>

                {/* 2. 卖家 / 渠道 */}
                <TableCell>
                  <ChannelPillBadge
                    id={record.seller_id}
                    subtext={subtext || copy.dash}
                    variant={index % 2 === 0 ? 'blue' : 'amber'}
                    isInProgress={isInProgress}
                  />
                </TableCell>

                {/* 3. 令牌 */}
                <TableCell>{tokenDisplay}</TableCell>

                {/* 4. TOKENS */}
                <TableCell>
                  <TokensMetricCell
                    input={record.input_tokens}
                    output={record.output_tokens}
                    cacheRead={record.cache_read_tokens}
                    cacheWrite={record.cache_write_tokens}
                    isInProgress={isInProgress}
                  />
                </TableCell>

                {/* 5. 耗时 */}
                <TableCell>
                  <TimingMetricCell
                    firstTokenMs={record.first_token_ms}
                    latencyMs={record.latency_ms}
                    isStream={record.is_stream}
                    isInProgress={isInProgress}
                  />
                </TableCell>

                {/* 6. 费用 */}
                <TableCell>
                  <CostPillBadge amountMicro={record.total_debit_micro} />
                </TableCell>

                {/* 7. 详情 */}
                <TableCell className="text-center text-ink-3">
                  {copy.dash}
                </TableCell>

                {/* 8. 时间 + 状态 */}
                <TableCell>
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
  index = 0,
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
        <ModelPillBadge model={record.model} converted={record.converted} />
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

      {/* Row 2: Seller + Token */}
      <div className="mt-3 flex items-center justify-between gap-2 border-t border-line/60 pt-2.5">
        <ChannelPillBadge
          id={record.seller_id}
          subtext={record.token_group || record.tier_label || copy.dash}
          variant={index % 2 === 0 ? 'blue' : 'amber'}
          isInProgress={isInProgress}
        />
        <div className="text-right">
          <span className="text-caption text-ink-3 block">令牌</span>
          {isInProgress ? (
            <span className="font-mono text-meta text-ink-3">-</span>
          ) : record.token_name ? (
            <span className="font-mono text-meta text-ink-2 truncate max-w-[120px] block">
              {record.token_name}
            </span>
          ) : (
            <span className="text-caption text-ink-3">{copy.deletedToken}</span>
          )}
        </div>
      </div>

      {/* Row 3: Timing + Tokens */}
      <div className="mt-3 flex items-center justify-between gap-2">
        <TimingMetricCell
          firstTokenMs={record.first_token_ms}
          latencyMs={record.latency_ms}
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
          />
        </div>
      </div>

      {/* Row 4: Cost + Time */}
      <div className="mt-3 flex items-center justify-between gap-2 border-t border-line/60 pt-2.5">
        <span className="font-mono text-caption text-ink-3 tabular-nums">
          {record.created_at.replace('T', ' ').substring(0, 19)}
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
