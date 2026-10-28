import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate, useSearch } from '@tanstack/react-router'
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
  fetchWalletTransactionsCsv,
  quoteTopupAmount,
  requestEpayTopup,
  topupInfoQuery,
  walletFlowQuery,
  walletQuery,
  walletTransactionsExportUrl,
} from '@/features/console/api'
import type { WalletFlowKind, WalletFlowRow } from '@/features/console/api'
import {
  ErrorBlock,
  Section,
  Skeleton,
  StatSkeleton,
} from '@/features/console/components/DataState'
import { Notice } from '@/features/console/components/form'
import { redeemCodeSchema } from '@/features/console/schemas'
import { walletKindLabel } from '@/features/console/wallet-labels'
import type { TagTone } from '@/ui'

const PAGE_SIZE = 20

/** Post-redirect confirmation poll: every 3s, at most 40 tries (~2 minutes). */
export const TOPUP_CONFIRM_POLL_INTERVAL_MS = 3_000
export const TOPUP_CONFIRM_POLL_MAX_ATTEMPTS = 40

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

/** Unified wallet stream copy (owned by consoleWallet.transactions). */
const tx = page.transactions

type WalletTab = WalletFlowKind

const WALLET_TABS: WalletTab[] = [
  'all',
  'usage',
  'topup',
  'earning',
  'withdrawal',
  'refund',
]

/** Type-column label; earning/withdrawal are not in the legacy label helper. */
function flowKindLabel(kind: WalletFlowRow['kind']): string {
  if (kind === 'earning') return tx.tabs.earning
  if (kind === 'withdrawal') return tx.tabs.withdrawal
  return walletKindLabel(kind)
}

/** Status label + tone for every flow kind, including seller payouts. */
function flowStatusMeta(kind: WalletFlowRow['kind'], status: string): {
  label: string
  tone: TagTone
} {
  if (kind === 'earning') {
    const labels = tx.statuses.earning as Record<string, string>
    return {
      label: labels[status] ?? labels.unknown,
      tone: ledgerTone(status),
    }
  }
  if (kind === 'withdrawal') {
    const labels = tx.statuses.withdrawal as Record<string, string>
    const tone: TagTone =
      status === 'paid'
        ? 'brand'
        : status === 'rejected'
          ? 'danger'
          : status === 'pending' || status === 'approved'
            ? 'neutral'
            : 'neutral'
    return { label: labels[status] ?? labels.unknown, tone }
  }
  if (kind === 'topup') {
    const labels = copy.pages.consoleWallet.topups.statuses as Record<string, string>
    const tone: TagTone =
      status === 'success' ? 'brand' : status === 'pending' ? 'neutral' : 'danger'
    return { label: labels[status] ?? labels.unknown, tone }
  }
  return { label: ledgerLabel(status), tone: ledgerTone(status) }
}

function ledgerLabel(status: string): string {
  const labels = page.ledger.status as Record<string, string>
  return labels[status] ?? labels.unknown
}

function ledgerTone(status: string): TagTone {
  if (status === 'available') return 'brand'
  if (status === 'pending') return 'neutral'
  if (status === 'reversed') return 'danger'
  return 'neutral'
}

/** Description column for every flow kind. */
function flowDescription(entry: WalletFlowRow): string {
  switch (entry.kind) {
    case 'topup':
      return entry.money !== undefined && entry.money !== null
        ? `${tx.descriptions.topup} · 实付 ¥${Number(entry.money).toFixed(2)}`
        : tx.descriptions.topup
    case 'refund':
      return tx.descriptions.refund
    case 'usage':
      return tx.descriptions.usage
    case 'earning':
      return tx.descriptions.earning
    case 'withdrawal':
      return tx.descriptions.withdrawal
    default:
      return tx.descriptions.unknown
  }
}

/** Read the validated page/topup search values. */
function useWalletSearch(): { page: number; topup?: string } {
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const parsed = Number(search.page)
  return {
    page: Number.isInteger(parsed) && parsed > 0 ? parsed : 1,
    topup:
      typeof search.topup === 'string' && search.topup ? search.topup : undefined,
  }
}

export function WalletPage() {
  const { page: currentPage } = useWalletSearch()
  const navigate = useNavigate()
  const toast = useToast()
  const [kind, setKind] = useState<WalletTab>('all')
  const [exporting, setExporting] = useState(false)
  const wallet = useQuery(walletQuery())
  const transactions = useQuery(walletFlowQuery(currentPage, kind))

  const total = transactions.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  function goTo(next: number) {
    void navigate({
      to: '/console/wallet',
      search: { page: Math.min(Math.max(1, next), totalPages) },
    })
  }

  async function exportCsv() {
    setExporting(true)
    try {
      const blob = await fetchWalletTransactionsCsv(kind)
      saveCsvBlob(blob, `wallet-${kind}.csv`)
    } catch {
      toast.show({ title: tx.exportFailed, tone: 'error' })
    } finally {
      setExporting(false)
    }
  }

  function selectKind(next: WalletTab) {
    setKind(next)
    // A filtered stream has a different length; start over at page 1.
    if (currentPage !== 1) {
      void navigate({ to: '/console/wallet', search: { page: 1 } })
    }
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <TopupReturnWatcher />

      <Notice tone="warning" title={page.warningTitle} className="mt-5">
        {page.warning}
      </Notice>

      {wallet.isPending ? (
        <div className="mt-6 grid grid-cols-2 gap-4 glass p-6 sm:grid-cols-3">
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
        <div className="mt-6 grid gap-6 glass p-6 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)] sm:divide-x sm:divide-line">
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

      <Section className="mt-9" title={tx.title} hint={tx.hint}>
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div
            role="tablist"
            aria-label={tx.title}
            className="inline-flex flex-wrap glass-inset p-1"
          >
            {WALLET_TABS.map((tab) => (
              <button
                key={tab}
                type="button"
                role="tab"
                aria-selected={kind === tab}
                onClick={() => selectKind(tab)}
                className={
                  kind === tab
                    ? 'rounded px-3 py-1.5 text-[13px] font-medium text-brand-ink'
                    : 'rounded px-3 py-1.5 text-[13px] text-ink-2 transition-colors hover:text-ink'
                }
              >
                {tx.tabs[tab]}
              </button>
            ))}
          </div>
          <a
            href={walletTransactionsExportUrl(kind)}
            download={`wallet-${kind}.csv`}
            className={buttonClasses('secondary', 'sm')}
            title={tx.exportHint}
            aria-busy={exporting}
            onClick={(event) => {
              // This console authenticates with the Authorization header, so
              // the download must go through fetch instead of navigation.
              event.preventDefault()
              if (!exporting) void exportCsv()
            }}
          >
            {exporting ? tx.exporting : tx.export}
          </a>
        </div>

        {transactions.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-5 w-full" />
            <Skeleton className="h-5 w-full" />
            <Skeleton className="h-5 w-4/5" />
          </div>
        ) : transactions.isError ? (
          <ErrorBlock
            error={transactions.error}
            onRetry={() => void transactions.refetch()}
          />
        ) : transactions.data.items.length === 0 ? (
          <Empty
            title={tx.emptyTitle}
            reason={kind === 'topup' ? tx.emptyTopupReason : tx.emptyReason}
          />
        ) : (
          <>
            <Table minWidth={860}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{tx.columns.time}</TableHeaderCell>
                  <TableHeaderCell>{tx.columns.type}</TableHeaderCell>
                  <TableHeaderCell>{tx.columns.description}</TableHeaderCell>
                  <TableHeaderCell numeric>{tx.columns.amount}</TableHeaderCell>
                  <TableHeaderCell>{tx.columns.status}</TableHeaderCell>
                  <TableHeaderCell>{tx.columns.reference}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {transactions.data.items.map((entry) => {
                  const status = flowStatusMeta(entry.kind, entry.status)
                  const description = flowDescription(entry)
                  return (
                    <TableRow key={entry.id}>
                      <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                        {formatDateTime(entry.created_at)}
                      </TableCell>
                      <TableCell>{flowKindLabel(entry.kind)}</TableCell>
                      <TableCell
                        className="max-w-[240px] truncate text-[13px] text-ink-3"
                        title={description}
                      >
                        {description}
                      </TableCell>
                      <TableCell
                        numeric
                        className={
                          entry.kind === 'refund' ||
                          entry.kind === 'topup' ||
                          entry.kind === 'earning'
                            ? 'text-brand-ink'
                            : undefined
                        }
                      >
                        {formatYuan(entry.amount_micro)}
                      </TableCell>
                      <TableCell>
                        <Tag tone={status.tone}>{status.label}</Tag>
                      </TableCell>
                      <TableCell
                        className="max-w-[220px] truncate font-mono text-[12px] text-ink-2"
                        title={entry.reference}
                      >
                        {entry.reference || copy.pages.consoleShared.none}
                      </TableCell>
                    </TableRow>
                  )
                })}
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

/** Trigger a client-side download of the exported CSV blob. */
function saveCsvBlob(blob: Blob, filename: string) {
  if (typeof URL.createObjectURL !== 'function') return
  const objectUrl = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectUrl
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(objectUrl)
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
    <section className="glass p-6">
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
                          : 'rounded-full border border-white/70 bg-white/70 px-3.5 py-1.5 text-[13px] text-ink-2 transition-colors hover:border-line-strong hover:text-ink'
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
              className="mt-3 glass-inset px-3.5 py-2.5"
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
                        : 'rounded-full border border-white/70 bg-white/70 px-3.5 py-1.5 text-[13px] text-ink-2 transition-colors hover:border-line-strong hover:text-ink'
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
  const { topup } = useWalletSearch()
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
    let attempts = 0

    const tick = async () => {
      attempts += 1
      try {
        const record = await fetchTopUpRecord(tradeNo)
        if (cancelled) return
        if (record?.status === 'success') {
          setState('success')
          sessionStorage.removeItem('mp:last-topup')
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: ['console', 'wallet'] }),
            queryClient.invalidateQueries({ queryKey: ['console', 'wallet-transactions'] }),
          ])
          return
        }
      } catch {
        // Keep polling: a transient failure is not a payment verdict.
      }
      if (cancelled) return
      if (attempts >= TOPUP_CONFIRM_POLL_MAX_ATTEMPTS) {
        setState('timeout')
        return
      }
      timer = window.setTimeout(
        () => void tick(),
        TOPUP_CONFIRM_POLL_INTERVAL_MS,
      )
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
      <Notice tone="brand" title={page.topupReturn.confirmingTitle} className="mt-5">
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
      await queryClient.invalidateQueries({ queryKey: ['console', 'wallet-transactions'] })
    } catch (redeemError) {
      setError(
        redeemError instanceof ApiError ? redeemError.message : copy.pages.consoleShared.errorHint,
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="glass p-6">
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
