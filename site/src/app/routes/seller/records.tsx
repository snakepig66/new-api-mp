import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { Empty, PageHeader, Pagination, Select } from '@/ui'

import { formatCount } from '@/features/console/format'
import { RecordDetailDrawer } from '@/features/records/RecordDetailDrawer'
import { RecordsFilterBar } from '@/features/records/RecordsFilterBar'
import {
  RecordsSummaryBar,
  type RecordsSummaryItem,
} from '@/features/records/RecordsSummaryBar'
import {
  presetToRange,
  type RecordDatePreset,
  type RecordFilters,
  type RecordsFilterState,
} from '@/features/records/filters'
import {
  sellerGroupsQuery,
  sellerListingsQuery,
  sellerVendorsQuery,
  shareRecordsQuery,
  shareSummaryQuery,
  vendorName,
} from '@/features/seller/api'
import { AsyncError, PageSkeleton } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { ShareRecordCards, ShareRecordsTable } from '@/features/seller/ShareRecordsTable'

export const Route = createFileRoute('/seller/records')({
  validateSearch: (search: Record<string, unknown>): RecordsSearch => {
    const pageSize = Number(search.page_size)
    return {
      preset: DATE_PRESETS.find((item) => item === String(search.preset ?? '')) ?? '7d',
      start: toPositiveInt(search.start),
      end: toPositiveInt(search.end),
      model: toFilterText(search.model),
      listing_id: toPositiveInt(search.listing_id),
      group: toFilterText(search.group),
      stream: toOptionalBool(search.stream),
      status: toFilterText(search.status),
      page: toPositiveInt(search.page) ?? 1,
      page_size: PAGE_SIZES.includes(pageSize) ? pageSize : 20,
    }
  },
  component: ShareRecordsPage,
})

const page = copy.pages.sellerRecords
const filtersCopy = copy.pages.records.filters
const PAGE_SIZES = [20, 50, 100]
const PAGE_SIZE_OPTIONS = PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))
const DATE_PRESETS: RecordDatePreset[] = ['today', '7d', '30d', 'custom']

interface RecordsSearch {
  preset: RecordDatePreset
  start?: number
  end?: number
  model?: string
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
  return ms > 0 ? `${Math.round(ms)} ms` : page.dash
}

function ShareRecordsPage() {
  const search = Route.useSearch()
  const navigate = useNavigate()
  const [openRequestId, setOpenRequestId] = useState<string | null>(null)
  // Bumped by 刷新 so a rolling preset re-anchors its end to now.
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [showSummary, setShowSummary] = useState(() => {
    try {
      return localStorage.getItem('seller_records_show_summary') !== 'false'
    } catch {
      return true
    }
  })

  const toggleSummary = () => {
    setShowSummary((prev) => {
      const next = !prev
      try {
        localStorage.setItem('seller_records_show_summary', String(next))
      } catch {
        // ignore
      }
      return next
    })
  }

  const listings = useQuery(sellerListingsQuery())
  const groups = useQuery(sellerGroupsQuery())
  const vendors = useQuery(sellerVendorsQuery())
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
      listing_id: search.listing_id,
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
    search.listing_id,
    search.group,
    search.stream,
    search.status,
  ])

  const records = useQuery({
    ...shareRecordsQuery(search.page, search.page_size, filters),
    refetchInterval: (query) =>
      query.state.data?.items?.some((item) => item.status === 'in_progress')
        ? 2500
        : false,
  })
  const summary = useQuery(shareSummaryQuery(filters))

  const total = records.data?.total ?? 0
  const summaryData = summary.data
  const summaryItems: RecordsSummaryItem[] = summaryData
    ? [
        { label: page.summary.requests, value: formatCount(summaryData.request_count) },
        {
          label: page.summary.successRate,
          value:
            summaryData.success_rate_milli < 0
              ? page.dash
              : `${(summaryData.success_rate_milli / 10).toFixed(1)}%`,
          hint: `${formatCount(summaryData.success_count)} / ${formatCount(summaryData.request_count)}`,
        },
        {
          label: page.summary.tokens,
          value: `${formatCount(summaryData.input_tokens)} / ${formatCount(summaryData.output_tokens)}`,
          hint: page.summary.tokensHint,
        },
        { label: page.summary.income, value: formatYuan(summaryData.seller_credit_micro) },
        {
          label: page.summary.latency,
          value: `${formatAvg(summaryData.avg_first_token_ms)} / ${formatAvg(summaryData.avg_latency_ms)}`,
          hint: page.summary.latencyHint,
        },
      ]
    : []

  function applyFilters(next: RecordsFilterState) {
    void navigate({
      to: '/seller/records',
      search: {
        preset: next.preset,
        start: next.start,
        end: next.end,
        model: next.model,
        listing_id: next.listing_id,
        group: next.group,
        stream: next.stream,
        status: next.status,
        page: 1,
        page_size: search.page_size,
      },
    })
  }

  function goToPage(next: number) {
    void navigate({ to: '/seller/records', search: { ...search, page: next } })
  }

  function changePageSize(value: string) {
    const size = Number(value)
    if (!PAGE_SIZES.includes(size)) return
    void navigate({ to: '/seller/records', search: { ...search, page: 1, page_size: size } })
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <RecordsFilterBar
        className="mt-4"
        value={{ preset: search.preset, ...filters }}
        onChange={applyFilters}
        subjectField="listing_id"
        subjectLabel={filtersCopy.subjectChannel}
        subjectAllLabel={filtersCopy.subjectChannelAll}
        subjectOptions={listingOptions}
        groupOptions={groupOptions}
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
        <RecordsSummaryBar className="mt-3" items={summaryItems} loading={summary.isPending} />
      ) : null}

      <div className="mt-4">
        {records.isPending ? (
          <PageSkeleton rows={4} />
        ) : records.isError ? (
          <AsyncError
            title={page.loadError}
            reason={sellerErrorMessage(records.error)}
            onRetry={() => void records.refetch()}
            retryLabel={copy.actions.retry}
          />
        ) : records.data.items.length === 0 ? (
          <Empty title={page.emptyTitle} reason={page.emptyReason} next={page.emptyNext} />
        ) : (
          <>
            <ShareRecordsTable records={records.data.items} onOpen={setOpenRequestId} />
            <ShareRecordCards records={records.data.items} onOpen={setOpenRequestId} />

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
        view="seller"
        onClose={() => setOpenRequestId(null)}
      />
    </>
  )
}
