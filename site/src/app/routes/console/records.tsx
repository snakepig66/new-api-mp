import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'

import { copy } from '@/copy'
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

  const records = useQuery({
    ...usageRecordsQuery(search.page, search.page_size, filters),
    refetchInterval: (query) =>
      query.state.data?.items?.some((item) => item.status === 'in_progress')
        ? 2500
        : false,
  })
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
        view="buyer"
        onClose={() => setOpenRequestId(null)}
      />
    </>
  )
}
