import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { Stat, buttonClasses } from '@/ui'

export const Route = createFileRoute('/')({
  component: HomePage,
})

function HomePage() {
  const home = copy.pages.home

  return (
    <>
      <section className="border-b border-line">
        <div className="mx-auto grid max-w-[1180px] gap-10 px-5 pb-12 pt-14 sm:pt-16 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-center">
          <div>
            <h1 className="max-w-[560px] text-[32px] font-semibold leading-[1.22] tracking-tight text-ink sm:text-[42px]">
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
            <p className="mt-5 text-[11.5px] text-muted">{home.kicker}</p>
          </div>

          <dl className="border-t border-line text-[12.5px]">
            {home.facts.map((fact) => (
              <div
                key={fact.label}
                className="flex items-baseline justify-between gap-4 border-b border-line/70 py-2.5"
              >
                <dt className="text-muted">{fact.label}</dt>
                <dd className="font-medium text-ink">{fact.value}</dd>
              </div>
            ))}
          </dl>
        </div>
      </section>

      <section className="border-b border-line bg-surface">
        <div className="mx-auto grid max-w-[1180px] divide-y divide-line px-5 sm:grid-cols-3 sm:divide-x sm:divide-y-0">
          <div className="py-5 sm:px-6 sm:first:pl-0">
            <Stat
              label={home.stats.models}
              value={copy.common.none}
              hint={copy.common.dataPending}
            />
          </div>
          <div className="py-5 sm:px-6">
            <Stat
              label={home.stats.lowestMultiplier}
              value={copy.common.none}
              hint={copy.common.dataPending}
            />
          </div>
          <div className="py-5 sm:px-6">
            <Stat
              label={home.stats.callsToday}
              value={copy.common.none}
              hint={copy.common.dataPending}
            />
          </div>
        </div>
      </section>

      <section className="mx-auto max-w-[1180px] px-5 py-14">
        <div className="grid gap-8 lg:grid-cols-[260px_1fr]">
          <h2 className="text-[22px] font-semibold leading-snug tracking-tight text-ink">
            {home.why.title}
          </h2>
          <div className="grid gap-px border border-line bg-line sm:grid-cols-3">
            {home.why.items.map((item) => (
              <div key={item.title} className="bg-paper p-5">
                <h3 className="text-[14px] font-semibold text-ink">{item.title}</h3>
                <p className="mt-2 text-[13px] leading-6 text-muted">{item.body}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className="border-y border-line bg-surface">
        <div className="mx-auto max-w-[1180px] px-5 py-14">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
            <h2 className="text-[22px] font-semibold leading-snug tracking-tight text-ink">
              {home.steps.title}
            </h2>
            <p className="text-[12.5px] text-muted">{home.steps.hint}</p>
          </div>
          <ol className="mt-8 grid gap-8 sm:grid-cols-3">
            {home.steps.items.map((item, index) => (
              <li key={item.title} className="border-t-2 border-brand/70 pt-4">
                <span className="font-numeric text-[12px] text-brand-dark">
                  0{index + 1}
                </span>
                <h3 className="mt-2 text-[15px] font-semibold text-ink">{item.title}</h3>
                <p className="mt-1 text-[13px] leading-6 text-muted">{item.body}</p>
              </li>
            ))}
          </ol>
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
