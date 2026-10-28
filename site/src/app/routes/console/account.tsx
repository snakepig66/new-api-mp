import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import {
  changeAccountPassword,
  fetchSelfProfile,
  requestPasswordChangeProof,
  type SelfProfile,
} from '@/features/auth/account'
import { ErrorBlock, Section, Skeleton } from '@/features/console/components/DataState'
import { Notice } from '@/features/console/components/form'
import { ApiError } from '@/lib/api'
import { useSession } from '@/lib/session'
import { Button, PageHeader, PasswordInput, useToast } from '@/ui'

export const Route = createFileRoute('/console/account')({
  component: AccountPage,
})

const page = copy.pages.consoleAccount
const passwordCopy = page.password

const schema = z
  .object({
    current: z.string().min(1, passwordCopy.required),
    next: z.string().min(8, passwordCopy.tooShort),
    confirm: z.string().min(1, copy.validation.required),
  })
  .refine((values) => values.next === values.confirm, {
    path: ['confirm'],
    message: passwordCopy.mismatch,
  })
  .refine((values) => values.next !== values.current, {
    path: ['next'],
    message: passwordCopy.sameAsCurrent,
  })

type PasswordForm = z.infer<typeof schema>

function AccountPage() {
  const profile = useQuery({
    queryKey: ['account', 'self'],
    queryFn: fetchSelfProfile,
    staleTime: 60_000,
  })

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <Section className="mt-7" title={page.identity.title}>
        {profile.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-5 w-64" />
          </div>
        ) : profile.isError ? (
          <ErrorBlock error={profile.error} onRetry={() => void profile.refetch()} />
        ) : (
          <IdentityRows profile={profile.data} />
        )}
      </Section>

      <section className="mt-6 glass p-6">
        <h2 className="text-title font-semibold tracking-tight text-ink">
          {passwordCopy.title}
        </h2>
        <p className="mt-1.5 text-meta leading-6 text-ink-3">{passwordCopy.hint}</p>

        {profile.isPending ? (
          <div className="mt-5 space-y-3">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : profile.isError ? null : profile.data.has_password === false ? (
          <Notice className="mt-4" title={passwordCopy.noPassword}>
            {passwordCopy.noPasswordHint}
          </Notice>
        ) : (
          <PasswordForm />
        )}
      </section>
    </>
  )
}

function IdentityRows({ profile }: { profile: SelfProfile }) {
  const session = useSession()
  const username = profile.username || session?.user?.username || '—'
  const displayName = profile.display_name || session?.user?.display_name || '—'

  return (
    <dl className="grid gap-4 text-body sm:grid-cols-2">
      <div>
        <dt className="text-meta text-ink-3">{page.identity.username}</dt>
        <dd className="mt-0.5 break-all text-ink">{username}</dd>
      </div>
      <div>
        <dt className="text-meta text-ink-3">{page.identity.displayName}</dt>
        <dd className="mt-0.5 break-all text-ink">{displayName}</dd>
      </div>
      <p className="text-meta leading-6 text-ink-3 sm:col-span-2">
        {page.identity.note}
      </p>
    </dl>
  )
}

function PasswordForm() {
  const toast = useToast()
  const {
    register,
    handleSubmit,
    reset,
    setError,
    clearErrors,
    formState: { errors, isSubmitting },
  } = useForm<PasswordForm>({
    resolver: zodResolver(schema),
    defaultValues: { current: '', next: '', confirm: '' },
  })

  const onSubmit = handleSubmit(async (values) => {
    clearErrors('root')
    try {
      const proof = await requestPasswordChangeProof(values.current)
      await changeAccountPassword(values.current, values.next, proof)
      reset()
      toast.show({ title: passwordCopy.success, tone: 'success' })
    } catch (error) {
      setError('root', {
        message: error instanceof ApiError ? error.message : passwordCopy.failed,
      })
    }
  })

  return (
    <form className="mt-5 space-y-4" onSubmit={onSubmit} noValidate>
      <div>
        <label
          htmlFor="account-current-password"
          className="mb-2 block text-body font-medium text-ink"
        >
          {passwordCopy.current}
        </label>
        <PasswordInput
          id="account-current-password"
          autoComplete="current-password"
          placeholder={passwordCopy.currentPlaceholder}
          invalid={Boolean(errors.current)}
          {...register('current')}
        />
        {errors.current ? (
          <p className="mt-1.5 text-meta text-danger">{errors.current.message}</p>
        ) : null}
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label
            htmlFor="account-next-password"
            className="mb-2 block text-body font-medium text-ink"
          >
            {passwordCopy.next}
          </label>
          <PasswordInput
            id="account-next-password"
            autoComplete="new-password"
            placeholder={passwordCopy.nextPlaceholder}
            invalid={Boolean(errors.next)}
            {...register('next')}
          />
          {errors.next ? (
            <p className="mt-1.5 text-meta text-danger">{errors.next.message}</p>
          ) : (
            <p className="mt-1.5 text-meta text-ink-3">{passwordCopy.policyHint}</p>
          )}
        </div>
        <div>
          <label
            htmlFor="account-confirm-password"
            className="mb-2 block text-body font-medium text-ink"
          >
            {passwordCopy.confirm}
          </label>
          <PasswordInput
            id="account-confirm-password"
            autoComplete="new-password"
            placeholder={passwordCopy.confirmPlaceholder}
            invalid={Boolean(errors.confirm)}
            {...register('confirm')}
          />
          {errors.confirm ? (
            <p className="mt-1.5 text-meta text-danger">{errors.confirm.message}</p>
          ) : null}
        </div>
      </div>

      {errors.root?.message ? (
        <p
          role="alert"
          className="rounded-md border border-danger/25 bg-danger/10 px-3 py-2 text-meta leading-5 text-danger"
        >
          {errors.root.message}
        </p>
      ) : null}

      <div className="flex justify-end">
        <Button type="submit" loading={isSubmitting}>
          {isSubmitting ? passwordCopy.submitting : passwordCopy.submit}
        </Button>
      </div>
    </form>
  )
}
