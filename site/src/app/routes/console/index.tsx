import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { Suspense, lazy } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Empty,
  PageHeader,
  PeakBadge,
  SellerId,
  Stat,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  buttonClasses,
} from '@/ui'

import { usageRecordsQuery, usageStatsQuery, walletQuery } from '@/features/console/api'
import {
  ErrorBlock,
  Section,
  Skeleton,
  StatSkeleton,
  TableSkeleton,
} from '@/features/console/components/DataState'
import { formatCount } from '@/features/console/format'
import { toUsageRow } from '@/features/console/records'
import type { UsageTier } from '@/features/console/types'

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

      {wallet.isError ? (
        <ErrorBlock
          className="mt-6"
          error={wallet.error}
          onRetry={() => void wallet.refetch()}
        />
      ) : (
        <div className="mt-6 grid gap-6 border-b border-line pb-6 sm:grid-cols-3 sm:divide-x sm:divide-line">
          {wallet.isPending ? (
            <>
              <StatSkeleton />
              <StatSkeleton />
              <StatSkeleton />
            </>
          ) : (
            <>
              <Stat
                label={page.stats.balance}
                value={formatYuan(wallet.data.balance_micro)}
                hint={page.stats.balanceHint}
                tone="brand"
                className="sm:pr-6"
              />
              <Stat
                label={page.stats.spendThisMonth}
                value={formatYuan(wallet.data.month_spend_micro)}
                hint={page.stats.spendHint}
                className="sm:px-6"
              />
              <Stat
                label={page.stats.savedThisMonth}
                value={formatYuan(wallet.data.month_saved_micro)}
                hint={page.stats.savedHint}
                tone="success"
                className="sm:pl-6"
              />
            </>
          )}
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
            search={{ page: 1 }}
            className="text-[12px] text-brand-dark hover:underline"
          >
            {page.recent.viewAll}
          </Link>
        }
      >
        {recent.isPending ? (
          <TableSkeleton rows={5} columns={6} />
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
                <TableHeaderCell numeric>{page.recent.columns.saved}</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {recent.data.items.map((record) => {
                const row = toUsageRow(record)
                return (
                  <TableRow key={row.requestId} interactive>
                    <TableCell className="whitespace-nowrap font-numeric text-[12px] text-muted">
                      {formatDateTime(row.createdAt)}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <span className="truncate font-medium text-ink">{row.model}</span>
                        <TierBadge tier={row.tier} />
                      </div>
                    </TableCell>
                    <TableCell>
                      <SellerId id={row.sellerId} />
                    </TableCell>
                    <TableCell numeric>
                      <span className="text-[12px]">
                        {copy.pages.consoleRecords.tokens.input(formatCount(row.inputTokens))}
                        {' · '}
                        {copy.pages.consoleRecords.tokens.output(formatCount(row.outputTokens))}
                      </span>
                    </TableCell>
                    <TableCell numeric>{formatYuan(row.totalDebitMicro)}</TableCell>
                    <TableCell numeric className="text-success">
                      {formatYuan(row.savedMicro)}
                    </TableCell>
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

function TierBadge({ tier }: { tier: UsageTier }) {
  if (tier === 'peak' || tier === 'offpeak') return <PeakBadge tier={tier} />
  return <Tag tone="neutral">{copy.pages.consoleRecords.tierFlat}</Tag>
}
