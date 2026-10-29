import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { Button, Empty, Input, PageHeader, Tag, buttonClasses, useToast } from '@/ui'

import {
  meQuery,
  postOnboarding,
  type MeResponse,
  type OnboardingInput,
  type SellerIdentity,
} from '@/features/seller/api'
import { Field } from '@/features/seller/components'
import { notify } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { isSellerActive } from '@/features/seller/status'

export const Route = createFileRoute('/console/onboarding')({
  component: SellerOnboardingPage,
})

const page = copy.pages.sellerOnboarding
const namePattern = /^[\p{L} ·.\-•]{2,64}$/u

function StepRail({ seller }: { seller: SellerIdentity }) {
  const progress = seller.progress
  const agreementDone = progress.agreement_signed
  const payoutDone = progress.payout_account_bound
  // 协议 → 绑定收款 → 完成. 收款 stay optional for sharing but required before withdrawal.
  const steps = [
    { title: page.stepTitles[0], hint: page.stepHints[0], done: agreementDone, optional: false },
    { title: page.stepTitles[1], hint: page.stepHints[1], done: payoutDone, optional: true },
    {
      title: page.stepTitles[2],
      hint: page.stepHints[2],
      done: agreementDone && payoutDone,
      optional: false,
    },
  ]
  const done = steps.filter((step) => step.done).length
  const current = steps.findIndex((step) => !step.done)

  return (
    <section aria-label={page.progress} className="mt-6 glass p-6">
      <div className="flex items-baseline justify-between">
        <h2 className="text-title font-semibold tracking-tight text-ink">{page.progress}</h2>
        <span className="tabular-nums text-meta text-ink-3">{done}/3</span>
      </div>
      <ol className="mt-5 grid gap-3 sm:grid-cols-3">
        {steps.map((step, index) => {
          const completed = step.done
          const active = index === current
          return (
            <li
              key={step.title}
              aria-current={active ? 'step' : undefined}
              className={cn(
                'relative rounded-md border p-4',
                active ? 'border-brand bg-brand-soft/50' : 'border-white/70 bg-white/45 backdrop-blur-md',
              )}
            >
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    'grid h-6 w-6 shrink-0 place-items-center rounded-full border text-meta font-medium',
                    completed
                      ? 'border-transparent bg-brand-soft text-brand-ink'
                      : active
                        ? 'border-transparent bg-brand text-white'
                        : 'glass-inset text-ink-3',
                  )}
                  aria-hidden="true"
                >
                  {completed ? (
                    <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
                      <path
                        d="M2.5 6.2l2.2 2.2 4.8-5"
                        stroke="currentColor"
                        strokeWidth="1.6"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      />
                    </svg>
                  ) : (
                    index + 1
                  )}
                </span>
                <span
                  className={cn(
                    'text-body font-medium',
                    completed || active ? 'text-ink' : 'text-ink-3',
                  )}
                >
                  {step.title}
                </span>
                {step.optional ? <Tag>{page.state.optional}</Tag> : null}
              </div>
              <p className="mt-2 text-meta leading-5 text-ink-3">{step.hint}</p>
              <p className="mt-1 text-meta text-ink-3">
                {completed ? page.state.done : active ? page.state.current : page.state.todo}
              </p>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

function PayoutAccountCard({
  seller,
  onSubmit,
  pending,
  errorText,
}: {
  seller: SellerIdentity
  onSubmit: (input: OnboardingInput) => void
  pending: boolean
  errorText: string | null
}) {
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

  if (isBound && !editing) {
    const isWx = seller.progress.payout_channel === 'wechat'
    return (
      <section className="mt-6 glass p-6">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <h2 className="text-title font-semibold tracking-tight text-ink">
              {page.payout.title}
            </h2>
            <Tag tone="brand">{page.state.done}</Tag>
          </div>
          <Button variant="secondary" size="sm" onClick={() => setEditing(true)}>
            {page.payout.changeAccount}
          </Button>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-4 text-meta text-ink-2">
          <Tag tone={isWx ? 'brand' : 'neutral'}>
            {isWx ? page.payout.channelWechat : page.payout.channelAlipay}
          </Tag>
          {seller.progress.payout_name ? (
            <span>收款人：{seller.progress.payout_name}</span>
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
      onSubmit({
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
      onSubmit({
        payout_channel: 'alipay',
        payout_name: trimmedName,
        payout_account: trimmedAccount,
      })
    }
  }

  return (
    <section className="mt-6 glass p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-title font-semibold tracking-tight text-ink">{page.payout.title}</h2>
        <div className="flex items-center gap-2">
          {isBound ? (
            <Button variant="ghost" size="sm" onClick={() => setEditing(false)}>
              取消修改
            </Button>
          ) : (
            <Tag>{page.state.optional}</Tag>
          )}
        </div>
      </div>
      <p className="mt-2 text-meta leading-6 text-ink-3">{page.payout.lead}</p>

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
          <span>{page.payout.channelWechat}</span>
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
          <span>{page.payout.channelAlipay}</span>
        </button>
      </div>

      <form className="mt-5 space-y-5" onSubmit={handleSubmit}>
        <Field
          label={page.payout.nameLabel}
          htmlFor="payout-name"
          hint={page.payout.nameHint}
        >
          <Input
            id="payout-name"
            autoComplete="name"
            placeholder={page.payout.namePlaceholder}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>

        {channel === 'wechat' ? (
          <Field
            label={page.payout.wechatQRLabel}
            hint={page.payout.wechatQRHint}
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
                    {page.payout.reuploadPrompt}
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
                  {page.payout.uploadPrompt}
                </span>
                <span className="mt-1 text-meta text-ink-3">支持 PNG、JPG、WEBP 格式，最大 5MB</span>
              </div>
            )}
          </Field>
        ) : (
          <Field
            label={page.payout.alipayAccountLabel}
            htmlFor="alipay-account"
            hint={page.payout.alipayAccountHint}
          >
            <Input
              id="alipay-account"
              autoComplete="tel"
              placeholder={page.payout.alipayAccountPlaceholder}
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

        {errorText ? (
          <p className="text-meta text-danger" role="alert">
            {errorText}
          </p>
        ) : null}

        <div className="flex justify-end border-t border-line pt-4">
          <Button type="submit" loading={pending}>
            {pending ? page.payout.submitting : page.payout.submit}
          </Button>
        </div>
      </form>
    </section>
  )
}

export function SellerOnboardingPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const { data } = useQuery(meQuery())
  const seller = data?.seller
  const [accepted, setAccepted] = useState(false)

  const mutation = useMutation({
    mutationFn: (input: OnboardingInput) => postOnboarding(input),
    onSuccess: (response) => {
      queryClient.setQueryData<MeResponse>(meQuery().queryKey, (old) =>
        old ? { ...old, seller: response.seller } : old,
      )
      void queryClient.invalidateQueries({ queryKey: meQuery().queryKey })
      notify(toast, { title: copy.pages.sellerCommon.toast.saved, tone: 'success' })
    },
  })

  if (!seller) return null

  const blocked = seller.exists && (seller.status === 'banned' || seller.status === 'closed')
  const active = isSellerActive(seller)
  const errorText = mutation.isError ? sellerErrorMessage(mutation.error) : null

  function submit(input: OnboardingInput) {
    mutation.reset()
    mutation.mutate(input)
  }

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          active ? (
            <Tag tone="brand" dot>
              {page.activeBadge}
            </Tag>
          ) : (
            <Tag tone="neutral" dot>
              {page.pendingBadge}
            </Tag>
          )
        }
      />

      {blocked ? (
        <Empty
          className="mt-6"
          title={page.blocked.title}
          reason={page.blocked.reason}
          next={page.blocked.next}
        />
      ) : (
        <>
          <StepRail seller={seller} />

          {active ? (
            <>
              <section className="mt-6 glass p-6">
                <h2 className="text-title font-semibold tracking-tight text-ink">
                  {page.done.title}
                </h2>
                <p className="mt-2 text-body leading-6 text-ink-2">{page.done.reason}</p>
                <div className="mt-4 flex flex-wrap gap-2">
                  <Link to="/console/listings/new" className={buttonClasses('primary', 'md')}>
                    {page.done.newListing}
                  </Link>
                  <Link to="/console/earnings" className={buttonClasses('secondary', 'md')}>
                    {page.done.overview}
                  </Link>
                </div>
              </section>

              <p className="mt-4 text-meta text-ink-3">{page.optionalHint}</p>

              <PayoutAccountCard
                seller={seller}
                onSubmit={submit}
                pending={mutation.isPending}
                errorText={errorText}
              />
            </>
          ) : (
            <section className="mt-6 glass p-6">
              <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-4">
                <h2 className="text-title font-semibold tracking-tight text-ink">
                  {page.agreement.title}
                </h2>
                <Tag>{page.agreement.version}</Tag>
              </header>

              <div className="space-y-5 pt-5">
                <p className="text-meta text-ink-3">{page.agreement.hint}</p>

                <ol className="divide-y divide-line/70 border-y border-line">
                  {page.agreement.clauses.map((clause) => (
                    <li key={clause.title} className="py-3.5">
                      <h3 className="text-meta font-medium text-ink">{clause.title}</h3>
                      <p className="mt-1 text-meta leading-6 text-ink-3">{clause.body}</p>
                    </li>
                  ))}
                </ol>

                <div className="flex flex-wrap items-center justify-between gap-3">
                  <label className="flex cursor-pointer items-start gap-2 text-meta text-ink">
                    <input
                      type="checkbox"
                      checked={accepted}
                      onChange={(event) => setAccepted(event.target.checked)}
                      className="mt-0.5 h-4 w-4 accent-brand"
                    />
                    <span>{page.agreement.checkbox}</span>
                  </label>
                  <span className="flex items-center gap-4">
                    <Link
                      to="/agreement"
                      className="text-meta text-brand underline-offset-4 hover:underline"
                    >
                      {copy.footer.terms}
                    </Link>
                    <Link
                      to="/privacy"
                      className="text-meta text-brand underline-offset-4 hover:underline"
                    >
                      {copy.footer.privacy}
                    </Link>
                  </span>
                </div>

                {errorText ? (
                  <p className="text-meta text-danger" role="alert">
                    {errorText}
                  </p>
                ) : null}

                <div className="flex justify-end border-t border-line pt-4">
                  <Button
                    disabled={!accepted}
                    loading={mutation.isPending}
                    onClick={() => submit({ agreement_version: 'v1' })}
                  >
                    {mutation.isPending ? page.agreement.submitting : page.agreement.submit}
                  </Button>
                </div>
              </div>
            </section>
          )}
        </>
      )}
    </>
  )
}
