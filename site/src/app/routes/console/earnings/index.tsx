import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { formatDate } from '@/lib/time'
import { formatYuan } from '@/lib/money'
import {
  Button,
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

import {
  meQuery,
  sellerListingsHealthQuery,
  sellerListingsQuery,
  sellerStatsQuery,
  vendorName,
} from '@/features/seller/api'
import { AsyncError, PageSkeleton, SectionHeading } from '@/features/seller/components'
import { IncomeChart } from '@/features/seller/IncomeChart'
import { sellerErrorMessage } from '@/features/seller/errors'
import { payoutGateState, type PayoutGateStepId } from '@/features/seller/payout-gate'
import { listingStatusView, successRateText } from '@/features/seller/status'

export const Route = createFileRoute('/console/earnings/')({
  component: SellerOverviewPage,
})

const page = copy.pages.sellerConsole
const common = copy.pages.sellerCommon
const earnings = copy.pages.consoleEarnings

function StatCard({ children }: { children: ReactNode }) {
  return <div className="min-w-0 rounded-md border border-line bg-bg p-5">{children}</div>
}

function stepLabel(id: PayoutGateStepId): string {
  switch (id) {
    case 'real_name':
      return page.payoutGate.realName
    case 'alipay':
      return page.payoutGate.alipay
    case 'minimum':
      return page.payoutGate.minimum
    case 't7':
      return page.payoutGate.t7
  }
}

function PayoutGate({
  realNameVerified,
  payoutAccountBound,
  availableMicro,
  pendingMicro,
}: {
  realNameVerified: boolean
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
      case 'real_name':
        return done ? page.payoutGate.ready : page.payoutGate.realNameHint
      case 'alipay':
        return done ? page.payoutGate.ready : page.payoutGate.alipayHint
      case 'minimum':
        return done
          ? page.payoutGate.minimumDone
          : page.payoutGate.minimumHint(formatYuan(state.missingMicro))
      case 't7':
        return done ? page.payoutGate.t7Done : page.payoutGate.t7Hint(formatYuan(pendingMicro))
    }
  }

  return (
    <section
      aria-label={page.payoutGate.title}
      className="mt-6 rounded-md border border-line bg-bg p-6"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-[16px] font-semibold tracking-tight text-ink">
            {page.payoutGate.title}
          </h2>
          <p className="mt-1 text-[13px] leading-5 text-ink-3">{page.payoutGate.lead}</p>
        </div>
        <Tag tone={state.ready ? 'brand' : 'neutral'} dot>
          {page.payoutGate.progress(state.doneCount, state.total)}
        </Tag>
      </div>

      {state.ready ? (
        <p className="mt-3 text-[13px] leading-5 text-brand-ink">{page.payoutGate.ready}</p>
      ) : null}

      <ul className="mt-4 grid gap-2 sm:grid-cols-2">
        {state.steps.map((step) => (
          <li
            key={step.id}
            className="flex items-start justify-between gap-3 rounded-md border border-line px-4 py-3"
          >
            <div className="min-w-0">
              <div className="text-[13px] font-medium text-ink">{stepLabel(step.id)}</div>
              <p className="mt-0.5 text-[13px] leading-5 text-ink-3">
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

export function SellerOverviewPage() {
  const stats = useQuery(sellerStatsQuery())
  const listings = useQuery(sellerListingsQuery())
  const health = useQuery(sellerListingsHealthQuery())
  const me = useQuery(meQuery())

  if (stats.isPending || listings.isPending) return <PageSkeleton />

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
  const daily = data?.daily ?? []
  const hasIncome = daily.some((day) => String(day.income_micro) !== '0')
  const healthById = new Map((health.data ?? []).map((row) => [row.listing_id, row]))

  return (
    <>
      <PageHeader title={earnings.title} lead={earnings.lead} />

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

      <p className="mt-3 text-[13px] text-ink-3">
        {page.stats.monthIncome} {data ? formatYuan(data.month_income_micro) : copy.common.none}
        {' · '}
        {page.stats.platformFee} {data ? formatYuan(data.platform_fee_total_micro) : copy.common.none}
      </p>

      <section className="mt-6 rounded-md border border-line bg-bg p-6">
        <SectionHeading title={page.chart.title} lead={page.chart.lead} />
        <div className="mt-4">
          {hasIncome ? (
            <IncomeChart daily={daily} />
          ) : (
            <Empty
              title={page.chart.emptyTitle}
              reason={page.chart.emptyReason}
              next={page.chart.emptyNext}
            />
          )}
        </div>
      </section>

      <section className="mt-6 rounded-md border border-line bg-bg p-6">
        <SectionHeading
          title={page.health.title}
          lead={page.health.lead}
          actions={
            <>
              <Link
                to="/console/listings"
                className={buttonClasses('secondary', 'sm')}
              >
                {copy.pages.sellerCommon.nav.listings}
              </Link>
              <Link
                to="/console/listings/new"
                className={buttonClasses('primary', 'sm')}
              >
                {page.newListing}
              </Link>
            </>
          }
        />

        <div className="mt-5">
          {listings.isPending ? (
            <div className="space-y-2 border-t border-line pt-3" aria-busy="true">
              {Array.from({ length: 3 }, (_, index) => (
                <div key={index} className="h-9 animate-pulse rounded-sm bg-bg-soft" />
              ))}
            </div>
          ) : listings.isError ? (
            <Empty
              title={copy.pages.sellerListings.loadError}
              reason={sellerErrorMessage(listings.error)}
              next={copy.common.retryHint}
              action={
                <Button variant="secondary" onClick={() => void listings.refetch()}>
                  {copy.actions.retry}
                </Button>
              }
            />
          ) : listings.data && listings.data.length > 0 ? (
            <Table minWidth={860}>
              <TableHead>
                <TableRow>
                  <TableHeaderCell>{page.health.columns.listing}</TableHeaderCell>
                  <TableHeaderCell>{page.health.columns.models}</TableHeaderCell>
                  <TableHeaderCell>{page.health.columns.status}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.health.columns.todayIncome}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.health.columns.successRate}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.health.columns.created}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {listings.data.map((listing) => {
                  const view = listingStatusView(listing.status)
                  const row = healthById.get(listing.id)
                  return (
                    <TableRow key={listing.id} interactive>
                      <TableCell>
                        <div className="font-medium text-ink">{vendorName(listing.vendor)}</div>
                        <div className="tabular-nums text-[13px] text-ink-3">
                          #{listing.id} · {common.keyFingerprint} {listing.key_fingerprint8 || '—'}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {listing.models.slice(0, 2).map((model) => (
                            <Tag key={model}>{model}</Tag>
                          ))}
                          {listing.models.length > 2 ? (
                            <Tag>+{listing.models.length - 2}</Tag>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Tag tone={view.tone} dot>
                          {view.label}
                        </Tag>
                      </TableCell>
                      <TableCell numeric className="tabular-nums">
                        {formatYuan(row?.today_income_micro ?? 0)}
                      </TableCell>
                      <TableCell numeric className="tabular-nums text-ink-3">
                        {successRateText(row)}
                      </TableCell>
                      <TableCell numeric className="text-ink-3">
                        {formatDate(listing.created_at)}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          ) : (
            <Empty
              title={page.health.emptyTitle}
              reason={page.health.emptyReason}
              next={page.health.emptyNext}
              action={
                <Link
                  to="/console/listings/new"
                  className={buttonClasses('primary', 'md')}
                >
                  {page.newListing}
                </Link>
              }
            />
          )}
        </div>
      </section>
    </>
  )
}
