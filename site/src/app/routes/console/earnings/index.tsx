import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  Empty,
  PageHeader,
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

import { meQuery, sellerStatsQuery, type SellerOverviewChannel } from '@/features/seller/api'
import { AsyncError, PageSkeleton, SectionHeading } from '@/features/seller/components'
import { OverviewCharts, formatCount } from '@/features/seller/OverviewCharts'
import { sellerErrorMessage } from '@/features/seller/errors'
import { payoutGateState, type PayoutGateStepId } from '@/features/seller/payout-gate'

export const Route = createFileRoute('/console/earnings/')({
  component: SellerOverviewPage,
})

const page = copy.pages.sellerConsole
const common = copy.pages.sellerCommon
const earnings = copy.pages.consoleEarnings

function StatCard({ children }: { children: ReactNode }) {
  return <div className="min-w-0 glass p-5">{children}</div>
}

function stepLabel(id: PayoutGateStepId): string {
  switch (id) {
    case 'payout_account':
    case 'alipay':
      return page.payoutGate.payoutAccount || page.payoutGate.alipay
    case 'minimum':
      return page.payoutGate.minimum
    case 't3':
    case 't7':
      return page.payoutGate.t3 || page.payoutGate.t7
    default:
      return page.payoutGate.realName
  }
}

function PayoutGate({
  realNameVerified,
  payoutAccountBound,
  availableMicro,
  pendingMicro,
}: {
  realNameVerified?: boolean
  payoutAccountBound: boolean
  availableMicro: number | string
  pendingMicro: number | string
}) {
  const state = payoutGateState({
    realNameVerified,
    payoutAccountBound,
    availableMicro,
    pendingMicro,
  })

  function stepHint(id: PayoutGateStepId, done: boolean): string {
    switch (id) {
      case 'payout_account':
      case 'alipay':
        return done ? page.payoutGate.ready : page.payoutGate.payoutAccountHint
      case 'minimum':
        return done
          ? page.payoutGate.minimumDone
          : page.payoutGate.minimumHint(formatYuan(state.missingMicro))
      case 't3':
      case 't7':
        return done ? page.payoutGate.t3Done : page.payoutGate.t3Hint(formatYuan(pendingMicro))
      default:
        return page.payoutGate.ready
    }
  }

  return (
    <section
      aria-label={page.payoutGate.title}
      className="mt-6 glass p-6"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-title font-semibold tracking-tight text-ink">
            {page.payoutGate.title}
          </h2>
          <p className="mt-1 text-meta leading-5 text-ink-3">{page.payoutGate.lead}</p>
        </div>
        <Tag tone={state.ready ? 'brand' : 'neutral'} dot>
          {page.payoutGate.progress(state.doneCount, state.total)}
        </Tag>
      </div>

      {state.ready ? (
        <p className="mt-3 text-meta leading-5 text-brand-ink">{page.payoutGate.ready}</p>
      ) : null}

      <ul className="mt-4 grid gap-2 sm:grid-cols-2">
        {state.steps.map((step) => (
          <li
            key={step.id}
            className="flex items-start justify-between gap-3 glass-inset px-4 py-3"
          >
            <div className="min-w-0">
              <div className="text-meta font-medium text-ink">{stepLabel(step.id)}</div>
              <p className="mt-0.5 text-meta leading-5 text-ink-3">
                {stepHint(step.id, step.done)}
              </p>
            </div>
            <Tag tone={step.done ? 'brand' : 'neutral'}>
              {step.done ? page.payoutGate.done : page.payoutGate.todo}
            </Tag>
          </li>
        ))}
      </ul>

      <div className="mt-4 flex flex-wrap gap-2">
        {!realNameVerified || !payoutAccountBound ? (
          <Link to="/console/onboarding" className={buttonClasses('secondary', 'sm')}>
            {page.payoutGate.onboardingLink}
          </Link>
        ) : null}
        <Link to="/console/earnings/payouts" className={buttonClasses('primary', 'sm')}>
          {page.payoutGate.payoutLink}
        </Link>
      </div>
    </section>
  )
}

/** 排序用：金额统一转成可比较的数字，展示仍走 formatYuan。 */
function incomeSortValue(value: number | string): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

export function SellerOverviewPage() {
  const stats = useQuery(sellerStatsQuery())
  const me = useQuery(meQuery())

  if (stats.isPending) return <PageSkeleton />

  if (stats.isError) {
    return (
      <AsyncError
        title={page.loadError}
        reason={sellerErrorMessage(stats.error)}
        onRetry={() => void stats.refetch()}
        retryLabel={copy.actions.retry}
      />
    )
  }

  const data = stats.data
  const series = data?.daily_by_model ?? []
  const hasChartData = series.some((row) =>
    row.days.some((day) => Number(day.tokens) > 0 || Number(day.income_micro) > 0),
  )
  const channels = [...(data?.by_channel ?? [])].sort(
    (a: SellerOverviewChannel, b: SellerOverviewChannel) =>
      incomeSortValue(b.income_micro) - incomeSortValue(a.income_micro),
  )
  const range = data?.range

  return (
    <>
      <PageHeader title={earnings.title} lead={earnings.lead} />

      <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard>
          <Stat
            label={earnings.stats.calls}
            value={data ? formatCount(data.month_calls) : copy.common.none}
            hint={earnings.stats.callsHint}
          />
        </StatCard>
        <StatCard>
          <Stat
            label={earnings.stats.tokens}
            value={data ? formatCount(data.month_tokens) : copy.common.none}
            hint={earnings.stats.tokensHint}
          />
        </StatCard>
        <StatCard>
          <Stat
            label={earnings.stats.income}
            value={data ? formatYuan(data.month_income_micro) : copy.common.none}
            hint={earnings.stats.incomeHint}
          />
        </StatCard>
        <StatCard>
          <Stat
            label={earnings.stats.pending}
            value={data ? formatYuan(data.pending_micro) : copy.common.none}
            hint={earnings.stats.pendingHint}
          />
        </StatCard>
      </div>

      <PayoutGate
        realNameVerified={me.data?.seller.progress.real_name_verified ?? false}
        payoutAccountBound={me.data?.seller.progress.payout_account_bound ?? false}
        availableMicro={data?.available_micro ?? 0}
        pendingMicro={data?.pending_micro ?? 0}
      />

      <div className="mt-6 grid gap-4 md:grid-cols-3">
        <StatCard>
          <Stat
            label={earnings.balances.pending}
            value={data ? formatYuan(data.pending_micro) : copy.common.none}
            hint={earnings.balances.pendingHint}
          />
        </StatCard>
        <StatCard>
          <Stat
            label={earnings.balances.available}
            value={data ? formatYuan(data.available_micro) : copy.common.none}
            hint={earnings.balances.availableHint}
            tone="brand"
          />
        </StatCard>
        <StatCard>
          <Stat
            label={earnings.balances.paid}
            value={data ? formatYuan(data.paid_micro) : copy.common.none}
            hint={earnings.balances.paidHint}
          />
        </StatCard>
      </div>

      <p className="mt-3 text-meta text-ink-3">
        {page.stats.monthIncome} {data ? formatYuan(data.month_income_micro) : copy.common.none}
        {' · '}
        {page.stats.platformFee} {data ? formatYuan(data.platform_fee_total_micro) : copy.common.none}
      </p>

      <section className="mt-6 glass p-6">
        <SectionHeading
          title={earnings.charts.title}
          lead={
            range ? earnings.charts.lead(range.start.slice(0, 10), range.end.slice(0, 10)) : undefined
          }
        />
        <div className="mt-4">
          {hasChartData ? (
            <OverviewCharts series={series} />
          ) : (
            <Empty
              title={earnings.charts.emptyTitle}
              reason={earnings.charts.emptyReason}
              next={earnings.charts.emptyNext}
            />
          )}
        </div>
      </section>

      <section className="mt-6 glass p-6">
        <SectionHeading
          title={earnings.channels.title}
          lead={earnings.channels.lead}
          actions={
            <>
              <Link to="/console/listings" className={buttonClasses('secondary', 'sm')}>
                {common.nav.listings}
              </Link>
              <Link to="/console/listings/new" className={buttonClasses('primary', 'sm')}>
                {page.newListing}
              </Link>
            </>
          }
        />

        <div className="mt-5">
          {channels.length === 0 ? (
            <Empty
              title={earnings.channels.emptyTitle}
              reason={earnings.channels.emptyReason}
              next={earnings.channels.emptyNext}
            />
          ) : (
            <Table minWidth={640}>
              <TableHead>
                <TableRow>
                  <TableHeaderCell>{earnings.channels.columns.channel}</TableHeaderCell>
                  <TableHeaderCell numeric>{earnings.channels.columns.calls}</TableHeaderCell>
                  <TableHeaderCell numeric>{earnings.channels.columns.tokens}</TableHeaderCell>
                  <TableHeaderCell numeric>{earnings.channels.columns.income}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {channels.map((row) => (
                  <TableRow key={row.listing_id}>
                    <TableCell>
                      <Link
                        to="/console/listings"
                        search={{ edit: row.listing_id }}
                        className="font-medium text-brand-ink hover:underline"
                      >
                        {row.label || `#${row.listing_id}`}
                      </Link>
                    </TableCell>
                    <TableCell numeric className="tabular-nums">
                      {formatCount(row.calls)}
                    </TableCell>
                    <TableCell numeric className="tabular-nums">
                      {formatCount(row.tokens)}
                    </TableCell>
                    <TableCell numeric className="tabular-nums">
                      {formatYuan(row.income_micro)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      </section>
    </>
  )
}
