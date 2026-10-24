import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useMemo } from 'react'

import { copy } from '@/copy'
import { publicStatsQuery, squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import { CodeBlock } from '@/features/market/code-block'
import { formatCount } from '@/features/market/format'
import { ModelTable } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { currentBaseUrl } from '@/features/market/url'
import { formatMultiplier } from '@/lib/multiplier'
import { formatTime } from '@/lib/time'
import { Button, Empty, Stat, buttonClasses } from '@/ui'

export const Route = createFileRoute('/')({
  component: HomePage,
})

function StatPulse() {
  return (
    <span
      className="mt-1 block h-6 w-24 animate-pulse rounded-[3px] bg-line"
      aria-hidden="true"
    />
  )
}

function HomePage() {
  const home = copy.pages.home
  const stats = useQuery(publicStatsQuery())
  const models = useQuery(squareModelsQuery())

  // "热门" has no public traffic number; listing_count is the only public
  // popularity proxy, so the strip is ordered by supply and then by model id.
  const top = useMemo(() => {
    return [...(models.data ?? [])]
      .sort((a, b) => b.listing_count - a.listing_count || a.model.localeCompare(b.model))
      .slice(0, 8)
  }, [models.data])

  const { details, loading: detailsLoading } = useSquareModelDetails(top)

  const sampleModel = top[0]?.model ?? 'deepseek-flash'
  const baseUrl = currentBaseUrl()
  const statsError = stats.isError

  return (
    <>
      <section className="border-b border-line">
        <div className="mx-auto grid max-w-[1180px] gap-10 px-5 pb-12 pt-14 sm:pt-16 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-start">
          <div>
            <p className="flex items-center gap-2 text-[11.5px] text-muted">
              <span className="h-1.5 w-1.5 rounded-full bg-brand" aria-hidden="true" />
              {home.kicker}
            </p>
            <h1 className="mt-4 max-w-[560px] text-[32px] font-semibold leading-[1.22] tracking-tight text-ink sm:text-[42px]">
              {home.headlineLines[0]}
              <br />
              {home.headlineLines[1]}
            </h1>
            <p className="mt-5 max-w-xl text-[14px] leading-7 text-muted">{home.lead}</p>
            <div className="mt-7 flex flex-wrap gap-2.5">
              <Link to="/models" className={buttonClasses('primary', 'md')}>
                {copy.actions.browseModels}
              </Link>
              <Link to="/seller" className={buttonClasses('secondary', 'md')}>
                {copy.actions.becomeSeller}
              </Link>
            </div>
            <dl className="mt-8 flex flex-wrap gap-x-8 gap-y-2 border-t border-line pt-4 text-[12.5px]">
              {home.facts.map((fact) => (
                <div key={fact.label} className="flex items-baseline gap-2">
                  <dt className="text-muted">{fact.label}</dt>
                  <dd className="font-medium text-ink">{fact.value}</dd>
                </div>
              ))}
            </dl>
          </div>

          <aside className="border border-line bg-surface" aria-label={home.live}>
            <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5">
              <span className="text-[11.5px] font-medium text-ink">{home.live}</span>
              <span className="font-numeric text-[11px] text-muted">
                {stats.data
                  ? home.liveUpdated(formatTime(stats.data.generated_at))
                  : stats.isPending
                    ? copy.common.loading
                    : copy.common.none}
              </span>
            </div>

            {statsError ? (
              <div className="px-4 py-4" role="alert">
                <p className="text-[13px] font-medium text-ink">{home.statsErrorTitle}</p>
                <p className="mt-1 text-[12px] leading-5 text-muted">{home.statsErrorReason}</p>
                <Button
                  variant="secondary"
                  size="sm"
                  className="mt-3"
                  loading={stats.isFetching}
                  onClick={() => void stats.refetch()}
                >
                  {copy.actions.retry}
                </Button>
              </div>
            ) : (
              <dl className="divide-y divide-line">
                <div className="px-4 py-3">
                  <Stat
                    label={home.stats.models}
                    value={stats.data ? formatCount(stats.data.models_on_sale) : <StatPulse />}
                  />
                </div>
                <div className="px-4 py-3">
                  <Stat
                    label={home.stats.lowestMultiplier}
                    tone="brand"
                    value={
                      stats.data ? (
                        stats.data.min_multiplier_milli > 0 ? (
                          `${formatMultiplier(stats.data.min_multiplier_milli)}x`
                        ) : (
                          copy.common.none
                        )
                      ) : (
                        <StatPulse />
                      )
                    }
                    hint={
                      stats.data && stats.data.models_on_sale === 0
                        ? home.topEmptyTitle
                        : undefined
                    }
                  />
                </div>
                <div className="px-4 py-3">
                  <Stat
                    label={home.stats.callsToday}
                    value={stats.data ? formatCount(stats.data.today_requests) : <StatPulse />}
                  />
                </div>
              </dl>
            )}
            <p className="border-t border-line px-4 py-2.5 text-[11px] leading-5 text-muted">
              {home.statsHint}
            </p>
          </aside>
        </div>
      </section>

      <section className="border-b border-line">
        <div className="mx-auto max-w-[1180px] px-5 py-12">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <h2 className="text-[22px] font-semibold leading-snug tracking-tight text-ink">
                {home.topTitle}
              </h2>
              <p className="mt-1 text-[13px] leading-6 text-muted">{home.topLead}</p>
            </div>
            {models.data && models.data.length > 8 ? (
              <Link
                to="/models"
                className="shrink-0 text-[12.5px] text-brand-dark hover:underline"
              >
                {home.viewAll(models.data.length)}
              </Link>
            ) : null}
          </div>

          <div className="mt-6">
            {models.isError ? (
              <QueryError
                title={home.topErrorTitle}
                reason={home.topErrorReason}
                onRetry={() => void models.refetch()}
                retrying={models.isFetching}
              />
            ) : models.isPending ? (
              <ModelTable models={[]} loading skeletonRows={8} />
            ) : top.length === 0 ? (
              <Empty
                title={home.topEmptyTitle}
                reason={home.topEmptyReason}
                next={home.topEmptyNext}
                action={
                  <Link to="/seller" className={buttonClasses('secondary', 'md')}>
                    {copy.actions.becomeSeller}
                  </Link>
                }
              />
            ) : (
              <ModelTable models={top} details={details} detailsLoading={detailsLoading} />
            )}
          </div>

          <p className="mt-3 text-[11.5px] text-muted">{copy.pages.models.priceNote}</p>
        </div>
      </section>

      <section className="border-b border-line bg-surface">
        <div className="mx-auto max-w-[1180px] px-5 py-14">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
            <h2 className="text-[22px] font-semibold leading-snug tracking-tight text-ink">
              {home.steps.title}
            </h2>
            <p className="text-[12.5px] text-muted">{home.steps.hint}</p>
          </div>

          <ol className="mt-8 grid gap-8 sm:grid-cols-3">
            <li className="border-t-2 border-brand/70 pt-4">
              <span className="font-numeric text-[12px] text-brand-dark">01</span>
              <h3 className="mt-2 text-[15px] font-semibold text-ink">{home.steps.items[0].title}</h3>
              <p className="mt-1 text-[13px] leading-6 text-muted">{home.steps.items[0].body}</p>
              <Link
                to="/register"
                className="mt-2 inline-block text-[12.5px] text-brand-dark hover:underline"
              >
                {copy.actions.register}
              </Link>
            </li>
            <li className="border-t-2 border-brand/70 pt-4">
              <span className="font-numeric text-[12px] text-brand-dark">02</span>
              <h3 className="mt-2 text-[15px] font-semibold text-ink">{home.steps.items[1].title}</h3>
              <p className="mt-1 text-[13px] leading-6 text-muted">{home.steps.items[1].body}</p>
              <p className="mt-2 text-[12.5px] text-muted">
                {home.stepsKeyHint}{' '}
                <Link to="/console" className="text-brand-dark hover:underline">
                  {home.stepsKeyAction}
                </Link>
              </p>
            </li>
            <li className="border-t-2 border-brand/70 pt-4">
              <span className="font-numeric text-[12px] text-brand-dark">03</span>
              <h3 className="mt-2 text-[15px] font-semibold text-ink">{home.steps.items[2].title}</h3>
              <p className="mt-1 text-[13px] leading-6 text-muted">{home.steps.items[2].body}</p>
            </li>
          </ol>

          <div className="mt-8 max-w-[820px]">
            <div className="mb-2 flex flex-col gap-1 sm:flex-row sm:items-baseline sm:justify-between">
              <h3 className="text-[13px] font-medium text-ink">{home.stepsCurlTitle}</h3>
              <span className="text-[11.5px] text-muted">{home.stepsCurlNote}</span>
            </div>
            <CodeBlock code={home.firstCurl(baseUrl, sampleModel)} label="curl" />
          </div>
        </div>
      </section>

      <section className="mx-auto max-w-[1180px] px-5 py-12">
        <div className="flex flex-col gap-4 border-l-[3px] border-brand-dark bg-brand-soft px-5 py-5 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-[16px] font-semibold text-ink">{home.seller.title}</h2>
            <p className="mt-1 text-[13px] leading-6 text-muted">{home.seller.body}</p>
          </div>
          <Link to="/seller" className={buttonClasses('primary', 'md', 'shrink-0')}>
            {copy.actions.becomeSeller}
          </Link>
        </div>
      </section>
    </>
  )
}
