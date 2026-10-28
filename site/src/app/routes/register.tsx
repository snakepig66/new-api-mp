import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { registerUser, sendEmailCode as requestEmailCode, systemStatusQuery } from '@/features/auth/api'
import { AuthSplit } from '@/features/auth/auth-split'
import { GithubButton } from '@/features/auth/github-button'
import { Turnstile, isTurnstileConfigured } from '@/features/auth/turnstile'
import { ApiError } from '@/lib/api'
import { isLoggedIn } from '@/lib/auth'
import { safeRedirect } from '@/lib/navigation'
import { Button, Input, PasswordInput, useToast } from '@/ui'

interface RegisterSearch {
  redirect?: string
}

const searchSchema = (search: Record<string, unknown>): RegisterSearch => ({
  redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
})

export const Route = createFileRoute('/register')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (isLoggedIn()) throw redirect({ to: '/console' })
  },
  component: RegisterPage,
})

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

function buildSchema(emailVerification: boolean) {
  return z
    .object({
      username: z
        .string()
        .trim()
        .min(1, copy.validation.required)
        .max(20, copy.validation.username),
      email: z
        .string()
        .trim()
        .min(1, copy.validation.required)
        .regex(EMAIL_RE, copy.validation.email),
      password: z.string().min(8, copy.validation.password8).max(128),
      confirm: z.string().min(1, copy.validation.required),
      code: z.string().trim(),
    })
    .refine((values) => values.password === values.confirm, {
      path: ['confirm'],
      message: copy.validation.mismatch,
    })
    .superRefine((values, ctx) => {
      if (emailVerification && !values.code) {
        ctx.addIssue({ code: 'custom', path: ['code'], message: copy.validation.required })
      }
    })
}

type RegisterForm = z.infer<ReturnType<typeof buildSchema>>

function FieldLabel({ htmlFor, children }: { htmlFor: string; children: string }) {
  return (
    <label htmlFor={htmlFor} className="mb-2 block text-meta font-medium text-ink">
      {children}
    </label>
  )
}

function RegisterPage() {
  const page = copy.pages.register
  const { redirect: redirectTo } = Route.useSearch()
  const navigate = useNavigate()
  const toast = useToast()
  const status = useQuery(systemStatusQuery())
  const emailVerification = status.data?.email_verification === true
  const [turnstileToken, setTurnstileToken] = useState('')
  const [sending, setSending] = useState(false)

  const {
    register,
    handleSubmit,
    getValues,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<RegisterForm>({
    resolver: zodResolver(buildSchema(emailVerification)),
    defaultValues: { username: '', email: '', password: '', confirm: '', code: '' },
  })

  async function requestCode() {
    const email = getValues('email').trim()
    if (!EMAIL_RE.test(email)) {
      setError('email', { message: copy.validation.email })
      return
    }
    if (status.data?.turnstile_check) {
      if (!isTurnstileConfigured()) {
        setError('root', { message: copy.pages.login.turnstileMissing })
        return
      }
      if (!turnstileToken) {
        setError('root', { message: copy.pages.login.turnstileRequired })
        return
      }
    }
    setSending(true)
    try {
      await requestEmailCode(email, turnstileToken || undefined)
      toast.show({ title: page.codeSent, tone: 'success' })
    } catch (error) {
      toast.show({
        title: page.codeSendFailed,
        description: error instanceof ApiError ? error.message : undefined,
        tone: 'error',
      })
    } finally {
      setSending(false)
    }
  }

  const onSubmit = handleSubmit(async (values) => {
    if (status.data?.turnstile_check) {
      if (!isTurnstileConfigured()) {
        setError('root', { message: copy.pages.login.turnstileMissing })
        return
      }
      if (!turnstileToken) {
        setError('root', { message: copy.pages.login.turnstileRequired })
        return
      }
    }
    try {
      await registerUser(
        {
          username: values.username,
          email: values.email,
          password: values.password,
          verificationCode: values.code || undefined,
        },
        turnstileToken || undefined,
      )
      await navigate({
        to: '/login',
        search: { registered: true, redirect: safeRedirect(redirectTo, '/console') },
      })
    } catch (error) {
      setError('root', {
        message: error instanceof ApiError && error.status === 0 ? error.message : page.failed,
      })
    }
  })

  return (
    <AuthSplit headline={page.brandHeading} body={page.brandBody} points={page.points}>
      <h1 className="text-h1 font-semibold leading-tight tracking-tight text-ink">{page.title}</h1>
      <p className="mt-2 text-body leading-6 text-ink-2">{page.lead}</p>

      <form className="mt-7 space-y-4" onSubmit={onSubmit} noValidate>
        <div>
          <FieldLabel htmlFor="register-username">{page.username}</FieldLabel>
          <Input
            id="register-username"
            autoComplete="username"
            placeholder={page.usernamePlaceholder}
            invalid={Boolean(errors.username)}
            {...register('username')}
          />
          {errors.username ? (
            <p className="mt-1.5 text-meta text-danger">{errors.username.message}</p>
          ) : null}
        </div>

        <div>
          <FieldLabel htmlFor="register-email">{page.email}</FieldLabel>
          <Input
            id="register-email"
            type="email"
            autoComplete="email"
            placeholder={page.emailPlaceholder}
            invalid={Boolean(errors.email)}
            {...register('email')}
          />
          {errors.email ? (
            <p className="mt-1.5 text-meta text-danger">{errors.email.message}</p>
          ) : (
            <p className="mt-1.5 text-meta text-ink-3">{page.emailOptional}</p>
          )}
        </div>

        {emailVerification ? (
          <div>
            <FieldLabel htmlFor="register-code">{page.code}</FieldLabel>
            <div className="flex gap-2">
              <Input
                id="register-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                placeholder={page.codePlaceholder}
                invalid={Boolean(errors.code)}
                {...register('code')}
              />
              <Button
                variant="secondary"
                className="h-12 shrink-0 px-5"
                loading={sending}
                onClick={() => void requestCode()}
              >
                {sending ? page.sendingCode : page.sendCode}
              </Button>
            </div>
            {errors.code ? (
              <p className="mt-1.5 text-meta text-danger">{errors.code.message}</p>
            ) : (
              <p className="mt-1.5 text-meta text-ink-3">{page.emailVerificationHint}</p>
            )}
          </div>
        ) : null}

        <div>
          <FieldLabel htmlFor="register-password">{page.password}</FieldLabel>
          <PasswordInput
            id="register-password"
            autoComplete="new-password"
            placeholder={page.passwordPlaceholder}
            invalid={Boolean(errors.password)}
            {...register('password')}
          />
          {errors.password ? (
            <p className="mt-1.5 text-meta text-danger">{errors.password.message}</p>
          ) : (
            <p className="mt-1.5 text-meta text-ink-3">{page.passwordHint}</p>
          )}
        </div>

        <div>
          <FieldLabel htmlFor="register-confirm">{page.confirm}</FieldLabel>
          <PasswordInput
            id="register-confirm"
            autoComplete="new-password"
            placeholder={page.confirmPlaceholder}
            invalid={Boolean(errors.confirm)}
            {...register('confirm')}
          />
          {errors.confirm ? (
            <p className="mt-1.5 text-meta text-danger">{errors.confirm.message}</p>
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
        {copy.pages.login.or}
        <span className="h-px flex-1 bg-line" aria-hidden="true" />
      </div>

      <GithubButton
        redirectTo={safeRedirect(redirectTo, '/console')}
        onError={(message) => setError('root', { message })}
      />

      <p className="mt-6 text-body text-ink-2">
        {page.haveAccount}{' '}
        <Link to="/login" className="text-brand hover:underline">
          {page.toLogin}
        </Link>
      </p>

      <p className="mt-6 border-t border-line pt-4 text-meta leading-6 text-ink-3">
        {page.agreementPrefix}{' '}
        <Link to="/agreement" className="text-ink-2 hover:text-brand">
          {copy.footer.terms}
        </Link>{' '}
        {page.agreementAnd}{' '}
        <Link to="/privacy" className="text-ink-2 hover:text-brand">
          {copy.footer.privacy}
        </Link>
        {page.agreementPeriod}
      </p>
    </AuthSplit>
  )
}
