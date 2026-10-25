import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

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
  buttonClasses,
  useToast,
} from '@/ui'

import {
  redeemCode,
  fetchTopUpRecord,
  quoteTopupAmount,
  requestEpayTopup,
  topUpRecordsQuery,
  topupInfoQuery,
  walletLedgerQuery,
  walletQuery,
} from '@/features/console/api'
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
  validateSearch: (search: Record<string, unknown>): { page: number; topup?: string } => {
    const parsed = Number(search.page)
    return {
      page: Number.isInteger(parsed) && parsed > 0 ? parsed : 1,
      topup: typeof search.topup === 'string' && search.topup ? search.topup : undefined,
    }
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

      <TopupReturnWatcher />

      <Notice tone="warning" title={page.warningTitle} className="mt-5">
        {page.warning}
      </Notice>

      {wallet.isPending ? (
        <div className="mt-6 grid grid-cols-2 gap-4 rounded-md border border-line bg-bg p-6 sm:grid-cols-3">
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
        <div className="mt-6 grid gap-6 rounded-md border border-line bg-bg p-6 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)] sm:divide-x sm:divide-line">
          <div className="min-w-0 sm:pr-6">
            <div className="text-[13px] text-ink-3">{page.balance}</div>
            <div className="mt-1 break-all text-[32px] font-semibold leading-tight tracking-tight tabular-nums text-brand-ink">
              {formatYuan(wallet.data.balance_micro)}
            </div>
            <div className="mt-1.5 text-[13px] leading-5 text-ink-3">{page.balanceHint}</div>
          </div>
          <Stat
            label={page.monthSpend}
            value={formatYuan(wallet.data.month_spend_micro)}
            className="sm:px-6"
          />
          <Stat
            label={page.monthSaved}
            value={formatYuan(wallet.data.month_saved_micro)}
            tone="brand"
            className="sm:pl-6"
          />
        </div>
      )}

      <div className="mt-6 grid gap-4 lg:grid-cols-2">
        <RechargePanel />
        <RedeemPanel />
      </div>

      <TopUpRecords />

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
                    <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                      {formatDateTime(entry.created_at)}
                    </TableCell>
                    <TableCell>{ledgerEntryLabel(entry.entry_type)}</TableCell>
                    <TableCell className="max-w-[220px] truncate tabular-nums text-[13px] text-ink-3">
                      {entry.request_id || copy.pages.consoleShared.none}
                    </TableCell>
                    <TableCell
                      numeric
                      className={entry.entry_type === 'refund' ? 'text-brand-ink' : undefined}
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
              <span className="tabular-nums text-[13px] text-ink-3">
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

function RechargePanel() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const info = useQuery(topupInfoQuery())
  const [amount, setAmount] = useState('')
  const [method, setMethod] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const methods = info.data?.pay_methods ?? []
  const amountOptions = info.data?.amount_options ?? []
  const amountValue = Number(amount)
  const amountValid = Number.isInteger(amountValue) && amountValue > 0
  const selectedMethod = methods.find((item) => item.type === method)
  const methodMin = Number(selectedMethod?.min_topup ?? 0)
  const minAmount = Math.max(
    info.data?.min_topup || 0,
    Number.isFinite(methodMin) ? methodMin : 0,
  )
  // The backend owns group ratios and discounts; never display a locally
  // computed "actual pay" next to the gateway amount.
  const quote = useQuery({
    queryKey: ['console', 'topup-quote', amountValue, method],
    queryFn: () => quoteTopupAmount(amountValue),
    enabled: Boolean(info.data?.enable_online_topup) && amountValid && amountValue >= minAmount,
    retry: false,
    staleTime: 0,
  })
  const payable = quote.data && quote.data > 0 ? quote.data : null

  useEffect(() => {
    const data = info.data
    if (!data) return
    setMethod((current) => {
      if (current) return current
      const preferred = data.pay_methods?.find((item) => item.type === 'alipay')
      return preferred?.type ?? data.pay_methods?.[0]?.type ?? ''
    })
    setAmount((current) => {
      if (current) return current
      const first = data.amount_options?.[0]
      return first ? String(first) : ''
    })
  }, [info.data])

  async function submit() {
    setError(null)
    const data = info.data
    if (!data || !method) return
    const value = Number(amount)
    if (!amount.trim()) {
      setError(page.recharge.errors.amountRequired)
      return
    }
    if (!Number.isInteger(value) || value <= 0) {
      setError(page.recharge.errors.amountInvalid)
      return
    }
    if (value < minAmount) {
      setError(page.recharge.errors.tooLow(String(minAmount)))
      return
    }
    if (payable === null) {
      setError(page.recharge.quoteFailed)
      return
    }
    setSubmitting(true)
    try {
      const payment = await requestEpayTopup(value, method)
      if (!payment?.url || !payment.data) {
        throw new ApiError(200, undefined, page.recharge.errors.failed)
      }
      if (payment.trade_no) {
        // The gateway redirect carries no order number; remember it so the
        // wallet can poll for the asynchronous callback after returning.
        sessionStorage.setItem(
          'mp:last-topup',
          JSON.stringify({ trade_no: payment.trade_no, at: Date.now() }),
        )
      }
      toast.show({ title: page.recharge.redirecting, tone: 'success' })
      submitPaymentForm(payment.url, payment.data)
      await queryClient.invalidateQueries({ queryKey: ['console', 'topup-info'] })
    } catch (payError) {
      setError(
        payError instanceof ApiError ? payError.message : page.recharge.errors.failed,
      )
      setSubmitting(false)
    }
  }

  const amountNumber = Number(amount)
  const discount = Number.isInteger(amountNumber)
    ? info.data?.discount?.[String(amountNumber)]
    : undefined

  return (
    <section className="rounded-md border border-line bg-bg p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">
          {page.recharge.title}
        </h2>
        {info.data?.enable_online_topup && methods.length > 0 ? (
          <Tag tone="brand">
            {methods.find((item) => item.type === method)?.name ?? methods[0]?.name}
          </Tag>
        ) : (
          <Tag tone="neutral">{page.recharge.unavailable}</Tag>
        )}
      </div>

      {info.isPending ? (
        <div className="mt-4 space-y-3">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-32" />
        </div>
      ) : info.isError ? (
        <ErrorBlock className="mt-4" error={info.error} onRetry={() => void info.refetch()} />
      ) : !info.data.enable_online_topup || methods.length === 0 ? (
        <>
          <p className="mt-2 text-[13px] leading-6 text-ink-3">
            {page.recharge.unavailableBody}
          </p>
          {info.data.topup_link ? (
            <a
              href={info.data.topup_link}
              target="_blank"
              rel="noreferrer"
              className={buttonClasses('secondary', 'md', 'mt-4')}
            >
              {page.recharge.topupLink}
            </a>
          ) : null}
        </>
      ) : (
        <>
          <p className="mt-2 text-[13px] leading-6 text-ink-3">{page.recharge.body}</p>

          <div className="mt-4">
            <label
              htmlFor="recharge-amount"
              className="mb-2 block text-[14px] font-medium text-ink"
            >
              {page.recharge.amount}
            </label>
            {amountOptions.length > 0 ? (
              <div className="mb-2 flex flex-wrap gap-2">
                {amountOptions.map((option) => {
                  const active = amount === String(option)
                  return (
                    <button
                      key={option}
                      type="button"
                      aria-pressed={active}
                      onClick={() => setAmount(String(option))}
                      className={
                        active
                          ? 'rounded-full border border-brand bg-brand-soft px-3.5 py-1.5 text-[13px] font-medium text-brand-ink'
                          : 'rounded-full border border-line bg-bg px-3.5 py-1.5 text-[13px] text-ink-2 transition-colors hover:border-line-strong hover:text-ink'
                      }
                    >
                      {option} 元
                    </button>
                  )
                })}
              </div>
            ) : null}
            <div className="flex items-center gap-2">
              <Input
                id="recharge-amount"
                inputMode="numeric"
                placeholder={page.recharge.amountPlaceholder}
                value={amount}
                invalid={Boolean(error)}
                onChange={(event) => setAmount(event.target.value)}
              />
              <span className="shrink-0 text-[13px] text-ink-3">元</span>
            </div>
            <p className="mt-1.5 text-[13px] leading-5 text-ink-3">
              {page.recharge.min(String(info.data.min_topup))}
              {discount ? ` · ${page.recharge.discount(formatDiscount(discount))}` : ''}
            </p>
            <div
              data-testid="topup-payable"
              className="mt-3 rounded-md border border-line bg-bg-subtle px-3.5 py-2.5"
            >
              <div className="text-[15px] font-semibold tabular-nums text-ink">
                {amountValid && amountValue >= minAmount
                  ? payable !== null
                    ? page.recharge.actualPay(payable.toFixed(2))
                    : quote.isError
                      ? page.recharge.quoteFailed
                      : '…'
                  : page.recharge.actualPayHint}
              </div>
              {amountValid && amountValue >= minAmount && payable !== null ? (
                <div className="mt-1 text-[12px] leading-5 text-ink-3">
                  {page.recharge.actualPayHint}
                </div>
              ) : null}
            </div>
          </div>

          {methods.length > 1 ? (
            <div className="mt-4">
              <div className="mb-2 text-[14px] font-medium text-ink">
                {page.recharge.method}
              </div>
              <div className="flex flex-wrap gap-2">
                {methods.map((item) => (
                  <button
                    key={item.type}
                    type="button"
                    aria-pressed={method === item.type}
                    onClick={() => setMethod(item.type)}
                    className={
                      method === item.type
                        ? 'rounded-full border border-brand bg-brand-soft px-3.5 py-1.5 text-[13px] font-medium text-brand-ink'
                        : 'rounded-full border border-line bg-bg px-3.5 py-1.5 text-[13px] text-ink-2 transition-colors hover:border-line-strong hover:text-ink'
                    }
                  >
                    {item.name}
                  </button>
                ))}
              </div>
            </div>
          ) : null}

          {error ? (
            <p role="alert" className="mt-3 text-[13px] leading-5 text-danger">
              {error}
            </p>
          ) : null}

          <Button
            className="mt-4"
            loading={submitting}
            disabled={!amountValid || amountValue < minAmount || payable === null}
            onClick={() => void submit()}
          >
            {submitting ? page.recharge.paying : page.recharge.action}
          </Button>
          <p className="mt-3 text-[13px] leading-5 text-ink-3">{page.recharge.hint}</p>
        </>
      )}
    </section>
  )
}

type TopupReturnState = 'idle' | 'confirming' | 'success' | 'timeout'

/**
 * After the gateway redirects back, the payment callback may not have arrived
 * yet. Poll the order for 30 seconds instead of telling the buyer the money
 * vanished; on timeout hand them the order number for support.
 */
function TopupReturnWatcher() {
  const { topup } = Route.useSearch()
  const queryClient = useQueryClient()
  const [state, setState] = useState<TopupReturnState>('idle')
  const [orderNo, setOrderNo] = useState('')

  useEffect(() => {
    if (topup !== 'returning') return
    let tradeNo = ''
    try {
      const raw = sessionStorage.getItem('mp:last-topup')
      if (raw) {
        const parsed = JSON.parse(raw) as { trade_no?: unknown }
        if (typeof parsed.trade_no === 'string') tradeNo = parsed.trade_no
      }
    } catch {
      // A corrupt entry is equivalent to a missing one.
    }
    if (!tradeNo) return

    setOrderNo(tradeNo)
    setState('confirming')
    let cancelled = false
    let timer = 0
    const started = Date.now()

    const tick = async () => {
      try {
        const record = await fetchTopUpRecord(tradeNo)
        if (cancelled) return
        if (record?.status === 'success') {
          setState('success')
          sessionStorage.removeItem('mp:last-topup')
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: ['console', 'wallet'] }),
            queryClient.invalidateQueries({ queryKey: ['console', 'wallet-ledger'] }),
            queryClient.invalidateQueries({ queryKey: ['console', 'topup-records'] }),
          ])
          return
        }
      } catch {
        // Keep polling: a transient failure is not a payment verdict.
      }
      if (cancelled) return
      if (Date.now() - started >= 30_000) {
        setState('timeout')
        return
      }
      timer = window.setTimeout(() => void tick(), 3000)
    }
    void tick()

    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [topup, queryClient])

  if (state === 'idle') return null
  if (state === 'confirming') {
    return (
      <Notice tone="brand" className="mt-5">
        {page.topupReturn.confirming}
      </Notice>
    )
  }
  if (state === 'success') {
    return (
      <Notice tone="brand" className="mt-5">
        {page.topupReturn.success}
      </Notice>
    )
  }
  return (
    <Notice tone="warning" title={page.topupReturn.timeoutTitle} className="mt-5">
      <p>
        {page.topupReturn.timeout}
        <span className="ml-1 font-medium tabular-nums">{orderNo}</span>
      </p>
      <Button
        variant="secondary"
        size="sm"
        className="mt-2"
        onClick={() => setState('idle')}
      >
        {page.topupReturn.dismiss}
      </Button>
    </Notice>
  )
}

function TopUpRecords() {
  const records = useQuery(topUpRecordsQuery())

  return (
    <Section className="mt-9" title={page.topups.title} hint={page.topups.hint}>
      {records.isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-5 w-full" />
          <Skeleton className="h-5 w-full" />
        </div>
      ) : records.isError ? (
        <ErrorBlock error={records.error} onRetry={() => void records.refetch()} />
      ) : records.data.items.length === 0 ? (
        <Empty
          title={page.topups.emptyTitle}
          reason={page.topups.emptyReason}
        />
      ) : (
        <Table minWidth={720}>
          <TableHead>
            <TableRow interactive={false}>
              <TableHeaderCell>{page.topups.time}</TableHeaderCell>
              <TableHeaderCell>{page.topups.order}</TableHeaderCell>
              <TableHeaderCell numeric>{page.topups.amount}</TableHeaderCell>
              <TableHeaderCell numeric>{page.topups.money}</TableHeaderCell>
              <TableHeaderCell>{page.topups.status}</TableHeaderCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {records.data.items.map((record) => (
              <TableRow key={record.id} interactive={false}>
                <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                  {formatDateTime(record.create_time)}
                </TableCell>
                <TableCell className="max-w-[220px] truncate font-mono text-[12px] text-ink-2">
                  {record.trade_no}
                </TableCell>
                <TableCell numeric>{record.amount} 元</TableCell>
                <TableCell numeric>¥{Number(record.money).toFixed(2)}</TableCell>
                <TableCell>
                  <TopUpStatus status={record.status} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Section>
  )
}

function TopUpStatus({ status }: { status: string }) {
  const label =
    page.topups.statuses[status as keyof typeof page.topups.statuses] ??
    page.topups.statuses.unknown
  if (status === 'success') return <Tag tone="brand">{label}</Tag>
  if (status === 'pending') return <Tag tone="neutral">{label}</Tag>
  return <Tag tone="danger">{label}</Tag>
}

function formatDiscount(discount: number): string {
  if (!Number.isFinite(discount) || discount <= 0) return '—'
  return (discount * 10).toFixed(1).replace(/\.0$/, '')
}

/** Navigate the whole tab to the gateway with its hidden form fields. */
function submitPaymentForm(url: string, params: Record<string, string>) {
  const form = document.createElement('form')
  form.action = url
  form.method = 'POST'
  form.style.display = 'none'
  for (const [key, value] of Object.entries(params)) {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = key
    input.value = String(value)
    form.appendChild(input)
  }
  document.body.appendChild(form)
  form.submit()
  form.remove()
}

function LedgerStatus({ status }: { status: string }) {
  const label = ledgerStatusLabel(status)
  if (status === 'available') return <Tag tone="brand">{label}</Tag>
  if (status === 'pending') return <Tag tone="neutral">{label}</Tag>
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
    <section className="rounded-md border border-line bg-bg p-6">
      <h2 className="text-[16px] font-semibold tracking-tight text-ink">{page.redeem.title}</h2>
      <p className="mt-2 text-[13px] leading-6 text-ink-3">{page.redeem.hint}</p>
      <form
        className="mt-4 flex flex-col gap-2 sm:flex-row sm:items-start"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
        noValidate
      >
        <div className="min-w-0 flex-1">
          <label htmlFor="redeem-code" className="mb-2 block text-[14px] font-medium text-ink">
            {page.redeem.label}
          </label>
          <Input
            id="redeem-code"
            autoComplete="off"
            placeholder={page.redeem.placeholder}
            value={code}
            invalid={Boolean(error)}
            onChange={(event) => setCode(event.target.value)}
          />
        </div>
        <Button type="submit" className="sm:mt-[30px]" loading={submitting}>
          {submitting ? page.redeem.redeeming : page.redeem.action}
        </Button>
      </form>
      {error ? (
        <p role="alert" className="mt-2 text-[13px] text-danger">
          {error}
        </p>
      ) : null}
    </section>
  )
}
