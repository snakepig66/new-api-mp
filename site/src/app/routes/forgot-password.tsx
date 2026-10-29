import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { requestPasswordReset, systemStatusQuery } from '@/features/auth/api'
import { AuthSplit } from '@/features/auth/auth-split'
import { Turnstile, isTurnstileConfigured } from '@/features/auth/turnstile'
import { ApiError } from '@/lib/api'
import { Button, Input } from '@/ui'

const page = copy.pages.forgotPassword
const loginCopy = copy.pages.login

const schema = z.object({
  email: z
    .string()
    .trim()
    .min(1, copy.validation.required)
    .regex(/^[^\s@]+@[^\s@]+\.[^\s@]+$/, copy.validation.email),
})

type ForgotForm = z.infer<typeof schema>

export const Route = createFileRoute('/forgot-password')({
  component: ForgotPasswordPage,
})

function ForgotPasswordPage() {
  const status = useQuery(systemStatusQuery())
  const [turnstileToken, setTurnstileToken] = useState('')
  const [sentTo, setSentTo] = useState('')

  const {
    register,
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<ForgotForm>({
    resolver: zodResolver(schema),
    defaultValues: { email: '' },
  })

  const onSubmit = handleSubmit(async (values) => {
    if (status.data?.turnstile_check) {
      if (!isTurnstileConfigured()) {
        setError('root', { message: loginCopy.turnstileMissing })
        return
      }
      if (!turnstileToken) {
        setError('root', { message: loginCopy.turnstileRequired })
        return
      }
    }
    try {
      await requestPasswordReset(values.email, turnstileToken || undefined)
      setSentTo(values.email)
    } catch (error) {
      setError('root', {
        message: error instanceof ApiError ? error.message : page.failed,
      })
    }
  })

  return (
    <AuthSplit headline={loginCopy.brandHeading} body={loginCopy.brandBody} points={loginCopy.points}>
      <h1 className="text-h1 font-semibold leading-tight tracking-tight text-ink">
        {page.title}
      </h1>
      <p className="mt-2 text-body leading-6 text-ink-2">{page.lead}</p>

      {sentTo ? (
        <>
          <div
            role="status"
            className="mt-7 rounded-md border border-brand/25 bg-brand-soft px-4 py-3 text-meta leading-6 text-brand-ink"
          >
            <p className="font-medium">{page.sentTitle}</p>
            <p className="mt-1">{page.sentBody(sentTo)}</p>
          </div>
          <Link
            to="/login"
            className="mt-6 inline-block text-body text-brand hover:underline"
          >
            {page.backToLogin}
          </Link>
        </>
      ) : (
        <form className="mt-7 space-y-4" onSubmit={onSubmit} noValidate>
          {status.data && !status.data.smtp_configured ? (
            <div
              role="alert"
              className="rounded-md border border-warn/25 bg-warn/10 px-4 py-3 text-meta text-warn"
            >
              当前系统尚未配置邮件服务（SMTP），无法自动发送重置密码邮件。请联系平台管理员协助重置密码。
            </div>
          ) : null}

          <div>
            <label
              htmlFor="forgot-email"
              className="mb-2 block text-meta font-medium text-ink"
            >
              {page.email}
            </label>
            <Input
              id="forgot-email"
              type="email"
              autoComplete="email"
              placeholder={page.emailPlaceholder}
              invalid={Boolean(errors.email)}
              {...register('email')}
            />
            {errors.email ? (
              <p className="mt-1.5 text-meta text-danger">{errors.email.message}</p>
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

          <Button
            type="submit"
            size="lg"
            className="w-full"
            loading={isSubmitting}
            disabled={isSubmitting || (status.data ? !status.data.smtp_configured : false)}
          >
            {isSubmitting ? page.submitting : page.submit}
          </Button>

          <Link
            to="/login"
            className="inline-block text-meta text-ink-3 hover:text-ink hover:underline"
          >
            {page.backToLogin}
          </Link>
        </form>
      )}

      <p className="mt-8 border-t border-line pt-4 text-meta leading-6 text-ink-3">
        {page.note}
      </p>
    </AuthSplit>
  )
}
