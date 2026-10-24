import { createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { useAuth } from '@/lib/auth'
import { Button, Empty, PageHeader, Stat, useToast } from '@/ui'

export const Route = createFileRoute('/console/')({
  component: ConsoleOverviewPage,
})

function ConsoleOverviewPage() {
  const { user } = useAuth()
  const toast = useToast()
  const page = copy.pages.console

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          user ? (
            <span className="text-[12px] text-muted">
              {user.display_name || user.username}
            </span>
          ) : null
        }
      />

      <div className="mt-6 grid gap-6 border-b border-line pb-6 sm:grid-cols-3 sm:divide-x sm:divide-line">
        <Stat
          label={page.stats.balance}
          value={copy.common.none}
          hint={copy.common.dataPending}
          className="sm:pr-6"
        />
        <Stat
          label={page.stats.spendThisMonth}
          value={copy.common.none}
          hint={copy.common.dataPending}
          className="sm:px-6"
        />
        <Stat
          label={page.stats.savedThisMonth}
          value={copy.common.none}
          hint={copy.common.dataPending}
          tone="success"
          className="sm:pl-6"
        />
      </div>

      <Empty
        className="mt-8"
        title={page.empty.title}
        reason={page.empty.reason}
        next={page.empty.next}
        action={
          <Button
            onClick={() => toast.show({ title: page.keySoon, tone: 'default' })}
          >
            {copy.actions.createKey}
          </Button>
        }
      />
    </>
  )
}
