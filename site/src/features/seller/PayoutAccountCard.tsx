import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { Button, Input, Tag, useToast } from '@/ui'

import {
  meQuery,
  postOnboarding,
  type MeResponse,
  type OnboardingInput,
  type SellerIdentity,
} from '@/features/seller/api'
import { Field, notify } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'

const page = copy.pages.sellerPayouts
const namePattern = /^[\p{L} ·.\-•]{2,64}$/u

/**
 * 收款账户表单。提现页和开通向导共用同一份实现，避免两套表单产生差异。
 */
export function PayoutAccountCard({
  seller,
  className = 'mt-6',
}: {
  seller: SellerIdentity
  className?: string
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const isBound = seller.progress.payout_account_bound
  const [editing, setEditing] = useState(!isBound)
  const [channel, setChannel] = useState<'wechat' | 'alipay'>(
    seller.progress.payout_channel === 'alipay' ? 'alipay' : 'wechat',
  )
  const [name, setName] = useState(seller.progress.payout_name || '')
  const [alipayAccount, setAlipayAccount] = useState('')
  const [wechatAccount, setWechatAccount] = useState('')
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
      const trimmedAccount = wechatAccount.trim()
      if (!qrCode && !seller.progress.payout_has_qr_code && !trimmedAccount) {
        setValidationError('请上传微信收款码或输入微信手机号')
        return
      }
      if (trimmedAccount && !/^1\d{10}$/.test(trimmedAccount)) {
        setValidationError('请输入 11 位中国大陆手机号')
        return
      }
      mutation.mutate({
        payout_channel: 'wechat',
        payout_name: trimmedName,
        payout_account: trimmedAccount || undefined,
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
      <section className={cn('glass p-6', className)}>
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
            <div className="flex items-center gap-3">
              {seller.progress.payout_account_masked ? (
                <span>账号：{seller.progress.payout_account_masked}</span>
              ) : null}
              {seller.progress.payout_has_qr_code ? (
                <span className="text-brand-ink">✓ 微信收款码已保存</span>
              ) : null}
            </div>
          ) : seller.progress.payout_account_masked ? (
            <span>账号：{seller.progress.payout_account_masked}</span>
          ) : null}
        </div>
      </section>
    )
  }

  return (
    <section className={cn('glass p-6', className)}>
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
          <>
            <Field
              label={page.accountCard.wechatAccountLabel ?? '微信手机号'}
              htmlFor="wechat-account"
              hint={page.accountCard.wechatAccountHint ?? '打款转账到该微信绑定的手机号（选填）'}
            >
              <Input
                id="wechat-account"
                type="tel"
                autoComplete="tel"
                placeholder={page.accountCard.wechatAccountPlaceholder ?? '11 位中国大陆手机号'}
                value={wechatAccount}
                onChange={(e) => setWechatAccount(e.target.value)}
              />
            </Field>

            <Field label={page.accountCard.wechatQRLabel} hint={page.accountCard.wechatQRHint}>
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
          </>
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
