import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useMemo } from 'react'

import { copy } from '@/copy'
import { publicStatsQuery, squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import { CodeBlock } from '@/features/market/code-block'
import { CopyButton } from '@/features/market/copy-button'
import { formatCount } from '@/features/market/format'
import { ModelTable } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { currentBaseUrl } from '@/features/market/url'
import { formatMultiplier } from '@/lib/multiplier'
import { Empty, buttonClasses } from '@/ui'

export const Route = createFileRoute('/')({
  component: HomePage,
})

const CONTAINER = 'mx-auto w-full max-w-[1200px] px-5 sm:px-6'
const SECTION = 'py-16 sm:py-20 lg:py-[120px]'

export function HomePage() {
  const home = copy.pages.home
  const stats = useQuery(publicStatsQuery())
  const models = useQuery(squareModelsQuery())

  // "热门" has no public traffic number; listing_count is the only public
  // popularity proxy, so the strip is ordered by supply and then by model id.
  const top = useMemo(() => {
    return [...(models.data ?? [])]
      .sort((a, b) => b.listing_count - a.listing_count || a.model.localeCompare(b.model))
      .slice(0, 6)
  }, [models.data])

  const { details, loading: detailsLoading } = useSquareModelDetails(top)

  const sampleModel = top[0]?.model ?? 'deepseek-flash'
  const baseUrl = currentBaseUrl()
  // §0：没有在售挂单时（最低倍率只能显示占位）整条数据条隐藏，不出
  // 「0 / —」这种占位观感的数字。
  const statsReady = stats.data !== undefined && stats.data.models_on_sale > 0

  return (
    <div>
      <section className="relative overflow-hidden">
        {/* Decorative wash only: it must not affect flow or hit-testing. */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 top-0 h-[640px] bg-[linear-gradient(180deg,#eef1ff_0%,#ffffff_100%)]"
        />

        <div className="relative mx-auto w-full max-w-[1200px] px-5 pb-16 pt-16 sm:px-6 sm:pb-20 sm:pt-24 lg:pt-28">
          <div className="mx-auto max-w-[820px] text-center">
            <h1 className="text-[36px] font-semibold leading-[1.15] tracking-[-0.02em] text-ink sm:text-[56px]">
              {home.headline}
            </h1>
            <p className="mx-auto mt-5 max-w-[620px] text-[16px] leading-7 text-ink-2">{home.lead}</p>
            <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
              <Link to="/register" className={buttonClasses('primary', 'lg')}>
                {home.ctaStart}
              </Link>
              <Link to="/models" className={buttonClasses('secondary', 'lg')}>
                {copy.actions.browseModels}
              </Link>
            </div>
          </div>

          {statsReady ? (
            <div className="mx-auto mt-16 grid max-w-[760px] grid-cols-1 gap-10 sm:grid-cols-3">
              <div className="text-center">
                <div className="text-[32px] font-semibold leading-tight tracking-tight tabular-nums text-ink">
                  {formatCount(stats.data.models_on_sale)}
                </div>
                <div className="mt-1 text-[13px] text-ink-3">{home.stats.models}</div>
              </div>
              <div className="text-center">
                <div className="text-[32px] font-semibold leading-tight tracking-tight tabular-nums text-ink">
                  {stats.data.min_multiplier_milli > 0
                    ? `${formatMultiplier(stats.data.min_multiplier_milli)}x`
                    : copy.common.none}
                </div>
                <div className="mt-1 text-[13px] text-ink-3">{home.stats.lowestMultiplier}</div>
              </div>
              <div className="text-center">
                <div className="text-[32px] font-semibold leading-tight tracking-tight tabular-nums text-ink">
                  {formatCount(stats.data.today_requests)}
                </div>
                <div className="mt-1 text-[13px] text-ink-3">{home.stats.callsToday}</div>
              </div>
            </div>
          ) : null}

          <div className="mx-auto mt-14 max-w-[860px] rounded-lg border border-line bg-bg p-3 sm:p-4">
            <div className="flex items-center justify-between gap-3 rounded-sm px-2 py-1.5">
              <span className="shrink-0 text-[13px] text-ink-3">{home.baseUrlLabel}</span>
              <div className="flex min-w-0 items-center gap-1">
                <code className="truncate text-[13px] tabular-nums text-ink">{baseUrl}/v1</code>
                <CopyButton text={`${baseUrl}/v1`} />
              </div>
            </div>
            <CodeBlock code={home.firstCurl(baseUrl, sampleModel)} label="curl" className="mt-2" />
            <p className="mt-3 px-2 text-[13px] leading-6 text-ink-3">{home.stepsCurlNote}</p>
          </div>
        </div>
      </section>

      <section className={SECTION}>
        <div className={CONTAINER}>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <h2 className="text-[24px] font-semibold leading-snug tracking-tight text-ink">
                {home.topTitle}
              </h2>
              <p className="mt-2 text-[15px] leading-6 text-ink-2">{home.topLead}</p>
            </div>
            <Link
              to="/models"
              className="shrink-0 text-[14px] text-brand transition-colors hover:text-brand-hover hover:underline"
            >
              {home.viewAll} →
            </Link>
          </div>

          <div className="mt-8">
            {models.isError ? (
              <QueryError
                title={home.topErrorTitle}
                reason={home.topErrorReason}
                onRetry={() => void models.refetch()}
                retrying={models.isFetching}
              />
            ) : models.isPending ? (
              <ModelTable models={[]} loading skeletonRows={6} />
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

          <p className="mt-4 text-[13px] text-ink-3">{copy.pages.models.priceNote}</p>
        </div>
      </section>

      <section className={SECTION}>
        <div className={CONTAINER}>
          <div className="max-w-[640px]">
            <h2 className="text-[24px] font-semibold leading-snug tracking-tight text-ink">
              {home.steps.title}
            </h2>
            <p className="mt-2 text-[15px] leading-6 text-ink-2">{home.steps.hint}</p>
          </div>

          <ol className="mt-10 grid gap-10 sm:grid-cols-3 sm:gap-8">
            {home.steps.items.map((item, index) => (
              <li key={item.title}>
                <span className="grid h-7 w-7 place-items-center rounded-full bg-brand-soft text-[13px] font-medium tabular-nums text-brand-ink">
                  {index + 1}
                </span>
                <h3 className="mt-4 text-[16px] font-semibold text-ink">{item.title}</h3>
                <p className="mt-1.5 text-[14px] leading-6 text-ink-2">{item.body}</p>
                {index === 0 ? (
                  <Link
                    to="/register"
                    className="mt-2 inline-block text-[14px] text-brand hover:underline"
                  >
                    {copy.actions.register}
                  </Link>
                ) : null}
                {index === 1 ? (
                  <p className="mt-2 text-[14px] text-ink-2">
                    {home.stepsKeyHint}{' '}
                    <Link to="/console" className="text-brand hover:underline">
                      {home.stepsKeyAction}
                    </Link>
                  </p>
                ) : null}
              </li>
            ))}
          </ol>
        </div>
      </section>

      <section className="pb-16 sm:pb-20 lg:pb-[120px]">
        <div className={CONTAINER}>
          <div className="flex flex-col gap-6 rounded-lg bg-bg-soft px-6 py-8 sm:flex-row sm:items-center sm:justify-between sm:px-10 sm:py-10">
            <div className="min-w-0">
              <h2 className="text-[20px] font-semibold tracking-tight text-ink">{home.seller.title}</h2>
              <p className="mt-2 max-w-[560px] text-[15px] leading-6 text-ink-2">{home.seller.body}</p>
            </div>
            <Link to="/seller" className={buttonClasses('primary', 'md', 'shrink-0')}>
              {copy.actions.becomeSeller}
            </Link>
          </div>
        </div>
      </section>
    </div>
  )
}
