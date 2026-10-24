import { createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { useAuth } from '@/lib/auth'
import { Empty, PageHeader, Stat, Tag } from '@/ui'

export const Route = createFileRoute('/admin/')({
  component: AdminPage,
})

function AdminPage() {
  const { isRoot } = useAuth()
  const page = copy.pages.admin

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={<Tag tone="warning">{copy.common.sample}</Tag>}
      />

      <div className="mt-6 grid gap-6 border-b border-line pb-6 sm:grid-cols-3 sm:divide-x sm:divide-line">
        <Stat
          label={page.stats.pendingListings}
          value={copy.common.none}
          hint={copy.common.dataPending}
          className="sm:pr-6"
        />
        <Stat
          label={page.stats.pendingPayouts}
          value={copy.common.none}
          hint={copy.common.dataPending}
          className="sm:px-6"
        />
        <Stat
          label={page.stats.openReversals}
          value={copy.common.none}
          hint={copy.common.dataPending}
          tone="warning"
          className="sm:pl-6"
        />
      </div>

      <Empty
        className="mt-8"
        title={page.empty.title}
        reason={page.empty.reason}
        next={page.empty.next}
      />

      {isRoot ? (
        <p className="mt-4 border border-line bg-wash px-4 py-3 font-mono text-[11.5px] leading-6 text-muted">
          {page.legacy}
        </p>
      ) : null}
    </>
  )
}
