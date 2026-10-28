import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { meQuery } from '@/features/seller/api'
import { isSellerActive } from '@/features/seller/status'
import { useAuth } from '@/lib/auth'
import { PageHeader, Tag, buttonClasses } from '@/ui'

export const Route = createFileRoute('/share')({
  component: SharePage,
})

const page = copy.pages.share

function SharePage() {
  const { isLoggedIn } = useAuth()
  const me = useQuery({ ...meQuery(), enabled: isLoggedIn })
  const active = me.data ? isSellerActive(me.data.seller) : false

  const action = !isLoggedIn ? (
    <Link to="/login" search={{ redirect: '/console/onboarding' }} className={buttonClasses('primary', 'md')}>
      {page.ctaLogin}
    </Link>
  ) : active ? (
    <Link to="/console/listings" className={buttonClasses('primary', 'md')}>
      {page.ctaActive}
    </Link>
  ) : (
    <Link to="/console/onboarding" className={buttonClasses('primary', 'md')}>
      {page.cta}
    </Link>
  )

  return (
    <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={action}
        className="border-b-0 pb-0"
      />
      <p className="mt-4 max-w-2xl text-[15px] leading-7 text-ink-2">{page.heroBody}</p>

      <section className="mt-12">
        <h2 className="text-[24px] font-semibold tracking-tight text-ink">{page.stepsTitle}</h2>
        <div className="mt-5 grid gap-4 md:grid-cols-3">
          {page.steps.map((step, index) => (
            <div key={step.title} className="glass glass-lift rounded-lg p-6">
              <Tag tone="brand">{String(index + 1)}</Tag>
              <h3 className="mt-3 text-[16px] font-semibold text-ink">{step.title}</h3>
              <p className="mt-1 text-[14px] leading-6 text-ink-2">{step.body}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="mt-10 glass-inset rounded-lg p-6">
        <h2 className="text-[18px] font-semibold text-ink">{page.feeTitle}</h2>
        <p className="mt-2 text-[14px] leading-6 text-ink-2">{page.feeBody}</p>
        <p className="mt-2 text-[13px] leading-5 text-ink-3">{page.note}</p>
      </section>
    </div>
  )
}
