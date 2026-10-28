import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  PageHeader,
  Pagination,
  PeakBadge,
  Select,
  SellerId,
  Tag,
  buttonClasses,
} from '@/ui'

import {
  consoleKeysQuery,
  usageRecordCostQuery,
  usageRecordsQuery,
  usageSummaryQuery,
  type UsageCostRecordDetail,
} from '@/features/console/api'
import { ErrorBlock, Skeleton } from '@/features/console/components/DataState'
import { formatCount, prettyJson } from '@/features/console/format'
import { toUsageRow } from '@/features/console/records'
import type { UsageTier } from '@/features/console/types'
import {
  BuyerRecordCards,
  BuyerRecordsTable,
} from '../../../features/records/BuyerRecordsTable'
import { RecordsFilterBar } from '../../../features/records/RecordsFilterBar'
import {
  RecordsSummaryBar,
  type RecordsSummaryItem,
} from '../../../features/records/RecordsSummaryBar'
import {
  presetToRange,
  type RecordDatePreset,
  type RecordFilters,
  type RecordsFilterState,
} from '../../../features/records/filters'

const PAGE_SIZES = [20, 50, 100]
const PAGE_SIZE_OPTIONS = PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))
const DATE_PRESETS: RecordDatePreset[] = ['today', '7d', '30d', 'custom']

interface RecordsSearch {
  preset: RecordDatePreset
  start?: number
  end?: number
  model?: string
  token_id?: number
  group?: string
  stream?: boolean
  status?: string
  page: number
  page_size: number
}

function toPositiveInt(value: unknown): number | undefined {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

function toFilterText(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined
}

function toOptionalBool(value: unknown): boolean | undefined {
  if (value === true || value === 'true' || value === '1') return true
  if (value === false || value === 'false' || value === '0') return false
  return undefined
}

export const Route = createFileRoute('/console/records')({
  validateSearch: (search: Record<string, unknown>): RecordsSearch => {
    const pageSize = Number(search.page_size)
    return {
      preset: DATE_PRESETS.find((item) => item === String(search.preset ?? '')) ?? '7d',
      start: toPositiveInt(search.start),
      end: toPositiveInt(search.end),
      model: toFilterText(search.model),
      token_id: toPositiveInt(search.token_id),
      group: toFilterText(search.group),
      stream: toOptionalBool(search.stream),
      status: toFilterText(search.status),
      page: toPositiveInt(search.page) ?? 1,
      page_size: PAGE_SIZES.includes(pageSize) ? pageSize : 20,
    }
  },
  component: RecordsPage,
})

const page = copy.pages.consoleRecords

function RecordsPage() {
  const search = Route.useSearch()
  const navigate = useNavigate()
  const [openRequestId, setOpenRequestId] = useState<string | null>(null)
  // Bumped by 刷新 so a rolling preset re-anchors its end to now.
  const [refreshNonce, setRefreshNonce] = useState(0)
  const keys = useQuery(consoleKeysQuery())
  const tokenOptions = useMemo(
    () =>
      (keys.data ?? []).map(({ token }) => ({
        value: String(token.id),
        label: token.name || `#${token.id}`,
      })),
    [keys.data],
  )

  // A named preset always wins over the start/end left in the URL; only
  // 「自定义」uses the dates the user picked. Memoized so the now-based range
  // does not change the query key on every render.
  const filters = useMemo((): RecordFilters => {
    void refreshNonce
    const [start, end] =
      search.preset === 'custom' ? [search.start, search.end] : presetToRange(search.preset)
    return {
      start,
      end,
      model: search.model,
      token_id: search.token_id,
      group: search.group,
      stream: search.stream,
      status: search.status,
    }
  }, [
    refreshNonce,
    search.preset,
    search.start,
    search.end,
    search.model,
    search.token_id,
    search.group,
    search.stream,
    search.status,
  ])

  const records = useQuery(usageRecordsQuery(search.page, search.page_size, filters))
  const summary = useQuery(usageSummaryQuery(filters))

  const total = records.data?.total ?? 0
  const summaryData = summary.data
  const summaryItems: RecordsSummaryItem[] = summaryData
    ? [
        { label: '请求数', value: formatCount(summaryData.request_count) },
        {
          label: '成功率',
          value:
            summaryData.success_rate_milli < 0
              ? '—'
              : `${(summaryData.success_rate_milli / 10).toFixed(1)}%`,
          hint: `${formatCount(summaryData.success_count)} / ${formatCount(summaryData.request_count)}`,
        },
        {
          label: 'Tokens',
          value: `${formatCount(summaryData.input_tokens)} / ${formatCount(summaryData.output_tokens)}`,
          hint: '输入 / 输出',
        },
        { label: '实付', value: formatYuan(summaryData.total_debit_micro) },
        { label: '节省', value: formatYuan(summaryData.saved_micro) },
        {
          label: '平均耗时',
          value: `${summaryData.avg_first_token_ms > 0 ? `${Math.round(summaryData.avg_first_token_ms)} ms` : '—'} / ${
            summaryData.avg_latency_ms > 0 ? `${Math.round(summaryData.avg_latency_ms)} ms` : '—'
          }`,
          hint: '首字 / 总耗时',
        },
      ]
    : []

  function applyFilters(next: RecordsFilterState) {
    void navigate({
      to: '/console/records',
      search: {
        preset: next.preset,
        start: next.start,
        end: next.end,
        model: next.model,
        token_id: next.token_id,
        group: next.group,
        stream: next.stream,
        status: next.status,
        page: 1,
        page_size: search.page_size,
      },
    })
  }

  function goToPage(next: number) {
    void navigate({
      to: '/console/records',
      search: { ...search, page: next },
    })
  }

  function changePageSize(value: string) {
    const size = Number(value)
    if (!PAGE_SIZES.includes(size)) return
    void navigate({
      to: '/console/records',
      search: { ...search, page: 1, page_size: size },
    })
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <RecordsFilterBar
        className="mt-6"
        value={{ preset: search.preset, ...filters }}
        onChange={applyFilters}
        subjectOptions={tokenOptions}
        refreshing={records.isFetching || summary.isFetching}
        onRefresh={() => {
          if (search.preset === 'custom') {
            void records.refetch()
            void summary.refetch()
          } else {
            setRefreshNonce((n) => n + 1)
          }
        }}
      />

      <RecordsSummaryBar
        className="mt-4"
        items={summaryItems}
        loading={summary.isPending}
      />

      <div className="mt-6">
        {records.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : records.isError ? (
          <ErrorBlock error={records.error} onRetry={() => void records.refetch()} />
        ) : records.data.items.length === 0 ? (
          <Empty
            title={page.empty.title}
            reason={page.empty.reason}
            next={page.empty.next}
            action={
              <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
                {copy.pages.consoleKeys.create}
              </Link>
            }
          />
        ) : (
          <>
            <BuyerRecordsTable records={records.data.items} onOpen={setOpenRequestId} />
            <BuyerRecordCards records={records.data.items} onOpen={setOpenRequestId} />

            <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
              <Pagination
                page={search.page}
                pageSize={search.page_size}
                total={total}
                onPageChange={goToPage}
                showSummary
              />
              <div className="flex items-center gap-2">
                <span className="text-[13px] text-ink-3">每页</span>
                <Select
                  value={String(search.page_size)}
                  onChange={(event) => changePageSize(event.target.value)}
                  options={PAGE_SIZE_OPTIONS}
                  className="h-9 w-20"
                />
              </div>
            </div>
          </>
        )}
      </div>

      {openRequestId ? (
        <RecordDialog requestId={openRequestId} onClose={() => setOpenRequestId(null)} />
      ) : null}
    </>
  )
}

function TierBadge({ tier }: { tier: UsageTier }) {
  if (tier === 'peak' || tier === 'offpeak') return <PeakBadge tier={tier} />
  return <Tag tone="neutral">{page.tierFlat}</Tag>
}

function StatusTag({ status }: { status: string }) {
  if (status === 'success') return <Tag tone="brand">{page.status.success}</Tag>
  if (status === 'failed') return <Tag tone="danger">{page.status.failed}</Tag>
  return <Tag tone="neutral">{page.status.unknown}</Tag>
}

function RecordDialog({ requestId, onClose }: { requestId: string; onClose: () => void }) {
  const detail = useQuery(usageRecordCostQuery(requestId))
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      title={page.detail.costTitle}
      description={requestId}
      className="max-h-[calc(100vh-3rem)] max-w-2xl overflow-y-auto"
      footer={
        <Button variant="secondary" onClick={onClose}>
          {page.detail.close}
        </Button>
      }
    >
      {detail.isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-4 w-3/5" />
          <Skeleton className="mt-4 h-24 w-full" />
        </div>
      ) : detail.isError ? (
        <ErrorBlock error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <RecordDetailBody detail={detail.data} />
      )}
    </Dialog>
  )
}

export function RecordDetailBody({ detail }: { detail: UsageCostRecordDetail }) {
  const row = toUsageRow(detail)
  const retryText = prettyJson(detail.retry_chain)
  const totalTokens =
    row.inputTokens + row.outputTokens + row.cacheReadTokens + row.cacheWriteTokens
  return (
    <div className="space-y-5">
      <dl className="grid grid-cols-2 gap-x-5 gap-y-2 text-[13px]">
        <Info label={page.detail.requestId} value={row.requestId} mono />
        <Info label={page.detail.createdAt} value={formatDateTime(row.createdAt)} mono />
        <Info label={page.detail.model} value={row.model} mono />
        <Info
          label={page.detail.channelType}
          value={channelTypeLabel(detail.channel_type)}
        />
        <div>
          <dt className="text-ink-3">{page.detail.seller}</dt>
          <dd className="mt-0.5">
            <SellerId id={row.sellerId} />
          </dd>
        </div>
        <div>
          <dt className="text-ink-3">{page.detail.tier}</dt>
          <dd className="mt-0.5">
            <TierBadge tier={row.tier} />
          </dd>
        </div>
        <div>
          <dt className="text-ink-3">{page.detail.status}</dt>
          <dd className="mt-0.5">
            <StatusTag status={row.status} />
          </dd>
        </div>
      </dl>

      {row.usageEstimated ? (
        <p className="border border-warn/25 bg-warn/10 px-3 py-2 text-[13px] text-warn">
          {page.detail.estimated}
        </p>
      ) : null}

      <div>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {page.detail.tokens}
        </h3>
        <dl className="mt-2 grid grid-cols-2 gap-x-5 gap-y-1.5 text-[13px] sm:grid-cols-3">
          <Info label={page.detail.input} value={formatCount(row.inputTokens)} mono />
          <Info label={page.detail.output} value={formatCount(row.outputTokens)} mono />
          <Info label={page.detail.cacheRead} value={formatCount(row.cacheReadTokens)} mono />
          <Info label={page.detail.cacheWrite} value={formatCount(row.cacheWriteTokens)} mono />
          <Info label={page.detail.cacheHit} value={formatCount(row.cacheHitTokens)} mono />
          <Info label={page.detail.search} value={formatCount(row.searchCount)} mono />
          <Info label={page.detail.fetch} value={formatCount(row.fetchCount)} mono />
        </dl>
      </div>

      <div>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {page.detail.cost}
        </h3>
        <dl className="mt-2 grid grid-cols-2 gap-x-5 gap-y-1.5 text-[13px] sm:grid-cols-3">
          <Info label={page.detail.official} value={formatYuan(row.officialCostMicro)} mono />
          <Info label={page.detail.paid} value={formatYuan(row.totalDebitMicro)} mono />
          <Info label={page.detail.saved} value={formatYuan(row.savedMicro)} mono />
          <Info label={page.detail.toolDebit} value={formatYuan(row.toolDebitMicro)} mono />
          <Info
            label={`${page.detail.officialUnitPrice}（${page.detail.unitPriceUnit}）`}
            value={formatUnitPrice(row.officialCostMicro, totalTokens)}
            mono
          />
          <Info
            label={`${page.detail.paidUnitPrice}（${page.detail.unitPriceUnit}）`}
            value={formatUnitPrice(row.totalDebitMicro, totalTokens)}
            mono
          />
          <Info
            label={page.detail.multiplier}
            value={formatPriceRatio(detail.multiplier_milli)}
            mono
          />
          <Info label={page.detail.platformFee} value={formatYuan(detail.platform_fee_micro)} mono />
          <Info label={page.detail.sellerCredit} value={formatYuan(detail.seller_credit_micro)} mono />
        </dl>
      </div>

      <div>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {page.detail.retry}
        </h3>
        {retryText ? (
          <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-[#0d1117] p-4 font-mono text-[13px] leading-6 text-[#e6edf3]">
            <code>{retryText}</code>
          </pre>
        ) : (
          <p className="mt-2 text-[13px] text-ink-3">{page.detail.retryEmpty}</p>
        )}
      </div>
    </div>
  )
}

/** 渠道类型 label; an unknown or missing supplier falls back to 分享渠道. */
export function channelTypeLabel(channelType: string | undefined): string {
  switch (channelType) {
    case 'official':
      return page.detail.channelTypes.official
    case 'plan':
      return page.detail.channelTypes.plan
    case 'third_party':
      return page.detail.channelTypes.thirdParty
    case 'shared':
      return page.detail.channelTypes.shared
    case '':
    case undefined:
      return page.detail.channelTypes.shared
    default:
      return page.detail.channelTypes.unknown
  }
}

/**
 * Blended unit price in 元 per million tokens. The record does not store the
 * four price-book unit prices, so the dialog shows the effective rate implied
 * by the stored totals; zero tokens render as an em dash.
 */
export function formatUnitPrice(amountMicro: number, totalTokens: number): string {
  if (!Number.isFinite(totalTokens) || totalTokens <= 0) return '—'
  const perMillion = amountMicro / totalTokens
  if (!Number.isFinite(perMillion)) return '—'
  return `¥${perMillion.toFixed(perMillion >= 1 ? 2 : 4)}`
}

/** 计价比例 from milli (300 → "30%"); zero means "not recorded". */
export function formatPriceRatio(multiplierMilli: number): string {
  if (!Number.isFinite(multiplierMilli) || multiplierMilli <= 0) return '—'
  const percent = multiplierMilli / 10
  return `${percent.toFixed(percent % 1 === 0 ? 0 : 1)}%`
}

function Info({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-ink-3">{label}</dt>
      <dd className={mono ? 'mt-0.5 truncate tabular-nums text-[13px] text-ink' : 'mt-0.5 truncate text-ink'}>
        {value}
      </dd>
    </div>
  )
}
