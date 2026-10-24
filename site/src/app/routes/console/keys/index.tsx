import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  Button,
  Empty,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  buttonClasses,
} from '@/ui'

import { consoleKeysQuery } from '@/features/console/api'
import { CreateKeyDialog } from '@/features/console/components/CreateKeyDialog'
import { ErrorBlock, PageSkeleton, Section } from '@/features/console/components/DataState'
import { tokenStatusMeta } from '@/features/console/labels'
import {
  type ConsoleKey,
  type Strategy,
} from '@/features/console/types'

export const Route = createFileRoute('/console/keys/')({
  component: KeysPage,
})

const page = copy.pages.consoleKeys

const strategyLabelMap: Record<Strategy, string> = {
  cheapest: page.strategy.cheapest,
  balanced: page.strategy.balanced,
  stable: page.strategy.stable,
}

const strategyHintMap: Record<Strategy, string> = {
  cheapest: page.strategyHint.cheapest,
  balanced: page.strategyHint.balanced,
  stable: page.strategyHint.stable,
}

function KeysPage() {
  const keys = useQuery(consoleKeysQuery())
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [wizardOpen, setWizardOpen] = useState(false)

  function handleCreated(tokenId: number | null) {
    void queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
    if (tokenId !== null) {
      void navigate({ to: '/console/keys/$id', params: { id: String(tokenId) } })
    }
  }

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Button size="sm" onClick={() => setWizardOpen(true)}>
            {page.create}
          </Button>
        }
      />

      {keys.isPending ? (
        <div className="mt-6">
          <PageSkeleton />
        </div>
      ) : keys.isError ? (
        <ErrorBlock
          className="mt-6"
          error={keys.error}
          onRetry={() => void keys.refetch()}
        />
      ) : keys.data.length === 0 ? (
        <Empty
          className="mt-6"
          title={page.empty.title}
          reason={page.empty.reason}
          next={page.empty.next}
          action={<Button onClick={() => setWizardOpen(true)}>{page.create}</Button>}
        />
      ) : (
        <div className="mt-6">
          <Section title={copy.pages.console.nav.keys} hint={page.lead}>
            <Table minWidth={880}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{page.columns.name}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.models}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.strategy}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.spend}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.actions}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {keys.data.map((item) => (
                  <KeyRow key={item.token.id} item={item} />
                ))}
              </TableBody>
            </Table>
          </Section>
        </div>
      )}

      <CreateKeyDialog
        open={wizardOpen}
        onOpenChange={setWizardOpen}
        onCreated={handleCreated}
      />
    </>
  )
}

function KeyRow({ item }: { item: ConsoleKey }) {
  const { token, pref } = item
  const status = tokenStatusMeta(token.status)
  const strategy = pref?.strategy ?? null
  return (
    <TableRow interactive>
      <TableCell>
        <Link
          to="/console/keys/$id"
          params={{ id: String(token.id) }}
          className="font-medium text-ink hover:text-brand-dark"
        >
          {token.name || copy.pages.consoleShared.none}
        </Link>
        <div className="font-numeric text-[11px] text-muted">{token.key}</div>
      </TableCell>
      <TableCell numeric>
        <span className="text-[12.5px]">
          {copy.pages.consoleKeys.modelsUnit(pref?.models.length ?? 0)}
        </span>
      </TableCell>
      <TableCell>
        {strategy ? (
          <>
            <Tag tone="brand">{strategyLabelMap[strategy]}</Tag>
            <div className="mt-1 text-[11px] text-muted">{strategyHintMap[strategy]}</div>
          </>
        ) : (
          <span className="text-[12px] text-muted">{copy.pages.consoleShared.none}</span>
        )}
      </TableCell>
      <TableCell numeric>
        <div className="text-[12.5px]">{page.spend.used(formatYuan(token.used_quota))}</div>
        <div className="text-[11px] text-muted">
          {token.unlimited_quota
            ? page.spend.unlimited
            : page.spend.remaining(formatYuan(token.remain_quota))}
        </div>
      </TableCell>
      <TableCell>
        <Tag tone={status.tone} dot>
          {status.label}
        </Tag>
      </TableCell>
      <TableCell>
        <Link
          to="/console/keys/$id"
          params={{ id: String(token.id) }}
          className={buttonClasses('secondary', 'sm')}
        >
          {copy.pages.consoleShared.detail}
        </Link>
      </TableCell>
    </TableRow>
  )
}
