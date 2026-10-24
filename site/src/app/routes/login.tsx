import { zodResolver } from '@hookform/resolvers/zod'
import { Link, createFileRoute, redirect } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { isLoggedIn, login } from '@/lib/auth'
import { safeRedirect } from '@/lib/navigation'
import { Button, Input, LogoMark } from '@/ui'

const searchSchema = (search: Record<string, unknown>): { redirect?: string } => ({
  redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
})

export const Route = createFileRoute('/login')({
  validateSearch: searchSchema,
  beforeLoad: () => {
    if (isLoggedIn()) throw redirect({ to: '/console' })
  },
  component: LoginPage,
})

const schema = z.object({
  username: z.string().trim().min(1, copy.validation.required),
  password: z.string().min(1, copy.validation.required),
})

type LoginForm = z.infer<typeof schema>

function LoginPage() {
  const { redirect: redirectTo } = Route.useSearch()
  const page = copy.pages.login

  const {
    register,
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<LoginForm>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })

  const onSubmit = handleSubmit(async (values) => {
    try {
      await login(values.username, values.password)
      window.location.assign(safeRedirect(redirectTo, '/console'))
    } catch (error) {
      setError('root', {
        message: error instanceof ApiError ? error.message : page.failed,
      })
    }
  })

  return (
    <div className="mx-auto grid max-w-[1180px] gap-12 px-5 py-12 lg:grid-cols-[minmax(0,1fr)_400px] lg:gap-20 lg:py-20">
      <div className="hidden lg:block">
        <LogoMark size={34} className="text-brand-dark" />
        <h1 className="mt-8 max-w-[520px] text-[32px] font-semibold leading-[1.25] tracking-tight text-ink">
          {copy.pages.home.headlineLines[0]}
          <br />
          {copy.pages.home.headlineLines[1]}
        </h1>
        <p className="mt-4 max-w-md text-[13.5px] leading-7 text-muted">{page.brandBody}</p>
        <dl className="mt-10 max-w-sm border-t border-line text-[12.5px]">
          {copy.pages.home.facts.map((fact) => (
            <div
              key={fact.label}
              className="flex items-baseline justify-between gap-4 border-b border-line/70 py-2.5"
            >
              <dt className="text-muted">{fact.label}</dt>
              <dd className="font-medium text-ink">{fact.value}</dd>
            </div>
          ))}
        </dl>
      </div>

      <div className="w-full max-w-[400px]">
        <h1 className="text-[24px] font-semibold tracking-tight text-ink">{page.title}</h1>
        <p className="mt-1.5 text-[13px] leading-6 text-muted">{page.lead}</p>

        <form className="mt-7 space-y-4" onSubmit={onSubmit} noValidate>
          <div>
            <label htmlFor="login-username" className="mb-1.5 block text-[12.5px] font-medium text-ink">
              {page.username}
            </label>
            <Input
              id="login-username"
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
            <label htmlFor="login-password" className="mb-1.5 block text-[12.5px] font-medium text-ink">
              {page.password}
            </label>
            <Input
              id="login-password"
              type="password"
              autoComplete="current-password"
              placeholder={page.passwordPlaceholder}
              invalid={Boolean(errors.password)}
              {...register('password')}
            />
            {errors.password ? (
              <p className="mt-1 text-[11.5px] text-danger">{errors.password.message}</p>
            ) : null}
          </div>

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
          {page.noAccount}{' '}
          <Link to="/register" className="text-brand-dark hover:underline">
            {page.toRegister}
          </Link>
        </p>

        <p className="mt-8 border-t border-line pt-4 text-[11.5px] leading-5 text-muted">
          {page.note}
        </p>
      </div>
    </div>
  )
}
