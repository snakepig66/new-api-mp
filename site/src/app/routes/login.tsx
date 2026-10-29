import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, redirect } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { loginUser, systemStatusQuery } from '@/features/auth/api'
import { AuthSplit } from '@/features/auth/auth-split'
import { GithubButton } from '@/features/auth/github-button'
import { Turnstile, isTurnstileConfigured } from '@/features/auth/turnstile'
import { ApiError } from '@/lib/api'
import { isLoggedIn } from '@/lib/auth'
import { safeRedirect } from '@/lib/navigation'
import { Button, Input, PasswordInput } from '@/ui'

interface LoginSearch {
  redirect?: string
  registered?: boolean
}

const searchSchema = (search: Record<string, unknown>): LoginSearch => ({
  redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  registered: search.registered === true || search.registered === '1' ? true : undefined,
})

export const Route = createFileRoute('/login')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (isLoggedIn()) throw redirect({ to: '/console' })
  },
  component: LoginPage,
})

const schema = z.object({
  identifier: z.string().trim().min(1, copy.validation.required),
  password: z.string().min(1, copy.validation.required),
})

type LoginForm = z.infer<typeof schema>

function LoginPage() {
  const { redirect: redirectTo, registered } = Route.useSearch()
  const page = copy.pages.login
  const status = useQuery(systemStatusQuery())
  const [turnstileToken, setTurnstileToken] = useState('')

  const {
    register,
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<LoginForm>({
    resolver: zodResolver(schema),
    defaultValues: { identifier: '', password: '' },
  })

  const onSubmit = handleSubmit(async (values) => {
    if (status.data?.turnstile_check) {
      if (!isTurnstileConfigured()) {
        setError('root', { message: page.turnstileMissing })
        return
      }
      if (!turnstileToken) {
        setError('root', { message: page.turnstileRequired })
        return
      }
    }
    try {
      await loginUser(values.identifier, values.password, turnstileToken || undefined)
      window.location.assign(safeRedirect(redirectTo, '/console'))
    } catch (error) {
      setError('root', {
        message:
          error instanceof ApiError && error.status === 0
            ? error.message
            : page.failed,
      })
    }
  })

  return (
    <AuthSplit headline={page.brandHeading} body={page.brandBody} points={page.points}>
      <h1 className="text-h1 font-semibold leading-tight tracking-tight text-ink">{page.title}</h1>
      <p className="mt-2 text-body leading-6 text-ink-2">{page.lead}</p>

      {registered ? (
        <p
          role="status"
          className="mt-5 rounded-md bg-brand-soft px-3 py-2 text-meta leading-5 text-brand-ink"
        >
          {page.registered}
        </p>
      ) : null}

      <form className="mt-7 space-y-4" onSubmit={onSubmit} noValidate>
        <div>
          <label htmlFor="login-identifier" className="mb-2 block text-meta font-medium text-ink">
            {page.identifier}
          </label>
          <Input
            id="login-identifier"
            autoComplete="username"
            placeholder={page.identifierPlaceholder}
            invalid={Boolean(errors.identifier)}
            {...register('identifier')}
          />
          {errors.identifier ? (
            <p className="mt-1.5 text-meta text-danger">{errors.identifier.message}</p>
          ) : null}
        </div>

        <div>
          <div className="mb-2 flex items-baseline justify-between gap-3">
            <label htmlFor="login-password" className="block text-meta font-medium text-ink">
              {page.password}
            </label>
            <Link
              to="/forgot-password"
              className="text-meta text-ink-3 transition-colors hover:text-brand hover:underline"
            >
              {page.forgot}
            </Link>
          </div>
          <PasswordInput
            id="login-password"
            autoComplete="current-password"
            placeholder={page.passwordPlaceholder}
            invalid={Boolean(errors.password)}
            {...register('password')}
          />
          {errors.password ? (
            <p className="mt-1.5 text-meta text-danger">{errors.password.message}</p>
          ) : null}
        </div>

        <Turnstile onToken={setTurnstileToken} />

        {errors.root?.message ? (
          <p
            role="alert"
            className="rounded-md bg-danger/10 px-3 py-2 text-meta leading-5 text-danger"
          >
            {errors.root.message}
          </p>
        ) : null}

        <Button type="submit" size="lg" className="w-full" loading={isSubmitting}>
          {isSubmitting ? page.submitting : page.submit}
        </Button>
      </form>

      <div className="my-6 flex items-center gap-3 text-meta text-ink-3">
        <span className="h-px flex-1 bg-line" aria-hidden="true" />
        {page.or}
        <span className="h-px flex-1 bg-line" aria-hidden="true" />
      </div>

      <GithubButton
        redirectTo={safeRedirect(redirectTo, '/console')}
        onError={(message) => setError('root', { message })}
      />

      <p className="mt-6 text-body text-ink-2">
        {page.noAccount}{' '}
        <Link to="/register" className="text-brand hover:underline">
          {page.toRegister}
        </Link>
      </p>

      <p className="mt-8 border-t border-line pt-4 text-meta leading-6 text-ink-3">{page.note}</p>
    </AuthSplit>
  )
}
