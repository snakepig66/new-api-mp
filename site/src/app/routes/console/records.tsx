import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import {
  Empty,
  PageHeader,
  Pagination,
  Select,
  buttonClasses,
} from '@/ui'

import {
  consoleKeysQuery,
  usageRecordsQuery,
  usageSummaryQuery,
} from '@/features/console/api'
import { ErrorBlock, Skeleton } from '@/features/console/components/DataState'
import { formatCount } from '@/features/console/format'
import {
  BuyerRecordCards,
  BuyerRecordsTable,
} from '../../../features/records/BuyerRecordsTable'
import { RecordDetailDrawer } from '../../../features/records/RecordDetailDrawer'
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
import {
  sellerGroupsQuery,
  sellerListingsQuery,
  sellerVendorsQuery,
  shareRecordsQuery,
  shareSummaryQuery,
  vendorName,
} from '@/features/seller/api'
import { ShareRecordCards, ShareRecordsTable } from '@/features/seller/ShareRecordsTable'

const PAGE_SIZES = [20, 50, 100]
const PAGE_SIZE_OPTIONS = PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))
const DATE_PRESETS: RecordDatePreset[] = ['today', '7d', '30d', 'custom']

interface RecordsSearch {
  view?: 'usage' | 'sales'
  preset: RecordDatePreset
  start?: number
  end?: number
  model?: string
  token_id?: number
  listing_id?: number
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

/** ms → "820 ms"; 0 = 无数据 → "—". */
function formatAvg(ms: number): string {
  return ms > 0 ? `${Math.round(ms)} ms` : '—'
}

export const Route = createFileRoute('/console/records')({
  validateSearch: (search: Record<string, unknown>): RecordsSearch => {
    const pageSize = Number(search.page_size)
    return {
      view: search.view === 'sales' ? 'sales' : 'usage',
      preset: DATE_PRESETS.find((item) => item === String(search.preset ?? '')) ?? '7d',
      start: toPositiveInt(search.start),
      end: toPositiveInt(search.end),
      model: toFilterText(search.model),
      token_id: toPositiveInt(search.token_id),
      listing_id: toPositiveInt(search.listing_id),
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
const sellerPageCopy = copy.pages.sellerRecords
const filtersCopy = copy.pages.records.filters

function RecordsPage() {
  const search = Route.useSearch()
  const navigate = useNavigate()
  const isSeller = search.view === 'sales'
  const [openRequestId, setOpenRequestId] = useState<string | null>(null)
  // Bumped by 刷新 so a rolling preset re-anchors its end to now.
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [showSummary, setShowSummary] = useState(() => {
    try {
      return localStorage.getItem(
        isSeller ? 'seller_records_show_summary' : 'console_records_show_summary',
      ) !== 'false'
    } catch {
      return true
    }
  })

  const toggleSummary = () => {
    setShowSummary((prev) => {
      const next = !prev
      try {
        localStorage.setItem(
          isSeller ? 'seller_records_show_summary' : 'console_records_show_summary',
          String(next),
        )
      } catch {
        // ignore
      }
      return next
    })
  }

  // Buyer options
  const keys = useQuery({ ...consoleKeysQuery(), enabled: !isSeller })
  const tokenOptions = useMemo(
    () =>
      (keys.data ?? []).map(({ token }) => ({
        value: String(token.id),
        label: token.name || `#${token.id}`,
      })),
    [keys.data],
  )

  // Seller options
  const listings = useQuery({ ...sellerListingsQuery(), enabled: isSeller })
  const groups = useQuery({ ...sellerGroupsQuery(), enabled: isSeller })
  const vendors = useQuery({ ...sellerVendorsQuery(), enabled: isSeller })
  const listingOptions = useMemo(
    () =>
      (listings.data ?? []).map((listing) => ({
        value: String(listing.id),
        label: `#${listing.id} ${listing.label?.trim() || vendorName(listing.vendor, vendors.data)}`,
      })),
    [listings.data, vendors.data],
  )
  const groupOptions = useMemo(
    () =>
      (groups.data ?? []).map((group) => ({
        value: String(group.id),
        label: vendorName(group.vendor_slug, vendors.data),
      })),
    [groups.data, vendors.data],
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
      token_id: isSeller ? undefined : search.token_id,
      listing_id: isSeller ? search.listing_id : undefined,
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
    search.listing_id,
    search.group,
    search.stream,
    search.status,
    isSeller,
  ])

  // Usage queries (Buyer)
  const usageRecords = useQuery({
    ...usageRecordsQuery(search.page, search.page_size, filters),
    enabled: !isSeller,
    refetchInterval: (query) =>
      query.state.data?.items?.some((item) => item.status === 'in_progress')
        ? 2500
        : false,
  })
  const usageSummary = useQuery({
    ...usageSummaryQuery(filters),
    enabled: !isSeller,
  })

  // Share queries (Seller)
  const shareRecords = useQuery({
    ...shareRecordsQuery(search.page, search.page_size, filters),
    enabled: isSeller,
    refetchInterval: (query) =>
      query.state.data?.items?.some((item) => item.status === 'in_progress')
        ? 2500
        : false,
  })
  const shareSummary = useQuery({
    ...shareSummaryQuery(filters),
    enabled: isSeller,
  })

  const records = isSeller ? shareRecords : usageRecords
  const summary = isSeller ? shareSummary : usageSummary
  const total = records.data?.total ?? 0

  const summaryItems: RecordsSummaryItem[] = useMemo(() => {
    if (isSeller) {
      const summaryData = shareSummary.data
      if (!summaryData) return []
      return [
        { label: sellerPageCopy.summary.requests, value: formatCount(summaryData.request_count) },
        {
          label: sellerPageCopy.summary.successRate,
          value:
            summaryData.success_rate_milli < 0
              ? sellerPageCopy.dash
              : `${(summaryData.success_rate_milli / 10).toFixed(1)}%`,
          hint: `${formatCount(summaryData.success_count)} / ${formatCount(summaryData.request_count)}`,
        },
        {
          label: sellerPageCopy.summary.tokens,
          value: `${formatCount(summaryData.input_tokens + (summaryData.cache_read_tokens ?? 0) + (summaryData.cache_write_tokens ?? 0))} / ${formatCount(summaryData.output_tokens)}`,
          hint: sellerPageCopy.summary.tokensHint,
        },
        { label: sellerPageCopy.summary.income, value: formatYuan(summaryData.seller_credit_micro) },
        {
          label: sellerPageCopy.summary.latency,
          value: `${formatAvg(summaryData.avg_first_token_ms)} / ${formatAvg(summaryData.avg_latency_ms)}`,
          hint: sellerPageCopy.summary.latencyHint,
        },
      ]
    }

    const summaryData = usageSummary.data
    if (!summaryData) return []
    return [
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
        value: `${formatCount(summaryData.input_tokens + (summaryData.cache_read_tokens ?? 0) + (summaryData.cache_write_tokens ?? 0))} / ${formatCount(summaryData.output_tokens)}`,
        hint: '输入 / 输出',
      },
      { label: '实付', value: formatYuan(summaryData.total_debit_micro) },
      {
        label: '平均耗时',
        value: `${summaryData.avg_first_token_ms > 0 ? `${Math.round(summaryData.avg_first_token_ms)} ms` : '—'} / ${
          summaryData.avg_latency_ms > 0 ? `${Math.round(summaryData.avg_latency_ms)} ms` : '—'
        }`,
        hint: '首字 / 总耗时',
      },
    ]
  }, [isSeller, shareSummary.data, usageSummary.data])

  function applyFilters(next: RecordsFilterState) {
    void navigate({
      to: '/console/records',
      search: {
        view: search.view,
        preset: next.preset,
        start: next.start,
        end: next.end,
        model: next.model,
        token_id: isSeller ? undefined : next.token_id,
        listing_id: isSeller ? next.listing_id : undefined,
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
      <PageHeader
        title={isSeller ? sellerPageCopy.title : page.title}
        lead={isSeller ? sellerPageCopy.lead : page.lead}
      />

      <div className="mt-4 flex items-center gap-1.5 overflow-x-auto">
        <Link
          to="/console/records"
          search={{ view: 'usage', preset: '7d', page: 1, page_size: 20 }}
          className={cn(
            'inline-flex h-9 shrink-0 items-center whitespace-nowrap rounded-full px-4 text-body font-medium transition-colors',
            !isSeller
              ? 'bg-brand text-white shadow-xs'
              : 'text-ink-2 hover:bg-white/60 hover:text-ink',
          )}
        >
          我的调用（消费）
        </Link>
        <Link
          to="/console/records"
          search={{ view: 'sales', preset: '7d', page: 1, page_size: 20 }}
          className={cn(
            'inline-flex h-9 shrink-0 items-center whitespace-nowrap rounded-full px-4 text-body font-medium transition-colors',
            isSeller
              ? 'bg-brand text-white shadow-xs'
              : 'text-ink-2 hover:bg-white/60 hover:text-ink',
          )}
        >
          渠道收益调用（分享）
        </Link>
      </div>

      <RecordsFilterBar
        className="mt-4"
        value={{ preset: search.preset, ...filters }}
        onChange={applyFilters}
        subjectField={isSeller ? 'listing_id' : 'token_id'}
        subjectLabel={isSeller ? filtersCopy.subjectChannel : undefined}
        subjectAllLabel={isSeller ? filtersCopy.subjectChannelAll : undefined}
        subjectOptions={isSeller ? listingOptions : tokenOptions}
        groupOptions={isSeller ? groupOptions : undefined}
        refreshing={records.isFetching || summary.isFetching}
        showSummary={showSummary}
        onToggleSummary={toggleSummary}
        onRefresh={() => {
          if (search.preset === 'custom') {
            void records.refetch()
            void summary.refetch()
          } else {
            setRefreshNonce((n) => n + 1)
          }
        }}
      />

      {showSummary ? (
        <RecordsSummaryBar
          className="mt-3"
          items={summaryItems}
          loading={summary.isPending}
        />
      ) : null}

      <div className="mt-4">
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
            title={isSeller ? sellerPageCopy.emptyTitle : page.empty.title}
            reason={isSeller ? sellerPageCopy.emptyReason : page.empty.reason}
            next={isSeller ? sellerPageCopy.emptyNext : page.empty.next}
            action={
              isSeller ? undefined : (
                <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
                  {copy.pages.consoleKeys.create}
                </Link>
              )
            }
          />
        ) : (
          <>
            {isSeller ? (
              <>
                <ShareRecordsTable
                  records={records.data.items as any}
                  onOpen={setOpenRequestId}
                />
                <ShareRecordCards
                  records={records.data.items as any}
                  onOpen={setOpenRequestId}
                />
              </>
            ) : (
              <>
                <BuyerRecordsTable
                  records={records.data.items as any}
                  onOpen={setOpenRequestId}
                />
                <BuyerRecordCards
                  records={records.data.items as any}
                  onOpen={setOpenRequestId}
                />
              </>
            )}

            <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
              <Pagination
                page={search.page}
                pageSize={search.page_size}
                total={total}
                onPageChange={goToPage}
                showSummary
              />
              <div className="flex items-center gap-2">
                <span className="text-meta text-ink-3">每页</span>
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

      <RecordDetailDrawer
        requestId={openRequestId}
        view={isSeller ? 'seller' : 'buyer'}
        onClose={() => setOpenRequestId(null)}
      />
    </>
  )
}
