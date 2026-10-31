import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { Suspense, lazy, type ReactNode } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Empty,
  ModelName,
  PageHeader,
  SellerId,
  Stat,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  buttonClasses,
} from '@/ui'

import {
  consoleKeysQuery,
  usageRecordsQuery,
  usageStatsQuery,
  walletQuery,
} from '@/features/console/api'
import {
  ErrorBlock,
  Section,
  Skeleton,
  StatSkeleton,
  TableSkeleton,
} from '@/features/console/components/DataState'
import { Notice } from '@/features/console/components/form'
import { formatCount } from '@/features/console/format'
import { meQuery } from '@/features/seller/api'
import { isSellerActive } from '@/features/seller/status'
import { soonestExpiringKey } from '@/features/console/key-expiry'
import { toUsageRow } from '@/features/console/records'

const UsageChart = lazy(async () => {
  const module = await import('@/features/console/components/UsageChart')
  return { default: module.UsageChart }
})

export const Route = createFileRoute('/console/')({
  component: ConsoleOverviewPage,
})

const page = copy.pages.console

function ConsoleOverviewPage() {
  const wallet = useQuery(walletQuery())
  const stats = useQuery(usageStatsQuery(7))
  const recent = useQuery(usageRecordsQuery(1, 10))
  const keys = useQuery(consoleKeysQuery())
  const me = useQuery(meQuery())
  const requestsTotal = stats.data?.items.reduce((sum, item) => sum + item.requests, 0) ?? 0
  const expiring = keys.data ? soonestExpiringKey(keys.data) : null
  const sellerActive = me.data ? isSellerActive(me.data.seller) : false

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Link to="/console/keys" className={buttonClasses('primary', 'sm')}>
            {copy.pages.consoleKeys.create}
          </Link>
        }
      />

      {!sellerActive ? (
        <section className="mt-6 flex flex-wrap items-center justify-between gap-4 glass p-6">
          <div className="min-w-0">
            <h2 className="text-title font-semibold tracking-tight text-ink">
              {page.shareCard.title}
            </h2>
            <p className="mt-1 max-w-2xl text-body leading-6 text-ink-2">
              {page.shareCard.body}
            </p>
          </div>
          <Link to="/share" className={buttonClasses('secondary', 'sm', 'shrink-0')}>
            {page.shareCard.action}
          </Link>
        </section>
      ) : null}

      {expiring ? (
        <Notice className="mt-6" tone="warning" title={copy.pages.consoleKeys.expirySoon.title}>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {copy.pages.consoleKeys.expirySoon.body(
                expiring.token.name || copy.pages.consoleShared.none,
                formatDateTime(expiring.token.expired_time),
              )}
            </span>
            <Link
              to="/console/keys/$id"
              params={{ id: String(expiring.token.id) }}
              className="shrink-0 font-medium underline underline-offset-2"
            >
              {copy.pages.consoleKeys.expirySoon.action}
            </Link>
          </div>
        </Notice>
      ) : null}

      {wallet.isError ? (
        <ErrorBlock
          className="mt-6"
          error={wallet.error}
          onRetry={() => void wallet.refetch()}
        />
      ) : (
        <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          {wallet.isPending ? (
            <>
              <StatCard>
                <StatSkeleton />
              </StatCard>
              <StatCard>
                <StatSkeleton />
              </StatCard>
            </>
          ) : (
            <>
              <StatCard>
                <Stat
                  label={page.stats.balance}
                  value={formatYuan(wallet.data.balance_micro)}
                  hint={page.stats.balanceHint}
                  tone="brand"
                />
              </StatCard>
              <StatCard>
                <Stat
                  label={page.stats.spendThisMonth}
                  value={formatYuan(wallet.data.month_spend_micro)}
                  hint={page.stats.spendHint}
                />
              </StatCard>
            </>
          )}
          <StatCard>
            <Stat
              label={page.stats.requests7d}
              value={stats.data ? formatCount(requestsTotal) : copy.common.none}
              hint={page.stats.requests7dHint}
            />
          </StatCard>
        </div>
      )}

      <Section
        className="mt-8"
        title={page.chart.title}
        hint={page.chart.hint}
      >
        {stats.isPending ? (
          <Skeleton className="h-[220px] w-full" />
        ) : stats.isError ? (
          <ErrorBlock error={stats.error} onRetry={() => void stats.refetch()} />
        ) : stats.data.items.every((item) => item.requests === 0) ? (
          <Empty
            title={page.chart.emptyTitle}
            reason={page.chart.emptyReason}
            next={page.chart.emptyNext}
            action={
              <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
                {copy.pages.consoleKeys.create}
              </Link>
            }
          />
        ) : (
          <Suspense fallback={<Skeleton className="h-[220px] w-full" />}>
            <UsageChart items={stats.data.items} />
          </Suspense>
        )}
      </Section>

      <Section
        className="mt-9"
        title={page.recent.title}
        hint={page.recent.hint}
        action={
          <Link
            to="/console/records"
            search={{ preset: '7d', page: 1, page_size: 20 }}
            className="text-meta text-brand-ink hover:underline"
          >
            {page.recent.viewAll}
          </Link>
        }
      >
        {recent.isPending ? (
          <TableSkeleton rows={5} columns={5} />
        ) : recent.isError ? (
          <ErrorBlock error={recent.error} onRetry={() => void recent.refetch()} />
        ) : recent.data.items.length === 0 ? (
          <Empty
            title={page.recent.emptyTitle}
            reason={page.recent.emptyReason}
            next={page.recent.emptyNext}
            action={
              <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
                {copy.pages.consoleKeys.create}
              </Link>
            }
          />
        ) : (
          <Table minWidth={820}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>{page.recent.columns.time}</TableHeaderCell>
                <TableHeaderCell>{page.recent.columns.model}</TableHeaderCell>
                <TableHeaderCell>{page.recent.columns.seller}</TableHeaderCell>
                <TableHeaderCell numeric>{page.recent.columns.tokens}</TableHeaderCell>
                <TableHeaderCell numeric>{page.recent.columns.paid}</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {recent.data.items.map((record) => {
                const row = toUsageRow(record)
                return (
                  <TableRow key={row.requestId} interactive>
                    <TableCell className="whitespace-nowrap tabular-nums text-meta text-ink-3">
                      {formatDateTime(row.createdAt)}
                    </TableCell>
                    <TableCell>
                      <span className="truncate font-medium text-ink"><ModelName model={row.model} size="sm" /></span>
                    </TableCell>
                    <TableCell>
                      <SellerId id={row.sellerId} />
                    </TableCell>
                    <TableCell numeric>
                      <div className="text-meta tabular-nums">
                        {copy.pages.consoleRecords.tokens.pair(
                          formatCount(row.inputTokens),
                          formatCount(row.outputTokens)
                        )}
                      </div>
                      {(row.cacheReadTokens > 0 || row.cacheWriteTokens > 0) && (
                        <div className="flex items-center gap-1 text-meta tabular-nums text-ink-3">
                          {row.cacheReadTokens > 0 && (
                            <span>
                              {copy.pages.consoleRecords.tokens.cache(
                                formatCount(row.cacheReadTokens)
                              )}
                            </span>
                          )}
                          {row.cacheWriteTokens > 0 && (
                            <span>
                              {copy.pages.consoleRecords.tokens.cacheWrite(
                                formatCount(row.cacheWriteTokens)
                              )}
                            </span>
                          )}
                        </div>
                      )}
                    </TableCell>
                    <TableCell numeric>{formatYuan(row.totalDebitMicro)}</TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </Section>
    </>
  )
}

function StatCard({ children }: { children: ReactNode }) {
  return <div className="min-w-0 glass p-5">{children}</div>
}
