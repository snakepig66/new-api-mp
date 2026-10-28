import { Link, createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { resetPassword } from '@/features/auth/api'
import { AuthSplit } from '@/features/auth/auth-split'
import { ApiError } from '@/lib/api'
import { clearSession } from '@/lib/session'
import { Button, buttonClasses, useToast } from '@/ui'

const page = copy.pages.resetPassword
const loginCopy = copy.pages.login
const shared = copy.pages.consoleShared

interface ResetSearch {
  email?: string
  token?: string
}

const searchSchema = (search: Record<string, unknown>): ResetSearch => ({
  email: typeof search.email === 'string' ? search.email : undefined,
  token: typeof search.token === 'string' ? search.token : undefined,
})

export const Route = createFileRoute('/user/reset')({
  validateSearch: searchSchema,
  component: ResetPasswordPage,
})

function ResetPasswordPage() {
  const { email, token } = Route.useSearch()
  const toast = useToast()
  const [working, setWorking] = useState(false)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')

  const valid = Boolean(email && token)

  async function submit() {
    if (!email || !token) return
    setWorking(true)
    setError('')
    try {
      const next = await resetPassword(email, token)
      clearSession()
      setPassword(next)
    } catch (resetError) {
      setError(resetError instanceof ApiError ? resetError.message : page.failed)
    } finally {
      setWorking(false)
    }
  }

  async function copyPassword() {
    const ok = await writeClipboard(password)
    toast.show({
      title: ok ? page.copied : shared.copyFailed,
      tone: ok ? 'success' : 'error',
    })
  }

  return (
    <AuthSplit headline={loginCopy.brandHeading} body={loginCopy.brandBody} points={loginCopy.points}>
      <h1 className="text-h1 font-semibold leading-tight tracking-tight text-ink">
        {valid ? page.title : page.invalidTitle}
      </h1>
      <p className="mt-2 text-body leading-6 text-ink-2">
        {valid ? page.lead : page.invalidReason}
      </p>

      {!valid ? (
        <div className="mt-7 space-y-3">
          <p className="text-meta leading-6 text-ink-3">{page.invalidNext}</p>
          <div className="flex flex-wrap gap-3">
            <Link to="/forgot-password" className={buttonClasses('primary', 'md')}>
              {page.requestAgain}
            </Link>
            <Link to="/login" className="inline-flex h-10 items-center px-1 text-body text-brand hover:underline">
              {page.backToLogin}
            </Link>
          </div>
        </div>
      ) : password ? (
        <div className="mt-7 space-y-4">
          <div className="rounded-md border border-brand/25 bg-brand-soft px-4 py-3">
            <p className="text-meta font-medium text-brand-ink">{page.successTitle}</p>
            <p className="mt-1 text-meta leading-6 text-brand-ink">{page.successBody}</p>
            <p className="mt-3 text-caption text-ink-3">{page.generatedLabel}</p>
            <code className="mt-1 block break-all glass-inset rounded-sm px-3 py-2 font-mono text-body text-ink">
              {password}
            </code>
          </div>
          <Button type="button" className="w-full" onClick={() => void copyPassword()}>
            {page.copy}
          </Button>
          <Link
            to="/login"
            className="inline-block text-body text-brand hover:underline"
          >
            {page.toLogin}
          </Link>
        </div>
      ) : (
        <div className="mt-7 space-y-4">
          <dl className="glass-inset px-4 py-3 text-meta">
            <div className="flex items-baseline justify-between gap-4">
              <dt className="text-ink-3">{page.email}</dt>
              <dd className="break-all text-ink">{email}</dd>
            </div>
          </dl>

          {error ? (
            <p
              role="alert"
              className="rounded-md bg-danger/10 px-3 py-2 text-meta leading-5 text-danger"
            >
              {error}
            </p>
          ) : null}

          <Button
            type="button"
            size="lg"
            className="w-full"
            loading={working}
            onClick={() => void submit()}
          >
            {working ? page.submitting : page.submit}
          </Button>

          <Link
            to="/login"
            className="inline-block text-meta text-ink-3 hover:text-ink hover:underline"
          >
            {page.backToLogin}
          </Link>
        </div>
      )}
    </AuthSplit>
  )
}

async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Fall through to the legacy path.
  }
  try {
    const area = document.createElement('textarea')
    area.value = text
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.select()
    const copied = document.execCommand('copy')
    area.remove()
    return copied
  } catch {
    return false
  }
}
