import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useId, useMemo, useState } from 'react'

import { copy } from '@/copy'
import { publicStatsQuery, squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import { CodeBlock } from '@/features/market/code-block'
import { CopyButton } from '@/features/market/copy-button'
import { formatCount } from '@/features/market/format'
import { ModelTable } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { currentBaseUrl } from '@/features/market/url'
import { savingPercent } from '@/lib/multiplier'
import { Empty, buttonClasses } from '@/ui'

export const Route = createFileRoute('/')({
  component: HomePage,
})

const CONTAINER = 'mx-auto w-full max-w-[1200px] px-5 sm:px-6'
const SECTION = 'py-16 sm:py-20 lg:py-[120px]'

/**
 * Shipped vendor marks. The logo wall only renders vendors that actually
 * appear in the square payload, so no empty or invented logos are shown.
 */
const LOGO_VENDORS: { key: string; label: string; logo: string; match: RegExp }[] = [
  { key: 'deepseek', label: 'DeepSeek', logo: '/vendors/deepseek.svg', match: /deepseek/i },
  { key: 'zhipu', label: '智谱', logo: '/vendors/zhipu.svg', match: /zhipu|bigmodel|glm/i },
  { key: 'qwen', label: '千问', logo: '/vendors/qwen.svg', match: /qwen|alibaba|dashscope|aliyun/i },
  { key: 'kimi', label: 'Kimi', logo: '/vendors/kimi.svg', match: /moonshot|kimi/i },
  { key: 'minimax', label: 'MiniMax', logo: '/vendors/minimax.svg', match: /minimax/i },
  { key: 'doubao', label: '豆包', logo: '/vendors/doubao.svg', match: /doubao|volc|bytedance/i },
]

function FaqItem({ question, answer }: { question: string; answer: string }) {
  const [open, setOpen] = useState(false)
  const panelId = useId()

  return (
    <div className="border-b border-line">
      <h3>
        <button
          type="button"
          aria-expanded={open}
          aria-controls={panelId}
          onClick={() => setOpen((value) => !value)}
          className="flex w-full items-center justify-between gap-4 py-5 text-left text-[15px] font-medium text-ink transition-colors hover:text-brand"
        >
          <span>{question}</span>
          <span aria-hidden="true" className="text-[18px] leading-none text-ink-3">
            {open ? '−' : '+'}
          </span>
        </button>
      </h3>
      {open ? (
        <p id={panelId} className="pb-5 pr-8 text-[14px] leading-6 text-ink-2">
          {answer}
        </p>
      ) : null}
    </div>
  )
}

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

  const logoVendors = useMemo(() => {
    const data = models.data
    if (!data || data.length === 0) return []
    return LOGO_VENDORS.filter((vendor) =>
      data.some(
        (model) => vendor.match.test(model.origin_vendor) || vendor.match.test(model.vendor),
      ),
    )
  }, [models.data])

  // §0：没有在售模型时整条数据条隐藏，不出「0 / —」这种占位观感的数字。
  // 优惠幅度没有真实数据时只隐藏那一项，另外两项照常显示。
  const statItems =
    stats.data && stats.data.models_on_sale > 0
      ? [
          { value: formatCount(stats.data.models_on_sale), label: home.stats.models },
          ...(stats.data.min_multiplier_milli > 0
            ? [
                {
                  value: `${savingPercent(stats.data.min_multiplier_milli)}%`,
                  label: home.stats.maxSaving,
                },
              ]
            : []),
          { value: formatCount(stats.data.today_requests), label: home.stats.callsToday },
        ]
      : []

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

          {statItems.length > 0 ? (
            <div className="mx-auto mt-16 flex max-w-[760px] flex-wrap items-start justify-center gap-x-16 gap-y-10">
              {statItems.map((item) => (
                <div key={item.label} className="min-w-[120px] text-center">
                  <div className="text-[32px] font-semibold leading-tight tracking-tight tabular-nums text-ink">
                    {item.value}
                  </div>
                  <div className="mt-1 text-[13px] text-ink-3">{item.label}</div>
                </div>
              ))}
            </div>
          ) : null}

          {logoVendors.length > 0 ? (
            <div data-testid="vendor-logo-wall" className="mx-auto mt-14 max-w-[880px] text-center">
              <p className="text-[13px] text-ink-3">{home.logoWall.title}</p>
              <ul
                aria-label={home.logoWall.aria}
                className="mt-5 flex flex-wrap items-center justify-center gap-x-8 gap-y-4"
              >
                {logoVendors.map((vendor) => (
                  <li key={vendor.key} className="group flex items-center gap-2">
                    <img
                      src={vendor.logo}
                      alt={vendor.label}
                      width={28}
                      height={28}
                      className="h-7 w-7 rounded-md opacity-60 grayscale transition duration-200 group-hover:opacity-100 group-hover:grayscale-0"
                    />
                    <span className="text-[13px] font-medium text-ink-3 transition-colors group-hover:text-ink">
                      {vendor.label}
                    </span>
                  </li>
                ))}
              </ul>
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
                  <Link to="/share" className={buttonClasses('secondary', 'md')}>
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
              {home.flow.title}
            </h2>
            <p className="mt-2 text-[15px] leading-6 text-ink-2">{home.flow.hint}</p>
          </div>

          <ol className="mt-10 flex flex-col sm:flex-row">
            {home.flow.items.map((item, index) => (
              <li key={item.title} className="flex flex-1 flex-col sm:flex-row sm:items-start">
                <div className="flex flex-1 gap-3 sm:flex-col">
                  <div className="flex flex-col items-center sm:w-full sm:flex-row">
                    <span aria-hidden="true" className="h-2.5 w-2.5 shrink-0 rounded-full bg-brand" />
                    {index < home.flow.items.length - 1 ? (
                      <span
                        aria-hidden="true"
                        className="my-1.5 w-px flex-1 bg-line-strong sm:my-0 sm:ml-2 sm:h-px sm:w-auto sm:flex-1"
                      />
                    ) : null}
                  </div>
                  <div className="min-w-0 pb-8 sm:mt-3 sm:pb-0 sm:pr-8">
                    <h3 className="text-[15px] font-semibold text-ink">{item.title}</h3>
                    <p className="mt-1 text-[13px] leading-6 text-ink-2">{item.body}</p>
                  </div>
                </div>
              </li>
            ))}
          </ol>
        </div>
      </section>

      <section className={SECTION}>
        <div className={CONTAINER}>
          <div className="max-w-[640px]">
            <h2 className="text-[24px] font-semibold leading-snug tracking-tight text-ink">
              {home.audience.title}
            </h2>
            <p className="mt-2 text-[15px] leading-6 text-ink-2">{home.audience.hint}</p>
          </div>

          <div className="mt-10 grid gap-6 md:grid-cols-2">
            {[
              { card: home.audience.buyer, href: '/register' as const, primary: false },
              { card: home.audience.seller, href: '/share' as const, primary: true },
            ].map(({ card, href, primary }) => (
              <div
                key={card.label}
                className="flex flex-col rounded-lg border border-line bg-bg p-6 sm:p-8"
              >
                <span className="w-fit rounded-full bg-bg-soft px-3 py-1 text-[12px] font-medium text-ink-2">
                  {card.label}
                </span>
                <h3 className="mt-4 text-[20px] font-semibold tracking-tight text-ink">
                  {card.title}
                </h3>
                <ul className="mt-5 space-y-3">
                  {card.points.map((point) => (
                    <li key={point} className="flex gap-2.5 text-[14px] leading-6 text-ink-2">
                      <span
                        aria-hidden="true"
                        className="mt-[9px] h-1.5 w-1.5 shrink-0 rounded-full bg-brand"
                      />
                      <span>{point}</span>
                    </li>
                  ))}
                </ul>
                <div className="mt-auto pt-8">
                  <Link
                    to={href}
                    className={
                      primary
                        ? buttonClasses('primary', 'md')
                        : 'text-[14px] font-medium text-brand transition-colors hover:text-brand-hover hover:underline'
                    }
                  >
                    {card.cta}
                    {primary ? null : ' →'}
                  </Link>
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className={SECTION}>
        <div className={CONTAINER}>
          <div className="max-w-[640px]">
            <h2 className="text-[24px] font-semibold leading-snug tracking-tight text-ink">
              {home.examples.title}
            </h2>
            <p className="mt-2 text-[15px] leading-6 text-ink-2">{home.examples.hint}</p>
          </div>

          <div className="mt-10 grid min-w-0 gap-5 lg:grid-cols-3">
            <div className="min-w-0">
              <CodeBlock
                code={home.firstCurl(baseUrl, sampleModel)}
                label={home.examples.labels.curl}
              />
            </div>
            <div className="min-w-0">
              <CodeBlock
                code={home.examples.python(baseUrl, sampleModel)}
                label={home.examples.labels.python}
              />
            </div>
            <div className="min-w-0">
              <CodeBlock
                code={home.examples.node(baseUrl, sampleModel)}
                label={home.examples.labels.node}
              />
            </div>
          </div>
        </div>
      </section>

      <section className={SECTION}>
        <div className={CONTAINER}>
          <div className="mx-auto max-w-[760px]">
            <h2 className="text-[24px] font-semibold leading-snug tracking-tight text-ink">
              {home.faq.title}
            </h2>
            <p className="mt-2 text-[15px] leading-6 text-ink-2">{home.faq.hint}</p>
            <div className="mt-8 border-t border-line">
              {home.faq.items.map((item) => (
                <FaqItem key={item.q} question={item.q} answer={item.a} />
              ))}
            </div>
          </div>
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
            <Link to="/share" className={buttonClasses('primary', 'md', 'shrink-0')}>
              {home.ctaShare}
            </Link>
          </div>
        </div>
      </section>
    </div>
  )
}
