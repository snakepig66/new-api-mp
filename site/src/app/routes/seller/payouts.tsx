import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime, parseApiTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  Input,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  useToast,
} from '@/ui'

import {
  requestPayout,
  sellerBalanceQuery,
  sellerPayoutsQuery,
  type Payout,
} from '@/features/seller/api'
import { AsyncError, Field, PageSkeleton, TableSkeleton } from '@/features/seller/components'
import { notify } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { microToYuanInput, parseYuanToMicro } from '@/features/seller/money-input'
import { payoutStatusView } from '@/features/seller/status'

export const Route = createFileRoute('/seller/payouts')({
  component: SellerPayoutsPage,
})

const page = copy.pages.sellerPayouts
const commonErrors = copy.pages.sellerCommon.errors
const MIN_PAYOUT_MICRO = 50_000_000n
const WEEK_MS = 7 * 24 * 60 * 60 * 1000
const PAGE_SIZE = 10

function toBigInt(value: number | string): bigint {
  try {
    return BigInt(String(value))
  } catch {
    return 0n
  }
}

/** Mirrors the backend: a non-rejected payout inside the rolling 7-day window. */
function recentActivePayout(payouts: Payout[], now = Date.now()): Payout | undefined {
  return payouts.find((payout) => {
    if (payout.status === 'rejected') return false
    const date = parseApiTime(payout.created_at)
    if (!date) return false
    return now - date.getTime() < WEEK_MS
  })
}

function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `payout-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function PayoutDialog({
  open,
  availableMicro,
  onClose,
}: {
  open: boolean
  availableMicro: number | string
  onClose: () => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [amount, setAmount] = useState(() => microToYuanInput(availableMicro))
  const [idempotencyKey] = useState(() => newIdempotencyKey())
  const [error, setError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: requestPayout,
    onSuccess: () => {
      setError(null)
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
      notify(toast, { title: page.dialog.success, tone: 'success' })
      onClose()
    },
    onError: (err) => setError(sellerErrorMessage(err)),
  })

  function submit() {
    const micro = parseYuanToMicro(amount)
    if (micro === null) {
      setError(commonErrors.amount)
      return
    }
    if (micro < MIN_PAYOUT_MICRO) {
      setError(commonErrors.payoutMinimum)
      return
    }
    if (micro > toBigInt(availableMicro)) {
      setError(commonErrors.payoutInsufficient)
      return
    }
    setError(null)
    mutation.mutate({ amount_micro: Number(micro), idempotency_key: idempotencyKey })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={page.dialog.title}
      description={page.dialog.lead}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {copy.actions.close}
          </Button>
          <Button loading={mutation.isPending} onClick={submit}>
            {mutation.isPending ? page.dialog.submitting : page.dialog.submit}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field
          label={page.dialog.amountLabel}
          htmlFor="payout-amount"
          hint={page.dialog.hint(formatYuan(availableMicro))}
          error={error ?? undefined}
        >
          <Input
            id="payout-amount"
            mono
            inputMode="decimal"
            placeholder={page.dialog.amountPlaceholder}
            value={amount}
            onChange={(event) => {
              setAmount(event.target.value)
              setError(null)
            }}
          />
        </Field>
        <p className="text-[11px] text-muted">
          {page.min} · {page.weeklyHint}
        </p>
      </div>
    </Dialog>
  )
}

function SellerPayoutsPage() {
  const [pageNumber, setPageNumber] = useState(1)
  const [dialogOpen, setDialogOpen] = useState(false)
  const balance = useQuery(sellerBalanceQuery())
  const payouts = useQuery(sellerPayoutsQuery(pageNumber, PAGE_SIZE))

  if (balance.isPending) return <PageSkeleton />

  if (balance.isError) {
    return (
      <AsyncError
        title={page.loadError}
        reason={sellerErrorMessage(balance.error)}
        onRetry={() => void balance.refetch()}
        retryLabel={copy.actions.retry}
      />
    )
  }

  const data = balance.data
  const availableMicro = data?.available_micro ?? 0
  const available = toBigInt(availableMicro)
  const recent = data ? recentActivePayout(data.payouts ?? []) : undefined
  const canRequest = !recent && available >= MIN_PAYOUT_MICRO

  const historyRows = payouts.data?.items ?? []
  const total = payouts.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Button
            disabled={!canRequest}
            onClick={() => {
              setDialogOpen(true)
            }}
          >
            {page.request}
          </Button>
        }
      />

      <section className="mt-6 grid gap-6 border-b border-line pb-6 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
        <div>
          <div className="text-[12px] text-muted">{page.available}</div>
          <div className="mt-1 break-words font-numeric text-[34px] font-medium leading-tight tabular-nums text-brand-dark">
            {formatYuan(availableMicro)}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-muted">
            <span>{page.min}</span>
            <span className="h-3 w-px bg-line-strong" aria-hidden="true" />
            {recent ? (
              <Tag tone="warning" dot>
                {page.weekly}
              </Tag>
            ) : (
              <Tag tone="success" dot>
                {page.weeklyFree}
              </Tag>
            )}
          </div>
          <p className="mt-2 text-[11.5px] leading-5 text-muted">{page.weeklyHint}</p>
        </div>
        <div className="flex flex-col justify-end gap-3 border-t border-line pt-4 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0">
          <div>
            <div className="text-[12px] text-muted">{page.pending}</div>
            <div className="mt-1 font-numeric text-[21px] font-medium tabular-nums text-ink">
              {formatYuan(data?.pending_micro ?? 0)}
            </div>
          </div>
          <p className="text-[11.5px] leading-5 text-muted">{page.reserveHint}</p>
        </div>
      </section>

      <section className="mt-8">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 className="text-[15px] font-semibold tracking-tight text-ink">
              {page.history.title}
            </h2>
            <p className="mt-1 text-[12px] text-muted">{page.history.lead}</p>
          </div>
          <span className="font-numeric text-[11.5px] text-muted">
            {copy.pages.sellerCommon.totalCount(total)}
          </span>
        </div>

        <div className="mt-4">
          {payouts.isPending ? (
            <TableSkeleton rows={4} columns={4} />
          ) : payouts.isError ? (
            <Empty
              title={page.historyError}
              reason={sellerErrorMessage(payouts.error)}
              next={copy.common.retryHint}
              action={
                <Button variant="secondary" onClick={() => void payouts.refetch()}>
                  {copy.actions.retry}
                </Button>
              }
            />
          ) : historyRows.length === 0 ? (
            <Empty
              title={page.history.emptyTitle}
              reason={page.history.emptyReason}
              next={page.history.emptyNext}
            />
          ) : (
            <>
              <Table minWidth={720}>
                <TableHead>
                  <TableRow>
                    <TableHeaderCell>{page.history.columns.time}</TableHeaderCell>
                    <TableHeaderCell numeric>{page.history.columns.amount}</TableHeaderCell>
                    <TableHeaderCell>{page.history.columns.status}</TableHeaderCell>
                    <TableHeaderCell>{page.history.columns.reviewed}</TableHeaderCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {historyRows.map((payout) => {
                    const status = payoutStatusView(payout.status)
                    return (
                      <TableRow key={payout.id}>
                        <TableCell className="whitespace-nowrap font-numeric text-[12px] text-muted">
                          {formatDateTime(payout.created_at)}
                        </TableCell>
                        <TableCell numeric className="whitespace-nowrap font-medium">
                          {formatYuan(payout.amount_micro)}
                        </TableCell>
                        <TableCell>
                          <Tag tone={status.tone} dot>
                            {status.label}
                          </Tag>
                        </TableCell>
                        <TableCell className="whitespace-nowrap font-numeric text-[12px] text-muted">
                          {payout.reviewed_at ? formatDateTime(payout.reviewed_at) : '—'}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>

              <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
                <span className="text-[11.5px] text-muted">
                  {page.pagination.summary(pageNumber, pages, total)}
                </span>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={pageNumber <= 1}
                    onClick={() => setPageNumber((current) => Math.max(1, current - 1))}
                  >
                    {page.pagination.prev}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={pageNumber >= pages}
                    onClick={() => setPageNumber((current) => Math.min(pages, current + 1))}
                  >
                    {page.pagination.next}
                  </Button>
                </div>
              </div>
            </>
          )}
        </div>
      </section>

      {dialogOpen ? (
        <PayoutDialog
          open
          availableMicro={availableMicro}
          onClose={() => setDialogOpen(false)}
        />
      ) : null}
    </>
  )
}
