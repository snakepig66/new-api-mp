import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
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
  postOnboarding,
  requestPayout,
  sellerBalanceQuery,
  sellerPayoutsQuery,
  type MeResponse,
  type OnboardingInput,
  type Payout,
  type SellerIdentity,
} from '@/features/seller/api'
import { AsyncError, Field, PageSkeleton, TableSkeleton } from '@/features/seller/components'
import { notify } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { microToYuanInput, parseYuanToMicro } from '@/features/seller/money-input'
import { payoutStatusView } from '@/features/seller/status'

export const Route = createFileRoute('/console/earnings/payouts')({
  component: SellerPayoutsPage,
})

const page = copy.pages.sellerPayouts
const commonErrors = copy.pages.sellerCommon.errors
const MIN_PAYOUT_MICRO = 10_000_000n
const FREQ_MS = 3 * 24 * 60 * 60 * 1000
const PAGE_SIZE = 10
const namePattern = /^[\p{L} ·.\-•]{2,64}$/u

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

function PayoutAccountCard({ seller }: { seller: SellerIdentity }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const isBound = seller.progress.payout_account_bound
  const [editing, setEditing] = useState(!isBound)
  const [channel, setChannel] = useState<'wechat' | 'alipay'>(
    seller.progress.payout_channel === 'alipay' ? 'alipay' : 'wechat',
  )
  const [name, setName] = useState(seller.progress.payout_name || '')
  const [alipayAccount, setAlipayAccount] = useState('')
  const [qrCode, setQrCode] = useState('')
  const [validationError, setValidationError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const mutation = useMutation({
    mutationFn: (input: OnboardingInput) => postOnboarding(input),
    onSuccess: (response) => {
      queryClient.setQueryData<MeResponse>(meQuery().queryKey, (old) =>
        old ? { ...old, seller: response.seller } : old,
      )
      void queryClient.invalidateQueries({ queryKey: meQuery().queryKey })
      setEditing(false)
      notify(toast, { title: page.accountCard.savedToast, tone: 'success' })
    },
  })

  const isWx = seller.progress.payout_channel === 'wechat'

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    if (!['image/png', 'image/jpeg', 'image/jpg', 'image/webp'].includes(file.type)) {
      setValidationError('只支持 PNG / JPG / WEBP 格式图片')
      return
    }
    if (file.size > 5 * 1024 * 1024) {
      setValidationError('收款码图片大小不能超过 5MB')
      return
    }
    setValidationError(null)
    const reader = new FileReader()
    reader.onload = () => {
      setQrCode(reader.result as string)
    }
    reader.readAsDataURL(file)
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setValidationError(null)
    const trimmedName = name.trim()
    if (!trimmedName || !namePattern.test(trimmedName)) {
      setValidationError(copy.pages.sellerCommon.errors.realName)
      return
    }
    if (channel === 'wechat') {
      if (!qrCode && !seller.progress.payout_has_qr_code) {
        setValidationError('请上传微信收款码')
        return
      }
      mutation.mutate({
        payout_channel: 'wechat',
        payout_name: trimmedName,
        payout_qr_code: qrCode || undefined,
      })
    } else {
      const trimmedAccount = alipayAccount.trim()
      if (!/^1\d{10}$/.test(trimmedAccount)) {
        setValidationError('请输入 11 位中国大陆手机号')
        return
      }
      mutation.mutate({
        payout_channel: 'alipay',
        payout_name: trimmedName,
        payout_account: trimmedAccount,
      })
    }
  }

  if (isBound && !editing) {
    return (
      <section className="mt-6 glass p-6">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
          <div className="flex items-center gap-2">
            <h2 className="text-title font-semibold tracking-tight text-ink">
              {page.accountCard.title}
            </h2>
            <Tag tone="brand" dot>
              {page.accountCard.boundTag}
            </Tag>
          </div>
          <Button variant="secondary" size="sm" onClick={() => setEditing(true)}>
            {page.accountCard.changeAccount}
          </Button>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-4 text-meta text-ink-2">
          <Tag tone={isWx ? 'brand' : 'neutral'}>
            {isWx ? page.accountCard.channelWechat : page.accountCard.channelAlipay}
          </Tag>
          {seller.progress.payout_name ? (
            <span className="font-medium text-ink">收款人：{seller.progress.payout_name}</span>
          ) : null}
          {isWx ? (
            <span className="text-brand-ink">✓ 微信收款码已保存</span>
          ) : seller.progress.payout_account_masked ? (
            <span>账号：{seller.progress.payout_account_masked}</span>
          ) : null}
        </div>
      </section>
    )
  }

  return (
    <section className="mt-6 glass p-6">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
        <div>
          <div className="flex items-center gap-2">
            <h2 className="text-title font-semibold tracking-tight text-ink">
              {page.accountCard.title}
            </h2>
            <Tag tone={isBound ? 'brand' : 'neutral'} dot>
              {isBound ? page.accountCard.boundTag : page.accountCard.notBoundTag}
            </Tag>
          </div>
          <p className="mt-1 text-meta text-ink-3">{page.accountCard.lead}</p>
        </div>
        {isBound ? (
          <Button variant="ghost" size="sm" onClick={() => setEditing(false)}>
            {page.accountCard.cancel}
          </Button>
        ) : null}
      </div>

      {/* 渠道切换选项卡 */}
      <div className="mt-5 flex gap-2 border-b border-line pb-3">
        <button
          type="button"
          onClick={() => {
            setChannel('wechat')
            setValidationError(null)
          }}
          className={cn(
            'flex items-center gap-2 rounded-md px-3.5 py-1.5 text-meta font-medium transition-colors',
            channel === 'wechat'
              ? 'bg-brand text-white shadow-sm'
              : 'text-ink-2 hover:bg-white/50 hover:text-ink',
          )}
        >
          <span>{page.accountCard.channelWechat}</span>
        </button>
        <button
          type="button"
          onClick={() => {
            setChannel('alipay')
            setValidationError(null)
          }}
          className={cn(
            'flex items-center gap-2 rounded-md px-3.5 py-1.5 text-meta font-medium transition-colors',
            channel === 'alipay'
              ? 'bg-brand text-white shadow-sm'
              : 'text-ink-2 hover:bg-white/50 hover:text-ink',
          )}
        >
          <span>{page.accountCard.channelAlipay}</span>
        </button>
      </div>

      <form className="mt-5 space-y-5" onSubmit={handleSubmit}>
        <Field
          label={page.accountCard.nameLabel}
          htmlFor="payout-name"
          hint={page.accountCard.nameHint}
        >
          <Input
            id="payout-name"
            autoComplete="name"
            placeholder={page.accountCard.namePlaceholder}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>

        {channel === 'wechat' ? (
          <Field
            label={page.accountCard.wechatQRLabel}
            hint={page.accountCard.wechatQRHint}
          >
            <input
              ref={fileInputRef}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              className="hidden"
              onChange={handleFileChange}
            />
            {qrCode ? (
              <div className="flex items-center gap-4">
                <div className="relative h-32 w-32 overflow-hidden rounded-lg border border-line bg-white/70 p-1">
                  <img
                    src={qrCode}
                    alt="微信收款码预览"
                    className="h-full w-full object-contain"
                  />
                </div>
                <div className="space-y-2">
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    onClick={() => fileInputRef.current?.click()}
                  >
                    {page.accountCard.reuploadPrompt}
                  </Button>
                  <p className="text-meta text-ink-3">已选定新收款码图片</p>
                </div>
              </div>
            ) : (
              <div
                onClick={() => fileInputRef.current?.click()}
                className="flex cursor-pointer flex-col items-center justify-center rounded-lg border-2 border-dashed border-line p-6 transition-colors hover:border-brand/60 hover:bg-brand-soft/20"
              >
                <svg
                  className="h-8 w-8 text-ink-3"
                  stroke="currentColor"
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth="1.5"
                    d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
                  />
                </svg>
                <span className="mt-2 text-meta font-medium text-ink-2">
                  {page.accountCard.uploadPrompt}
                </span>
                <span className="mt-1 text-meta text-ink-3">支持 PNG、JPG、WEBP 格式，最大 5MB</span>
              </div>
            )}
          </Field>
        ) : (
          <Field
            label={page.accountCard.alipayAccountLabel}
            htmlFor="alipay-account"
            hint={page.accountCard.alipayAccountHint}
          >
            <Input
              id="alipay-account"
              autoComplete="tel"
              placeholder={page.accountCard.alipayAccountPlaceholder}
              value={alipayAccount}
              onChange={(e) => setAlipayAccount(e.target.value)}
            />
          </Field>
        )}

        {validationError ? (
          <p className="text-meta text-danger" role="alert">
            {validationError}
          </p>
        ) : null}

        {mutation.isError ? (
          <p className="text-meta text-danger" role="alert">
            {sellerErrorMessage(mutation.error)}
          </p>
        ) : null}

        <div className="flex justify-end border-t border-line pt-4">
          <Button type="submit" loading={mutation.isPending}>
            {mutation.isPending ? page.accountCard.submitting : page.accountCard.submit}
          </Button>
        </div>
      </form>
    </section>
  )
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
