import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Empty,
  Input,
  PageHeader,
  Stat,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  useToast,
} from '@/ui'

import { redeemCode, walletLedgerQuery, walletQuery } from '@/features/console/api'
import {
  ErrorBlock,
  Section,
  Skeleton,
  StatSkeleton,
} from '@/features/console/components/DataState'
import { Notice } from '@/features/console/components/form'
import { ledgerEntryLabel, ledgerStatusLabel } from '@/features/console/labels'
import { redeemCodeSchema } from '@/features/console/schemas'

const PAGE_SIZE = 20

export const Route = createFileRoute('/console/wallet')({
  validateSearch: (search: Record<string, unknown>): { page: number } => {
    const parsed = Number(search.page)
    return { page: Number.isInteger(parsed) && parsed > 0 ? parsed : 1 }
  },
  component: WalletPage,
})

const page = copy.pages.consoleWallet

function WalletPage() {
  const { page: currentPage } = Route.useSearch()
  const navigate = useNavigate()
  const wallet = useQuery(walletQuery())
  const ledger = useQuery(walletLedgerQuery(currentPage, PAGE_SIZE))

  const total = ledger.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  function goTo(next: number) {
    void navigate({
      to: '/console/wallet',
      search: { page: Math.min(Math.max(1, next), totalPages) },
    })
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <Notice tone="warning" title={page.warningTitle} className="mt-5">
        {page.warning}
      </Notice>

      {wallet.isPending ? (
        <div className="mt-6 grid gap-6 border-b border-line pb-6 sm:grid-cols-3">
          <StatSkeleton />
          <StatSkeleton />
          <StatSkeleton />
        </div>
      ) : wallet.isError ? (
        <ErrorBlock
          className="mt-6"
          error={wallet.error}
          onRetry={() => void wallet.refetch()}
        />
      ) : (
        <div className="mt-6 grid gap-6 border-b border-line pb-6 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)] sm:divide-x sm:divide-line">
          <div className="min-w-0 sm:pr-6">
            <div className="text-[12px] text-muted">{page.balance}</div>
            <div className="mt-1 break-all font-numeric text-[32px] font-medium leading-10 tabular-nums text-brand-dark">
              {formatYuan(wallet.data.balance_micro)}
            </div>
            <div className="mt-1 text-[11px] leading-5 text-muted/80">{page.balanceHint}</div>
          </div>
          <Stat
            label={page.monthSpend}
            value={formatYuan(wallet.data.month_spend_micro)}
            className="sm:px-6"
          />
          <Stat
            label={page.monthSaved}
            value={formatYuan(wallet.data.month_saved_micro)}
            tone="success"
            className="sm:pl-6"
          />
        </div>
      )}

      <div className="mt-7 grid gap-4 lg:grid-cols-2">
        <section className="border border-line bg-surface p-4 sm:p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-[14px] font-semibold tracking-tight text-ink">
              {page.recharge.title}
            </h2>
            <Tag tone="neutral">{page.recharge.soon}</Tag>
          </div>
          <p className="mt-2 text-[12.5px] leading-6 text-muted">{page.recharge.body}</p>
          <Button className="mt-4" variant="secondary" disabled>
            {page.recharge.action}
          </Button>
        </section>

        <RedeemPanel />
      </div>

      <Section className="mt-9" title={page.ledger.title} hint={page.ledger.hint}>
        {ledger.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-5 w-full" />
            <Skeleton className="h-5 w-full" />
            <Skeleton className="h-5 w-4/5" />
          </div>
        ) : ledger.isError ? (
          <ErrorBlock error={ledger.error} onRetry={() => void ledger.refetch()} />
        ) : ledger.data.items.length === 0 ? (
          <Empty
            title={page.ledger.emptyTitle}
            reason={page.ledger.emptyReason}
            next={page.ledger.emptyNext}
          />
        ) : (
          <>
            <Table minWidth={760}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{page.ledger.columns.time}</TableHeaderCell>
                  <TableHeaderCell>{page.ledger.columns.type}</TableHeaderCell>
                  <TableHeaderCell>{page.ledger.columns.request}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.ledger.columns.amount}</TableHeaderCell>
                  <TableHeaderCell>{page.ledger.columns.status}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {ledger.data.items.map((entry, index) => (
                  <TableRow key={`${entry.request_id}-${entry.entry_type}-${index}`}>
                    <TableCell className="whitespace-nowrap font-numeric text-[12px] text-muted">
                      {formatDateTime(entry.created_at)}
                    </TableCell>
                    <TableCell>{ledgerEntryLabel(entry.entry_type)}</TableCell>
                    <TableCell className="max-w-[220px] truncate font-numeric text-[11.5px] text-muted">
                      {entry.request_id || copy.pages.consoleShared.none}
                    </TableCell>
                    <TableCell
                      numeric
                      className={entry.entry_type === 'refund' ? 'text-success' : undefined}
                    >
                      {formatYuan(entry.amount_micro)}
                    </TableCell>
                    <TableCell>
                      <LedgerStatus status={entry.status} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
              <span className="font-numeric text-[11.5px] text-muted">
                {copy.pages.consoleRecords.pagination.summary(currentPage, totalPages)}
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={currentPage <= 1}
                  onClick={() => goTo(currentPage - 1)}
                >
                  {page.pagination.previous}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={currentPage >= totalPages}
                  onClick={() => goTo(currentPage + 1)}
                >
                  {page.pagination.next}
                </Button>
              </div>
            </div>
          </>
        )}
      </Section>
    </>
  )
}

function LedgerStatus({ status }: { status: string }) {
  const label = ledgerStatusLabel(status)
  if (status === 'available') return <Tag tone="success">{label}</Tag>
  if (status === 'pending') return <Tag tone="warning">{label}</Tag>
  if (status === 'reversed') return <Tag tone="danger">{label}</Tag>
  return <Tag tone="neutral">{label}</Tag>
}

function RedeemPanel() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function submit() {
    setError(null)
    const parsed = redeemCodeSchema.safeParse(code)
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? page.redeem.required)
      return
    }
    setSubmitting(true)
    try {
      const quota = await redeemCode(parsed.data)
      toast.show({
        title: page.redeem.success(formatYuan(quota)),
        tone: 'success',
      })
      setCode('')
      await queryClient.invalidateQueries({ queryKey: ['console', 'wallet'] })
      await queryClient.invalidateQueries({ queryKey: ['console', 'wallet-ledger'] })
    } catch (redeemError) {
      setError(
        redeemError instanceof ApiError ? redeemError.message : copy.pages.consoleShared.errorHint,
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="border border-line bg-surface p-4 sm:p-5">
      <h2 className="text-[14px] font-semibold tracking-tight text-ink">{page.redeem.title}</h2>
      <p className="mt-2 text-[12.5px] leading-6 text-muted">{page.redeem.hint}</p>
      <form
        className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-start"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
        noValidate
      >
        <div className="min-w-0 flex-1">
          <label htmlFor="redeem-code" className="mb-1.5 block text-[12px] font-medium text-ink">
            {page.redeem.label}
          </label>
          <Input
            id="redeem-code"
            mono
            autoComplete="off"
            placeholder={page.redeem.placeholder}
            value={code}
            invalid={Boolean(error)}
            onChange={(event) => setCode(event.target.value)}
          />
        </div>
        <Button type="submit" className="sm:mt-[26px]" loading={submitting}>
          {submitting ? page.redeem.redeeming : page.redeem.action}
        </Button>
      </form>
      {error ? (
        <p role="alert" className="mt-2 text-[12px] text-danger">
          {error}
        </p>
      ) : null}
    </section>
  )
}
