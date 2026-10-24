import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { registerUser, sendEmailCode, systemStatusQuery } from '@/features/auth/api'
import { AuthSplit } from '@/features/auth/auth-split'
import { GithubButton } from '@/features/auth/github-button'
import { Turnstile, isTurnstileConfigured } from '@/features/auth/turnstile'
import { ApiError } from '@/lib/api'
import { isLoggedIn } from '@/lib/auth'
import { safeRedirect } from '@/lib/navigation'
import { Button, Input, useToast } from '@/ui'

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

function RegisterPage() {
  const page = copy.pages.register
  const { redirect: redirectTo } = Route.useSearch()
  const navigate = useNavigate()
  const toast = useToast()
  const status = useQuery(systemStatusQuery())
  const emailVerification = status.data?.email_verification === true
  const [turnstileToken, setTurnstileToken] = useState('')
  const [sendingCode, setSendingCode] = useState(false)

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

  async function handleSendCode() {
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
    setSendingCode(true)
    try {
      await sendEmailCode(email, turnstileToken || undefined)
      toast.show({ title: page.codeSent, tone: 'success' })
    } catch (error) {
      toast.show({
        title: page.codeSendFailed,
        description: error instanceof ApiError ? error.message : undefined,
        tone: 'error',
      })
    } finally {
      setSendingCode(false)
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
    <AuthSplit headline={page.brandHeading} body={page.brandBody}>
      <h1 className="text-[24px] font-semibold tracking-tight text-ink">{page.title}</h1>
      <p className="mt-1.5 text-[13px] leading-6 text-muted">{page.lead}</p>

      <GithubButton
        redirectTo={safeRedirect(redirectTo, '/console')}
        onError={(message) => setError('root', { message })}
        className="mt-6"
      />

      <div className="my-5 flex items-center gap-3 text-[11.5px] text-muted">
        <span className="h-px flex-1 bg-line" aria-hidden="true" />
        {copy.pages.login.or}
        <span className="h-px flex-1 bg-line" aria-hidden="true" />
      </div>

      <form className="space-y-4" onSubmit={onSubmit} noValidate>
        <div>
          <label htmlFor="register-username" className="mb-1.5 block text-[12.5px] font-medium text-ink">
            {page.username}
          </label>
          <Input
            id="register-username"
            autoComplete="username"
            placeholder={page.usernamePlaceholder}
            invalid={Boolean(errors.username)}
            {...register('username')}
          />
          {errors.username ? (
            <p className="mt-1 text-[11.5px] text-danger">{errors.username.message}</p>
          ) : null}
        </div>

        <div>
          <label htmlFor="register-email" className="mb-1.5 block text-[12.5px] font-medium text-ink">
            {page.email}
          </label>
          <Input
            id="register-email"
            type="email"
            autoComplete="email"
            placeholder={page.emailPlaceholder}
            invalid={Boolean(errors.email)}
            {...register('email')}
          />
          {errors.email ? (
            <p className="mt-1 text-[11.5px] text-danger">{errors.email.message}</p>
          ) : (
            <p className="mt-1 text-[11px] text-muted">{page.emailOptional}</p>
          )}
        </div>

        {emailVerification ? (
          <div>
            <label htmlFor="register-code" className="mb-1.5 block text-[12.5px] font-medium text-ink">
              {page.code}
            </label>
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
                className="shrink-0"
                loading={sendingCode}
                onClick={() => void handleSendCode()}
              >
                {sendingCode ? page.sendingCode : page.sendCode}
              </Button>
            </div>
            {errors.code ? (
              <p className="mt-1 text-[11.5px] text-danger">{errors.code.message}</p>
            ) : (
              <p className="mt-1 text-[11px] text-muted">{page.emailVerificationHint}</p>
            )}
          </div>
        ) : null}

        <div>
          <label htmlFor="register-password" className="mb-1.5 block text-[12.5px] font-medium text-ink">
            {page.password}
          </label>
          <Input
            id="register-password"
            type="password"
            autoComplete="new-password"
            placeholder={page.passwordPlaceholder}
            invalid={Boolean(errors.password)}
            {...register('password')}
          />
          {errors.password ? (
            <p className="mt-1 text-[11.5px] text-danger">{errors.password.message}</p>
          ) : (
            <p className="mt-1 text-[11px] text-muted">{page.passwordHint}</p>
          )}
        </div>

        <div>
          <label htmlFor="register-confirm" className="mb-1.5 block text-[12.5px] font-medium text-ink">
            {page.confirm}
          </label>
          <Input
            id="register-confirm"
            type="password"
            autoComplete="new-password"
            placeholder={page.confirmPlaceholder}
            invalid={Boolean(errors.confirm)}
            {...register('confirm')}
          />
          {errors.confirm ? (
            <p className="mt-1 text-[11.5px] text-danger">{errors.confirm.message}</p>
          ) : null}
        </div>

        <Turnstile onToken={setTurnstileToken} />

        {errors.root?.message ? (
          <p
            role="alert"
            className="border border-danger/25 bg-danger-soft px-3 py-2 text-[12.5px] leading-5 text-danger"
          >
            {errors.root.message}
          </p>
        ) : null}

        <Button type="submit" className="w-full" loading={isSubmitting}>
          {isSubmitting ? page.submitting : page.submit}
        </Button>
      </form>

      <p className="mt-4 text-[12.5px] text-muted">
        {page.haveAccount}{' '}
        <Link to="/login" className="text-brand-dark hover:underline">
          {page.toLogin}
        </Link>
      </p>

      <p className="mt-6 border-t border-line pt-4 text-[11px] leading-5 text-muted">
        {page.agreementPrefix}{' '}
        <Link to="/docs" hash="terms" className="hover:text-ink">
          {copy.footer.terms}
        </Link>{' '}
        {page.agreementAnd}{' '}
        <Link to="/docs" hash="privacy" className="hover:text-ink">
          {copy.footer.privacy}
        </Link>
        {page.agreementPeriod}
      </p>
    </AuthSplit>
  )
}
