import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

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

const realNameSchema = z.object({
  real_name: z
    .string()
    .trim()
    .min(2, copy.pages.sellerCommon.errors.realName)
    .refine((value) => namePattern.test(value), copy.pages.sellerCommon.errors.realName),
  id_card_masked: z
    .string()
    .trim()
    .refine((value) => {
      if (value.length < 8 || value.length > 32) return false
      if (!/^[0-9Xx*]+$/.test(value)) return false
      const digits = value.replace(/[^0-9]/g, '').length
      const stars = value.replace(/[^*]/g, '').length
      return digits >= 4 && stars >= 4
    }, copy.pages.sellerCommon.errors.idCard),
})

type RealNameForm = z.infer<typeof realNameSchema>

const alipaySchema = z.object({
  alipay_account: z
    .string()
    .trim()
    .refine((value) => {
      if (!value || /[ \t\r\n]/.test(value)) return false
      const at = value.indexOf('@')
      if (at > 0) {
        const domain = value.slice(at + 1)
        return (
          at === value.lastIndexOf('@') &&
          domain.length > 0 &&
          domain.includes('.') &&
          !domain.startsWith('.') &&
          !domain.endsWith('.')
        )
      }
      return /^1\d{10}$/.test(value)
    }, copy.pages.sellerCommon.errors.alipay),
})

type AlipayForm = z.infer<typeof alipaySchema>

function StepRail({ seller }: { seller: SellerIdentity }) {
  const progress = seller.progress
  const agreementDone = progress.agreement_signed
  const realNameDone = progress.real_name_verified
  const payoutDone = progress.payout_account_bound
  // 协议 → 资料 → 收款 → 完成. 资料/收款 stay optional for sharing but are
  // required before withdrawal, so the rail always shows them.
  const steps = [
    { title: page.stepTitles[0], hint: page.stepHints[0], done: agreementDone, optional: false },
    { title: page.stepTitles[1], hint: page.stepHints[1], done: realNameDone, optional: true },
    { title: page.stepTitles[2], hint: page.stepHints[2], done: payoutDone, optional: true },
    {
      title: page.stepTitles[3],
      hint: page.stepHints[3],
      done: agreementDone && realNameDone && payoutDone,
      optional: false,
    },
  ]
  const done = steps.filter((step) => step.done).length
  const current = steps.findIndex((step) => !step.done)

  return (
    <section aria-label={page.progress} className="mt-6 rounded-md border border-line bg-bg p-6">
      <div className="flex items-baseline justify-between">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">{page.progress}</h2>
        <span className="tabular-nums text-[13px] text-ink-3">{done}/4</span>
      </div>
      <ol className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {steps.map((step, index) => {
          const completed = step.done
          const active = index === current
          return (
            <li
              key={step.title}
              aria-current={active ? 'step' : undefined}
              className={cn(
                'relative rounded-md border p-4',
                active ? 'border-brand bg-brand-soft/50' : 'border-line bg-bg',
              )}
            >
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    'grid h-6 w-6 shrink-0 place-items-center rounded-full border text-[13px] font-medium',
                    completed
                      ? 'border-transparent bg-brand-soft text-brand-ink'
                      : active
                        ? 'border-transparent bg-brand text-white'
                        : 'border-line bg-bg-soft text-ink-3',
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
                    'text-[14px] font-medium',
                    completed || active ? 'text-ink' : 'text-ink-3',
                  )}
                >
                  {step.title}
                </span>
                {step.optional ? <Tag>{page.state.optional}</Tag> : null}
              </div>
              <p className="mt-2 text-[13px] leading-5 text-ink-3">{step.hint}</p>
              <p className="mt-1 text-[13px] text-ink-3">
                {completed ? page.state.done : active ? page.state.current : page.state.todo}
              </p>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

interface OptionalCardProps {
  onSubmit: (input: OnboardingInput) => void
  pending: boolean
  errorText: string | null
}

function RealNameCard({ onSubmit, pending, errorText }: OptionalCardProps) {
  const form = useForm<RealNameForm>({
    resolver: zodResolver(realNameSchema),
    defaultValues: { real_name: '', id_card_masked: '' },
    mode: 'onTouched',
  })

  return (
    <section className="mt-6 rounded-md border border-line bg-bg p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">
          {page.realName.title}
        </h2>
        <Tag>{page.state.optional}</Tag>
      </div>
      <form
        className="mt-5 space-y-5"
        onSubmit={form.handleSubmit((values) =>
          onSubmit({
            real_name: values.real_name,
            id_card_masked: values.id_card_masked,
          }),
        )}
      >
        <p className="text-[13px] leading-6 text-ink-3">{page.realName.lead}</p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label={page.realName.nameLabel}
            htmlFor="real-name"
            hint={page.realName.nameHint}
            error={form.formState.errors.real_name?.message}
          >
            <Input
              id="real-name"
              autoComplete="name"
              placeholder={page.realName.namePlaceholder}
              invalid={Boolean(form.formState.errors.real_name)}
              {...form.register('real_name')}
            />
          </Field>
          <Field
            label={page.realName.idLabel}
            htmlFor="id-card"
            hint={page.realName.idHint}
            error={form.formState.errors.id_card_masked?.message}
          >
            <Input
              id="id-card"
              autoComplete="off"
              spellCheck={false}
              placeholder={page.realName.idPlaceholder}
              invalid={Boolean(form.formState.errors.id_card_masked)}
              {...form.register('id_card_masked')}
            />
          </Field>
        </div>

        {errorText ? (
          <p className="text-[13px] text-danger" role="alert">
            {errorText}
          </p>
        ) : null}

        <div className="flex justify-end border-t border-line pt-4">
          <Button type="submit" loading={pending}>
            {pending ? page.realName.submitting : page.realName.submit}
          </Button>
        </div>
      </form>
    </section>
  )
}

function AlipayCard({ onSubmit, pending, errorText }: OptionalCardProps) {
  const form = useForm<AlipayForm>({
    resolver: zodResolver(alipaySchema),
    defaultValues: { alipay_account: '' },
    mode: 'onTouched',
  })

  return (
    <section className="mt-6 rounded-md border border-line bg-bg p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">{page.alipay.title}</h2>
        <Tag>{page.state.optional}</Tag>
      </div>
      <form
        className="mt-5 space-y-5"
        onSubmit={form.handleSubmit((values) =>
          onSubmit({ alipay_account: values.alipay_account }),
        )}
      >
        <p className="text-[13px] leading-6 text-ink-3">{page.alipay.lead}</p>
        <Field
          label={page.alipay.accountLabel}
          htmlFor="alipay-account"
          hint={page.alipay.hint}
          error={form.formState.errors.alipay_account?.message}
        >
          <Input
            id="alipay-account"
            autoComplete="off"
            placeholder={page.alipay.accountPlaceholder}
            invalid={Boolean(form.formState.errors.alipay_account)}
            {...form.register('alipay_account')}
          />
        </Field>

        {errorText ? (
          <p className="text-[13px] text-danger" role="alert">
            {errorText}
          </p>
        ) : null}

        <div className="flex justify-end border-t border-line pt-4">
          <Button type="submit" loading={pending}>
            {pending ? page.alipay.submitting : page.alipay.submit}
          </Button>
        </div>
      </form>
    </section>
  )
}

function OptionalDone({ label }: { label: string }) {
  return (
    <div className="mt-3 flex items-center gap-2 text-[13px] text-ink-3">
      <span
        className="grid h-4 w-4 place-items-center rounded-full bg-brand-soft text-brand-ink"
        aria-hidden="true"
      >
        <svg width="9" height="9" viewBox="0 0 12 12" fill="none">
          <path
            d="M2.5 6.2l2.2 2.2 4.8-5"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </span>
      {label}
    </div>
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
              <section className="mt-6 rounded-md border border-line bg-bg p-6">
                <h2 className="text-[16px] font-semibold tracking-tight text-ink">
                  {page.done.title}
                </h2>
                <p className="mt-2 text-[14px] leading-6 text-ink-2">{page.done.reason}</p>
                <div className="mt-4 flex flex-wrap gap-2">
                  <Link to="/console/listings/new" className={buttonClasses('primary', 'md')}>
                    {page.done.newListing}
                  </Link>
                  <Link to="/console/earnings" className={buttonClasses('secondary', 'md')}>
                    {page.done.overview}
                  </Link>
                </div>
              </section>

              <p className="mt-4 text-[13px] text-ink-3">{page.optionalHint}</p>

              {seller.progress.real_name_verified ? (
                <OptionalDone label={page.realName.done} />
              ) : (
                <RealNameCard
                  onSubmit={submit}
                  pending={mutation.isPending}
                  errorText={errorText}
                />
              )}

              {seller.progress.payout_account_bound ? (
                <OptionalDone label={page.alipay.done} />
              ) : (
                <AlipayCard onSubmit={submit} pending={mutation.isPending} errorText={errorText} />
              )}
            </>
          ) : (
            <section className="mt-6 rounded-md border border-line bg-bg p-6">
              <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-4">
                <h2 className="text-[16px] font-semibold tracking-tight text-ink">
                  {page.agreement.title}
                </h2>
                <Tag>{page.agreement.version}</Tag>
              </header>

              <div className="space-y-5 pt-5">
                <p className="text-[13px] text-ink-3">{page.agreement.hint}</p>

                <ol className="divide-y divide-line/70 border-y border-line">
                  {page.agreement.clauses.map((clause) => (
                    <li key={clause.title} className="py-3.5">
                      <h3 className="text-[13px] font-medium text-ink">{clause.title}</h3>
                      <p className="mt-1 text-[13px] leading-6 text-ink-3">{clause.body}</p>
                    </li>
                  ))}
                </ol>

                <div className="flex flex-wrap items-center justify-between gap-3">
                  <label className="flex cursor-pointer items-start gap-2 text-[13px] text-ink">
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
                      className="text-[13px] text-brand underline-offset-4 hover:underline"
                    >
                      {copy.footer.terms}
                    </Link>
                    <Link
                      to="/privacy"
                      className="text-[13px] text-brand underline-offset-4 hover:underline"
                    >
                      {copy.footer.privacy}
                    </Link>
                  </span>
                </div>

                {errorText ? (
                  <p className="text-[13px] text-danger" role="alert">
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
