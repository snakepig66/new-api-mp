import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatDate } from '@/lib/time'
import { formatYuan } from '@/lib/money'
import {
  Button,
  Empty,
  Multiplier,
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

import { sellerListingsQuery, sellerStatsQuery, vendorName } from '@/features/seller/api'
import { AsyncError, PageSkeleton, SectionHeading } from '@/features/seller/components'
import { IncomeChart } from '@/features/seller/IncomeChart'
import { sellerErrorMessage } from '@/features/seller/errors'
import { listingStatusView } from '@/features/seller/status'

export const Route = createFileRoute('/seller/')({
  component: SellerOverviewPage,
})

const page = copy.pages.sellerConsole
const common = copy.pages.sellerCommon

function isZeroMicro(value: number | string): boolean {
  return String(value) === '0'
}

function limitText(concurrency: number, rpm: number, dailyCapMicro: number | string): string {
  const rpmText = rpm > 0 ? String(rpm) : common.unlimited
  const capText = isZeroMicro(dailyCapMicro) ? common.unlimited : formatYuan(dailyCapMicro)
  return `${concurrency} / ${rpmText} / ${capText}`
}

function SellerOverviewPage() {
  const stats = useQuery(sellerStatsQuery())
  const listings = useQuery(sellerListingsQuery())

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
  const hasIncome = daily.some((day) => !isZeroMicro(day.income_micro))

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <div className="mt-6 grid gap-5 border-b border-line pb-6 sm:grid-cols-2 lg:grid-cols-4 lg:divide-x lg:divide-line">
        <Stat
          label={page.stats.pending}
          value={data ? formatYuan(data.pending_micro) : copy.common.none}
          hint={page.statsHints.pending}
          className="lg:pr-5"
        />
        <Stat
          label={page.stats.withdrawable}
          value={data ? formatYuan(data.available_micro) : copy.common.none}
          hint={page.statsHints.withdrawable}
          tone="brand"
          className="lg:px-5"
        />
        <Stat
          label={page.stats.monthIncome}
          value={data ? formatYuan(data.month_income_micro) : copy.common.none}
          hint={page.statsHints.monthIncome}
          tone="success"
          className="lg:px-5"
        />
        <Stat
          label={page.stats.platformFee}
          value={data ? formatYuan(data.platform_fee_total_micro) : copy.common.none}
          hint={page.statsHints.platformFee}
          className="lg:pl-5"
        />
      </div>

      <section className="mt-8 border border-line bg-surface">
        <div className="border-b border-line px-5 py-4">
          <SectionHeading title={page.chart.title} lead={page.chart.lead} />
        </div>
        <div className="px-3 py-4 sm:px-4">
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

      <section className="mt-8">
        <SectionHeading
          title={page.health.title}
          lead={page.health.lead}
          actions={
            <>
              <Link
                to="/seller/listings"
                className={buttonClasses('secondary', 'sm')}
              >
                {copy.pages.sellerCommon.nav.listings}
              </Link>
              <Link
                to="/seller/listings/new"
                className={buttonClasses('primary', 'sm')}
              >
                {page.newListing}
              </Link>
            </>
          }
        />

        <div className="mt-4">
          {listings.isPending ? (
            <div className="space-y-2 border-t border-line pt-3" aria-busy="true">
              {Array.from({ length: 3 }, (_, index) => (
                <div key={index} className="h-9 animate-pulse rounded-[3px] bg-wash" />
              ))}
            </div>
          ) : listings.isError ? (
            <Empty
              title={copy.pages.sellerListings.loadError}
              reason={sellerErrorMessage(listings.error)}
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
                  <TableHeaderCell numeric>{page.health.columns.multiplier}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.health.columns.limits}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.health.columns.created}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {listings.data.map((listing) => {
                  const view = listingStatusView(listing.status)
                  return (
                    <TableRow key={listing.id} interactive>
                      <TableCell>
                        <div className="font-medium text-ink">{vendorName(listing.vendor)}</div>
                        <div className="font-numeric text-[11px] text-muted">
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
                      <TableCell numeric>
                        <Multiplier milli={listing.multiplier_milli} />
                      </TableCell>
                      <TableCell numeric className="text-muted">
                        {limitText(
                          listing.concurrency_limit,
                          listing.rpm_limit,
                          listing.daily_cap_micro,
                        )}
                      </TableCell>
                      <TableCell numeric className="text-muted">
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
                  to="/seller/listings/new"
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
