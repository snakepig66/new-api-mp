import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { Button, Empty, PageHeader, Tag, buttonClasses, useToast } from '@/ui'

import {
  meQuery,
  postOnboarding,
  type MeResponse,
  type OnboardingInput,
  type SellerIdentity,
} from '@/features/seller/api'
import { notify } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { isSellerActive } from '@/features/seller/status'

export const Route = createFileRoute('/console/onboarding')({
  component: SellerOnboardingPage,
})

const page = copy.pages.sellerOnboarding

function StepRail({ seller }: { seller: SellerIdentity }) {
  const done = seller.progress.agreement_signed ? 2 : 0
  const steps = [
    {
      title: page.stepTitles[0],
      hint: page.stepHints[0],
      done: seller.progress.agreement_signed,
    },
    {
      title: page.stepTitles[1],
      hint: page.stepHints[1],
      done: seller.progress.agreement_signed,
    },
  ]

  return (
    <section aria-label={page.progress} className="mt-6 glass p-6">
      <div className="flex items-baseline justify-between">
        <h2 className="text-title font-semibold tracking-tight text-ink">{page.progress}</h2>
        <span className="tabular-nums text-meta text-ink-3">{done}/2</span>
      </div>
      <ol className="mt-5 grid gap-3 sm:grid-cols-2">
        {steps.map((step, index) => {
          const completed = step.done
          const active = !completed && index === 0
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
              {page.state.done}
            </Tag>
          ) : (
            <Tag tone="neutral" dot>
              {page.state.todo}
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
