import { createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { Button, Empty, PageHeader, Stat, useToast } from '@/ui'

export const Route = createFileRoute('/seller/')({
  component: SellerOverviewPage,
})

function SellerOverviewPage() {
  const toast = useToast()
  const page = copy.pages.seller

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <div className="mt-6 grid gap-6 border-b border-line pb-6 sm:grid-cols-3 sm:divide-x sm:divide-line">
        <Stat
          label={page.stats.pending}
          value={copy.common.none}
          hint={copy.common.dataPending}
          className="sm:pr-6"
        />
        <Stat
          label={page.stats.withdrawable}
          value={copy.common.none}
          hint={copy.common.dataPending}
          className="sm:px-6"
        />
        <Stat
          label={page.stats.incomeThisMonth}
          value={copy.common.none}
          hint={copy.common.dataPending}
          tone="brand"
          className="sm:pl-6"
        />
      </div>

      <ol className="mt-8 grid gap-px border border-line bg-line sm:grid-cols-3">
        {page.steps.map((step, index) => (
          <li key={step} className="bg-surface p-4">
            <span className="font-numeric text-[11px] text-brand-dark">0{index + 1}</span>
            <p className="mt-1 text-[13px] text-ink">{step}</p>
          </li>
        ))}
      </ol>

      <Empty
        className="mt-6"
        title={page.empty.title}
        reason={page.empty.reason}
        next={page.empty.next}
        action={
          <Button onClick={() => toast.show({ title: page.onboardingSoon })}>
            {copy.actions.startOnboarding}
          </Button>
        }
      />
    </>
  )
}
