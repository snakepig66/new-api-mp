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
  meQuery,
  requestPayout,
  sellerBalanceQuery,
  sellerPayoutsQuery,
  type Payout,
  type SellerIdentity,
} from '@/features/seller/api'
import { AsyncError, Field, PageSkeleton, TableSkeleton } from '@/features/seller/components'
import { notify } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { microToYuanInput, parseYuanToMicro } from '@/features/seller/money-input'
import { PayoutAccountCard } from '@/features/seller/PayoutAccountCard'
import { payoutStatusView } from '@/features/seller/status'

export const Route = createFileRoute('/console/earnings/payouts')({
  component: SellerPayoutsPage,
})

const page = copy.pages.sellerPayouts
const commonErrors = copy.pages.sellerCommon.errors
const MIN_PAYOUT_MICRO = 10_000_000n
const FREQ_MS = 3 * 24 * 60 * 60 * 1000
const PAGE_SIZE = 10

function toBigInt(value: number | string): bigint {
  try {
    return BigInt(String(value))
  } catch {
    return 0n
  }
}

/** Mirrors the backend: a non-rejected payout inside the rolling 3-day window. */
function recentActivePayout(payouts: Payout[], now = Date.now()): Payout | undefined {
  return payouts.find((payout) => {
    if (payout.status === 'rejected') return false
    const date = parseApiTime(payout.created_at)
    if (!date) return false
    return now - date.getTime() < FREQ_MS
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
  seller,
  onClose,
}: {
  open: boolean
  availableMicro: number | string
  seller?: SellerIdentity
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

  const isWx = seller?.progress.payout_channel === 'wechat'
  const payoutName = seller?.progress.payout_name

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
        {seller?.progress.payout_account_bound ? (
          <div className="rounded-md border border-line bg-white/50 p-3 text-meta text-ink-2">
            <span className="font-medium text-ink">打款目标：</span>
            <span>
              {isWx ? '微信支付' : '支付宝'}
              {payoutName ? `（收款人：${payoutName}）` : ''}
              {!isWx && seller.progress.payout_account_masked ? ` - ${seller.progress.payout_account_masked}` : ''}
            </span>
          </div>
        ) : null}

        <Field
          label={page.dialog.amountLabel}
          htmlFor="payout-amount"
          hint={page.dialog.hint(formatYuan(availableMicro))}
          error={error ?? undefined}
        >
          <Input
            id="payout-amount"
            inputMode="decimal"
            placeholder={page.dialog.amountPlaceholder}
            value={amount}
            onChange={(event) => {
              setAmount(event.target.value)
              setError(null)
            }}
          />
        </Field>
        <p className="text-meta text-ink-3">
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
  const me = useQuery(meQuery())
  const seller = me.data?.seller
  const needsPayoutAccount = seller ? !seller.progress.payout_account_bound : false

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
        title={copy.pages.consoleEarnings.tabs.payouts}
        lead={page.lead}
        actions={
          <Button
            disabled={!canRequest || needsPayoutAccount}
            onClick={() => {
              setDialogOpen(true)
            }}
          >
            {page.request}
          </Button>
        }
      />

      {needsPayoutAccount ? (
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3 glass px-5 py-4 border-l-4 border-l-brand">
          <p className="text-meta font-medium text-ink">
            {page.accountCard.notBoundNotice}
          </p>
        </div>
      ) : null}

      <section className="mt-6 grid gap-6 glass p-6 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
        <div>
          <div className="text-meta text-ink-3">{page.available}</div>
          <div className="mt-1 break-words text-h1 font-semibold leading-tight tracking-tight tabular-nums text-brand-ink">
            {formatYuan(availableMicro)}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-meta text-ink-3">
            <span>{page.min}</span>
            <span className="h-3 w-px bg-line-strong" aria-hidden="true" />
            {recent ? (
              <Tag tone="neutral" dot>
                {page.weekly}
              </Tag>
            ) : (
              <Tag tone="brand" dot>
                {page.weeklyFree}
              </Tag>
            )}
          </div>
          <p className="mt-2 text-meta leading-5 text-ink-3">{page.weeklyHint}</p>
        </div>
        <div className="flex flex-col justify-end gap-3 border-t border-line pt-4 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0">
          <div>
            <div className="text-meta text-ink-3">{page.pending}</div>
            <div className="mt-1 text-h2 font-semibold tabular-nums text-ink">
              {formatYuan(data?.pending_micro ?? 0)}
            </div>
          </div>
          <p className="text-meta leading-5 text-ink-3">{page.reserveHint}</p>
        </div>
      </section>

      {/* 提现最后一步：设置收款账户 */}
      {seller ? <PayoutAccountCard seller={seller} /> : null}

      <section className="mt-6 glass p-6">
        <h2 className="text-title font-semibold tracking-tight text-ink">
          {page.history.title}
        </h2>
        <p className="mt-1 text-meta text-ink-3">{page.history.lead}</p>

        {payouts.isPending ? (
          <div className="mt-4">
            <TableSkeleton rows={4} columns={4} />
          </div>
        ) : payouts.isError ? (
          <div className="mt-4">
            <AsyncError
              title={page.historyError}
              reason={sellerErrorMessage(payouts.error)}
              onRetry={() => void payouts.refetch()}
              retryLabel={copy.actions.retry}
            />
          </div>
        ) : historyRows.length === 0 ? (
          <Empty
            className="mt-6"
            title={page.history.emptyTitle}
            reason={page.history.emptyReason}
            next={page.history.emptyNext}
          />
        ) : (
          <>
            <div className="mt-4">
              <Table minWidth={640}>
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
                        <TableCell className="whitespace-nowrap tabular-nums text-meta text-ink-3">
                          {formatDateTime(payout.created_at)}
                        </TableCell>
                        <TableCell numeric className="font-medium">
                          {formatYuan(payout.amount_micro)}
                        </TableCell>
                        <TableCell>
                          <Tag tone={status.tone} dot>
                            {status.label}
                          </Tag>
                        </TableCell>
                        <TableCell className="whitespace-nowrap tabular-nums text-meta text-ink-3">
                          {payout.reviewed_at ? formatDateTime(payout.reviewed_at) : '—'}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>

            {pages > 1 ? (
              <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-4">
                <span className="text-meta text-ink-3">
                  {page.pagination.summary(pageNumber, pages, total)}
                </span>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={pageNumber <= 1}
                    onClick={() => setPageNumber((p) => Math.max(1, p - 1))}
                  >
                    {page.pagination.prev}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={pageNumber >= pages}
                    onClick={() => setPageNumber((p) => Math.min(pages, p + 1))}
                  >
                    {page.pagination.next}
                  </Button>
                </div>
              </div>
            ) : null}
          </>
        )}
      </section>

      <PayoutDialog
        open={dialogOpen}
        availableMicro={availableMicro}
        seller={seller}
        onClose={() => setDialogOpen(false)}
      />
    </>
  )
}
