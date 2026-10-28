import { useQuery } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'

import { copy } from '@/copy'
import type {
  LeaderboardRange,
  LeaderboardTrend,
  ModelLeaderboardItem,
  SellerLeaderboardItem,
} from '@/features/leaderboard/api'
import {
  leaderboardModelsQuery,
  leaderboardSellersQuery,
  leaderboardTrendQuery,
} from '@/features/leaderboard/api'
import { formatCount } from '@/features/market/format'
import { QueryError } from '@/features/market/query-error'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { Empty, ModelName, PageHeader } from '@/ui'

interface LeaderboardSearch {
  range?: LeaderboardRange
}

const validateSearch = (
  search: Record<string, unknown>,
): LeaderboardSearch => {
  if (search.range === '7d') return { range: '7d' }
  if (search.range === '24h') return { range: '24h' }
  return {}
}

export const Route = createFileRoute('/leaderboard')({
  validateSearch,
  component: LeaderboardPage,
})

const page = copy.pages.leaderboard

const RANGES: LeaderboardRange[] = ['24h', '7d']

/** Thousandths of a percent, e.g. 995 -> "99.5%"; no sample -> 暂无数据. */
function formatRate(milli: number | null | undefined): string {
  if (milli === null || milli === undefined || !Number.isFinite(milli) || milli < 0) {
    return page.noData
  }
  return `${(milli / 10).toFixed(1)}%`
}

function hasRate(milli: number | null | undefined): boolean {
  return milli !== null && milli !== undefined && Number.isFinite(milli) && milli >= 0
}

/** Under one second stays in ms; longer durations switch to seconds. */
function formatLatency(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms < 0) {
    return page.noData
  }
  return ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(1)}s`
}

function rankTone(rank: number): string {
  return rank <= 3 ? 'font-semibold text-brand' : 'text-ink-3'
}

function Board({
  title,
  hint,
  children,
}: {
  title: string
  hint: string
  children: React.ReactNode
}) {
  return (
    <section className="min-w-0 glass p-4 sm:p-6">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 border-b border-line pb-3">
        <h2 className="text-title font-semibold tracking-tight text-ink">{title}</h2>
        <span className="text-caption text-ink-3">{hint}</span>
      </div>
      {children}
    </section>
  )
}

function Loading() {
  return <p className="py-10 text-center text-meta text-ink-3">{page.loading}</p>
}

function ModelBoard({
  items,
  pending,
  failed,
  retrying,
  onRetry,
}: {
  items: ModelLeaderboardItem[]
  pending: boolean
  failed: boolean
  retrying: boolean
  onRetry: () => void
}) {
  return (
    <Board title={page.models.title} hint={page.models.hint}>
      {pending ? (
        <Loading />
      ) : failed ? (
        <QueryError
          title={page.models.errorTitle}
          reason={page.models.errorReason}
          onRetry={onRetry}
          retrying={retrying}
        />
      ) : items.length === 0 ? (
        <Empty title={page.models.emptyTitle} reason={page.models.emptyReason} />
      ) : (
        <>
          <div className="mt-2 flex items-baseline gap-x-3 border-b border-line pb-1.5 text-caption text-ink-3">
            <span className="w-6 shrink-0 text-center">{page.models.rank}</span>
            <span className="min-w-0 flex-1">{page.models.model}</span>
            <span className="shrink-0">{page.models.tokens}</span>
          </div>
          <ul>
            {items.map((item) => (
              <li
                key={`${item.rank}-${item.model}`}
                className="border-b border-line py-3 last:border-b-0"
              >
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span
                    className={cn(
                      'w-6 shrink-0 text-center text-body tabular-nums',
                      rankTone(item.rank),
                    )}
                  >
                    {item.rank}
                  </span>
                  <span
                    className="min-w-0 flex-1 truncate text-body font-medium text-ink"
                    title={item.model}
                  >
                    <ModelName model={item.model} size="sm" />
                  </span>
                  <span className="shrink-0 whitespace-nowrap text-caption text-ink-3">
                    {page.models.tokens}{' '}
                    <span className="text-meta tabular-nums text-ink-2">
                      {formatCount(item.tokens)}
                    </span>
                  </span>
                </div>
                <dl className="mt-1 flex flex-wrap gap-x-4 gap-y-1 pl-9 text-caption text-ink-3">
                  <div className="flex items-baseline gap-1">
                    <dt>{page.models.requests}</dt>
                    <dd className="tabular-nums text-ink-2">{formatCount(item.requests)}</dd>
                  </div>
                  <div className="flex items-baseline gap-1">
                    <dt>{page.models.success}</dt>
                    <dd
                      className={cn(
                        'tabular-nums',
                        hasRate(item.success_rate_milli) ? 'text-ink-2' : 'text-ink-3',
                      )}
                    >
                      {formatRate(item.success_rate_milli)}
                    </dd>
                  </div>
                  <div className="flex items-baseline gap-1">
                    <dt>{page.models.latency}</dt>
                    <dd className="tabular-nums text-ink-2">
                      {formatLatency(item.avg_latency_ms)}
                    </dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        </>
      )}
    </Board>
  )
}

function SellerBoard({
  items,
  pending,
  failed,
  retrying,
  onRetry,
}: {
  items: SellerLeaderboardItem[]
  pending: boolean
  failed: boolean
  retrying: boolean
  onRetry: () => void
}) {
  return (
    <Board title={page.sellers.title} hint={page.sellers.hint}>
      {pending ? (
        <Loading />
      ) : failed ? (
        <QueryError
          title={page.sellers.errorTitle}
          reason={page.sellers.errorReason}
          onRetry={onRetry}
          retrying={retrying}
        />
      ) : items.length === 0 ? (
        <Empty title={page.sellers.emptyTitle} reason={page.sellers.emptyReason} />
      ) : (
        <>
          <div className="mt-2 flex items-baseline gap-x-3 border-b border-line pb-1.5 text-caption text-ink-3">
            <span className="w-6 shrink-0 text-center">{page.sellers.rank}</span>
            <span className="min-w-0 flex-1">{page.sellers.alias}</span>
            <span className="shrink-0">{page.sellers.income}</span>
          </div>
          <ul>
            {items.map((item) => (
              <li
                key={`${item.rank}-${item.alias}`}
                className="border-b border-line py-3 last:border-b-0"
              >
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span
                    className={cn(
                      'w-6 shrink-0 text-center text-body tabular-nums',
                      rankTone(item.rank),
                    )}
                  >
                    {item.rank}
                  </span>
                  <span
                    className="min-w-0 flex-1 truncate text-body font-medium text-ink"
                    title={item.alias}
                  >
                    {item.alias}
                  </span>
                  <span className="shrink-0 whitespace-nowrap text-caption text-ink-3">
                    {page.sellers.income}{' '}
                    <span className="text-meta tabular-nums text-ink">
                      {formatYuan(item.income_micro)}
                    </span>
                  </span>
                </div>
                <dl className="mt-1 flex flex-wrap gap-x-4 gap-y-1 pl-9 text-caption text-ink-3">
                  <div className="flex items-baseline gap-1">
                    <dt>{page.sellers.requests}</dt>
                    <dd className="tabular-nums text-ink-2">{formatCount(item.requests)}</dd>
                  </div>
                  <div className="flex items-baseline gap-1">
                    <dt>{page.sellers.success}</dt>
                    <dd
                      className={cn(
                        'tabular-nums',
                        hasRate(item.success_rate_milli) ? 'text-ink-2' : 'text-ink-3',
                      )}
                    >
                      {formatRate(item.success_rate_milli)}
                    </dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        </>
      )}
    </Board>
  )
}

function TrendChart({ trend }: { trend: LeaderboardTrend }) {
  const items = trend.items
  const maxRequests = Math.max(1, ...items.map((item) => item.requests))
  const start = trend.start ?? items[0]?.date ?? ''
  const end = trend.end ?? items[items.length - 1]?.date ?? ''

  return (
    <>
      <div
        className="mt-4 flex h-[160px] items-end gap-[2px] border-b border-line sm:gap-1"
        role="img"
        aria-label={page.trend.aria}
      >
        {items.map((item) => {
          const height =
            item.requests > 0 ? Math.max(2, (item.requests / maxRequests) * 100) : 0
          return (
            <div
              key={item.date}
              className="flex h-full min-w-0 flex-1 items-end"
              title={page.trend.barTitle(item.date, formatCount(item.requests))}
            >
              <div
                className="w-full rounded-t-[2px] bg-brand"
                style={{ height: `${height}%` }}
              />
            </div>
          )
        })}
      </div>
      <p className="mt-3 text-caption tabular-nums text-ink-3">
        {page.trend.range(start, end)}
      </p>
    </>
  )
}

function TrendSection({
  data,
  pending,
  failed,
  retrying,
  onRetry,
}: {
  data: LeaderboardTrend | undefined
  pending: boolean
  failed: boolean
  retrying: boolean
  onRetry: () => void
}) {
  const items = data?.items ?? []
  const hasTraffic = items.some((item) => item.requests > 0)

  return (
    <Board title={page.trend.title} hint={page.trend.hint}>
      {pending ? (
        <Loading />
      ) : failed ? (
        <QueryError
          title={page.trend.errorTitle}
          reason={page.trend.errorReason}
          onRetry={onRetry}
          retrying={retrying}
        />
      ) : !data || items.length === 0 || !hasTraffic ? (
        <Empty title={page.trend.emptyTitle} reason={page.trend.emptyReason} />
      ) : (
        <TrendChart trend={data} />
      )}
    </Board>
  )
}

export function LeaderboardPage() {
  const navigate = useNavigate()
  const search = Route.useSearch()
  const range: LeaderboardRange = search.range ?? '24h'

  const models = useQuery(leaderboardModelsQuery(range))
  const sellers = useQuery(leaderboardSellersQuery(range))
  const trend = useQuery(leaderboardTrendQuery(30))

  function selectRange(next: LeaderboardRange) {
    if (next === range) return
    void navigate({
      to: '/leaderboard',
      search: { range: next },
      replace: true,
      resetScroll: false,
    })
  }

  return (
    <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
      <PageHeader
        title={page.title}
        lead={range === '7d' ? page.lead7d : page.lead24h}
        actions={
          <div
            role="tablist"
            aria-label={page.rangeAria}
            className="inline-flex glass-inset p-1"
          >
            {RANGES.map((value) => (
              <button
                key={value}
                type="button"
                role="tab"
                aria-selected={range === value}
                onClick={() => selectRange(value)}
                className={
                  range === value
                    ? 'rounded px-3 py-1.5 text-meta font-medium text-brand-ink'
                    : 'rounded px-3 py-1.5 text-meta text-ink-2 transition-colors hover:text-ink'
                }
              >
                {value === '24h' ? page.range24h : page.range7d}
              </button>
            ))}
          </div>
        }
      />

      <div className="mt-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <ModelBoard
          items={models.data?.items ?? []}
          pending={models.isPending}
          failed={models.isError}
          retrying={models.isFetching}
          onRetry={() => void models.refetch()}
        />
        <SellerBoard
          items={sellers.data?.items ?? []}
          pending={sellers.isPending}
          failed={sellers.isError}
          retrying={sellers.isFetching}
          onRetry={() => void sellers.refetch()}
        />
      </div>

      <div className="mt-4">
        <TrendSection
          data={trend.data}
          pending={trend.isPending}
          failed={trend.isError}
          retrying={trend.isFetching}
          onRetry={() => void trend.refetch()}
        />
      </div>
    </div>
  )
}
