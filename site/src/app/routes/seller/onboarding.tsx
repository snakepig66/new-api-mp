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
import {
  completedStepCount,
  sellerStepIndex,
} from '@/features/seller/status'

export const Route = createFileRoute('/seller/onboarding')({
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
  const current = sellerStepIndex(seller.progress)
  const done = completedStepCount(seller.progress)

  return (
    <section aria-label={page.progress} className="mt-6">
      <div className="flex items-baseline justify-between">
        <h2 className="text-[13px] font-medium text-ink">{page.progress}</h2>
        <span className="font-numeric text-[11.5px] text-muted">
          {done}/3
        </span>
      </div>
      <ol className="mt-3 grid gap-px border border-line bg-line sm:grid-cols-3">
        {page.stepTitles.map((title, index) => {
          const completed = index < current
          const active = index === current && current < 3
          return (
            <li
              key={title}
              aria-current={active ? 'step' : undefined}
              className={cn(
                'relative bg-surface p-4',
                active && 'shadow-[inset_0_2px_0_var(--color-brand-dark)]',
              )}
            >
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    'grid h-5 w-5 shrink-0 place-items-center rounded-full border text-[10px] font-medium',
                    completed
                      ? 'border-success/30 bg-success-soft text-success'
                      : active
                        ? 'border-brand-dark bg-brand-dark text-white'
                        : 'border-line-strong bg-wash text-muted',
                  )}
                  aria-hidden="true"
                >
                  {completed ? (
                    <svg width="10" height="10" viewBox="0 0 12 12" fill="none">
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
                    'text-[12.5px] font-medium',
                    completed || active ? 'text-ink' : 'text-muted',
                  )}
                >
                  {title}
                </span>
              </div>
              <p className="mt-2 text-[11.5px] leading-5 text-muted">
                {page.stepHints[index]}
              </p>
              <p className="mt-1 text-[10.5px] text-muted/80">
                {completed ? page.state.done : active ? page.state.current : page.state.todo}
              </p>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

function SellerOnboardingPage() {
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

  const realNameForm = useForm<RealNameForm>({
    resolver: zodResolver(realNameSchema),
    defaultValues: { real_name: '', id_card_masked: '' },
    mode: 'onTouched',
  })
  const alipayForm = useForm<AlipayForm>({
    resolver: zodResolver(alipaySchema),
    defaultValues: { alipay_account: '' },
    mode: 'onTouched',
  })

  if (!seller) return null

  const blocked = seller.exists && (seller.status === 'banned' || seller.status === 'closed')
  const current = sellerStepIndex(seller.progress)
  const finished = seller.progress.next_step === 'done'

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
          finished ? (
            <Tag tone="success" dot>
              {page.activeBadge}
            </Tag>
          ) : (
            <Tag tone="warning" dot>
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

          {finished ? (
            <Empty
              className="mt-6"
              title={page.done.title}
              reason={page.done.reason}
              next={page.done.next}
              action={
                <div className="flex flex-wrap gap-2">
                  <Link to="/seller" className={buttonClasses('primary', 'md')}>
                    {page.done.overview}
                  </Link>
                  <Link
                    to="/seller/listings/new"
                    className={buttonClasses('secondary', 'md')}
                  >
                    {page.done.newListing}
                  </Link>
                </div>
              }
            />
          ) : (
            <section className="mt-6 border border-line bg-surface">
              <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-5 py-4">
                <h2 className="text-[15px] font-semibold tracking-tight text-ink">
                  {page.stepTitles[current]}
                </h2>
                <span className="font-numeric text-[11.5px] text-muted">
                  0{current + 1} / 03
                </span>
              </header>

              <div className="px-5 py-5">
                {current === 0 ? (
                  <div className="space-y-5">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <p className="text-[13px] text-muted">
                        {page.agreement.hint}
                      </p>
                      <Tag>{page.agreement.version}</Tag>
                    </div>

                    <ol className="divide-y divide-line/70 border-y border-line">
                      {page.agreement.clauses.map((clause) => (
                        <li key={clause.title} className="py-3.5">
                          <h3 className="text-[13px] font-medium text-ink">
                            {clause.title}
                          </h3>
                          <p className="mt-1 text-[12.5px] leading-6 text-muted">
                            {clause.body}
                          </p>
                        </li>
                      ))}
                    </ol>

                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <label className="flex cursor-pointer items-start gap-2 text-[12.5px] text-ink">
                        <input
                          type="checkbox"
                          checked={accepted}
                          onChange={(event) => setAccepted(event.target.checked)}
                          className="mt-0.5 h-3.5 w-3.5 accent-[var(--color-brand-dark)]"
                        />
                        <span>{page.agreement.checkbox}</span>
                      </label>
                      <Link
                        to="/docs"
                        hash="terms"
                        className="text-[12px] text-brand-dark underline-offset-4 hover:underline"
                      >
                        {copy.footer.terms}
                      </Link>
                    </div>

                    {errorText ? (
                      <p className="text-[12px] text-danger" role="alert">
                        {errorText}
                      </p>
                    ) : null}

                    <div className="flex justify-end border-t border-line pt-4">
                      <Button
                        disabled={!accepted}
                        loading={mutation.isPending}
                        onClick={() => submit({ agreement_version: 'v1' })}
                      >
                        {mutation.isPending
                          ? page.agreement.submitting
                          : page.agreement.submit}
                      </Button>
                    </div>
                  </div>
                ) : null}

                {current === 1 ? (
                  <form
                    className="space-y-5"
                    onSubmit={realNameForm.handleSubmit((values) =>
                      submit({
                        real_name: values.real_name,
                        id_card_masked: values.id_card_masked,
                      }),
                    )}
                  >
                    <p className="text-[13px] leading-6 text-muted">{page.realName.lead}</p>
                    <div className="grid gap-4 sm:grid-cols-2">
                      <Field
                        label={page.realName.nameLabel}
                        htmlFor="real-name"
                        hint={page.realName.nameHint}
                        error={realNameForm.formState.errors.real_name?.message}
                      >
                        <Input
                          id="real-name"
                          autoComplete="name"
                          placeholder={page.realName.namePlaceholder}
                          invalid={Boolean(realNameForm.formState.errors.real_name)}
                          {...realNameForm.register('real_name')}
                        />
                      </Field>
                      <Field
                        label={page.realName.idLabel}
                        htmlFor="id-card"
                        hint={page.realName.idHint}
                        error={realNameForm.formState.errors.id_card_masked?.message}
                      >
                        <Input
                          id="id-card"
                          mono
                          autoComplete="off"
                          spellCheck={false}
                          placeholder={page.realName.idPlaceholder}
                          invalid={Boolean(realNameForm.formState.errors.id_card_masked)}
                          {...realNameForm.register('id_card_masked')}
                        />
                      </Field>
                    </div>

                    {errorText ? (
                      <p className="text-[12px] text-danger" role="alert">
                        {errorText}
                      </p>
                    ) : null}

                    <div className="flex justify-end border-t border-line pt-4">
                      <Button type="submit" loading={mutation.isPending}>
                        {mutation.isPending
                          ? page.realName.submitting
                          : page.realName.submit}
                      </Button>
                    </div>
                  </form>
                ) : null}

                {current === 2 ? (
                  <form
                    className="space-y-5"
                    onSubmit={alipayForm.handleSubmit((values) =>
                      submit({ alipay_account: values.alipay_account }),
                    )}
                  >
                    <p className="text-[13px] leading-6 text-muted">{page.alipay.lead}</p>
                    <Field
                      label={page.alipay.accountLabel}
                      htmlFor="alipay-account"
                      hint={page.alipay.hint}
                      error={alipayForm.formState.errors.alipay_account?.message}
                    >
                      <Input
                        id="alipay-account"
                        mono
                        autoComplete="off"
                        placeholder={page.alipay.accountPlaceholder}
                        invalid={Boolean(alipayForm.formState.errors.alipay_account)}
                        {...alipayForm.register('alipay_account')}
                      />
                    </Field>

                    {errorText ? (
                      <p className="text-[12px] text-danger" role="alert">
                        {errorText}
                      </p>
                    ) : null}

                    <div className="flex justify-end border-t border-line pt-4">
                      <Button type="submit" loading={mutation.isPending}>
                        {mutation.isPending
                          ? page.alipay.submitting
                          : page.alipay.submit}
                      </Button>
                    </div>
                  </form>
                ) : null}
              </div>
            </section>
          )}
        </>
      )}
    </>
  )
}
