import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { copy } from '@/copy'
import { fetchSelfProfile } from '@/features/auth/account'
import { systemStatusQuery } from '@/features/auth/api'
import {
  fetchOAuthBindings,
  startGithubBind,
  startGoogleBind,
  unbindOAuthProvider,
  type BindableProvider,
} from '@/features/auth/bindings'
import { GithubMark } from '@/features/auth/github-button'
import { GoogleMark } from '@/features/auth/google-button'
import { ApiError } from '@/lib/api'
import { Button, Dialog, PasswordInput, useToast } from '@/ui'

import { Skeleton } from './DataState'

const page = copy.pages.consoleAccount.bindings

const providerLabel = (provider: BindableProvider) =>
  provider === 'github' ? page.github : page.google

const providers: BindableProvider[] = ['github', 'google']

type PendingAction =
  | { kind: 'bind'; provider: BindableProvider }
  | { kind: 'unbind'; provider: BindableProvider; providerId: number }

/**
 * GitHub / Google binding for the signed-in account. Binding uses the same
 * OAuth handshake as login but with intent=bind, so the browser leaves the page
 * and lands back on /oauth/$provider; that callback returns here with
 * ?bound=<provider>, which this panel turns into a toast and a refetch.
 */
export function OAuthBindings() {
  const toast = useToast()
  const profile = useQuery({
    queryKey: ['account', 'self'],
    queryFn: fetchSelfProfile,
    staleTime: 60_000,
  })
  const status = useQuery(systemStatusQuery())
  const bindings = useQuery({
    queryKey: ['account', 'oauth-bindings'],
    queryFn: fetchOAuthBindings,
    staleTime: 60_000,
  })

  const [action, setAction] = useState<PendingAction | null>(null)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [redirecting, setRedirecting] = useState(false)
  const handledBound = useRef(false)

  useEffect(() => {
    if (handledBound.current) return
    handledBound.current = true
    const params = new URLSearchParams(window.location.search)
    const bound = params.get('bound')
    if (bound !== 'github' && bound !== 'google') return
    toast.show({
      title: page.bindSuccess(providerLabel(bound)),
      tone: 'success',
    })
    params.delete('bound')
    const query = params.toString()
    window.history.replaceState(
      {},
      '',
      `${window.location.pathname}${query ? `?${query}` : ''}`,
    )
    void profile.refetch()
    void bindings.refetch()
  }, [bindings, profile, toast])

  const enabled: Record<BindableProvider, boolean> = {
    github: status.data?.github_oauth === true,
    google: Boolean(
      status.data?.custom_oauth_providers?.some((provider) => provider.slug === 'google'),
    ),
  }
  const googleBinding = bindings.data?.find((binding) => binding.provider_slug === 'google')
  const bound: Record<BindableProvider, boolean> = {
    github: Boolean(profile.data?.github_id),
    google: Boolean(googleBinding),
  }
  const hasPassword = profile.data?.has_password !== false

  function openBind(provider: BindableProvider) {
    setError('')
    setPassword('')
    setAction({ kind: 'bind', provider })
  }

  function openUnbind(provider: BindableProvider, providerId: number) {
    setError('')
    setPassword('')
    setAction({ kind: 'unbind', provider, providerId })
  }

  function closeDialog(open: boolean) {
    if (open || submitting) return
    setAction(null)
    setPassword('')
    setError('')
    setRedirecting(false)
  }

  async function submit() {
    if (!action) return
    if (!password.trim()) {
      setError(page.passwordRequired)
      return
    }
    setError('')
    setSubmitting(true)
    try {
      if (action.kind === 'bind') {
        setRedirecting(true)
        if (action.provider === 'github') {
          await startGithubBind(password)
        } else {
          await startGoogleBind(password)
        }
        return
      }
      await unbindOAuthProvider(action.providerId, password)
      setAction(null)
      setPassword('')
      toast.show({
        title: page.unbindSuccess(providerLabel(action.provider)),
        tone: 'success',
      })
      void bindings.refetch()
    } catch (caught) {
      setError(caught instanceof ApiError && caught.message ? caught.message : page.failed)
    } finally {
      setSubmitting(false)
      setRedirecting(false)
    }
  }

  return (
    <section className="mt-6 glass p-6">
      <h2 className="text-title font-semibold tracking-tight text-ink">{page.title}</h2>
      <p className="mt-1.5 text-meta leading-6 text-ink-3">{page.hint}</p>

      {bindings.isPending || profile.isPending ? (
        <div className="mt-5 space-y-3">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : (
        <ul className="mt-4 divide-y divide-line/60">
          {providers.map((provider) => (
            <li
              key={provider}
              className="flex flex-col gap-3 py-4 sm:flex-row sm:items-center sm:justify-between"
            >
              <div className="flex min-w-0 items-center gap-3">
                <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full border border-white/70 bg-white/70 text-ink">
                  {provider === 'github' ? <GithubMark /> : <GoogleMark />}
                </span>
                <div className="min-w-0">
                  <p className="text-body font-medium text-ink">{providerLabel(provider)}</p>
                  <p className="text-meta text-ink-3">
                    {bound[provider] ? page.bound : page.unbound}
                    {!enabled[provider] ? ` · ${page.unavailable}` : ''}
                  </p>
                </div>
              </div>

              {bound[provider] ? (
                provider === 'google' && googleBinding ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!hasPassword}
                    title={!hasPassword ? page.noPassword : undefined}
                    onClick={() => openUnbind('google', googleBinding.provider_id)}
                  >
                    {page.unbind}
                  </Button>
                ) : null
              ) : (
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={!enabled[provider] || !hasPassword || status.isPending}
                  title={!hasPassword ? page.noPassword : undefined}
                  onClick={() => openBind(provider)}
                >
                  {page.bind}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      {!hasPassword && !profile.isPending ? (
        <p className="mt-2 text-meta leading-6 text-ink-3">{page.noPassword}</p>
      ) : null}

      <Dialog
        open={action !== null}
        onOpenChange={closeDialog}
        title={
          action?.kind === 'bind'
            ? page.bindTitle(providerLabel(action.provider))
            : action?.kind === 'unbind'
              ? page.unbindTitle(providerLabel(action.provider))
              : ''
        }
        description={action?.kind === 'unbind' ? page.unbindHint : page.hint}
        footer={
          <div className="flex w-full items-center justify-between gap-2">
            <Button
              variant="secondary"
              onClick={() => closeDialog(false)}
              disabled={submitting}
            >
              {copy.pages.consoleShared.cancel}
            </Button>
            <Button onClick={() => void submit()} loading={submitting} disabled={redirecting}>
              {redirecting ? page.redirecting : action?.kind === 'unbind' ? page.unbind : page.bind}
            </Button>
          </div>
        }
      >
        <div className="space-y-2">
          <label
            htmlFor="oauth-binding-password"
            className="block text-body font-medium text-ink"
          >
            {page.password}
          </label>
          <PasswordInput
            id="oauth-binding-password"
            autoComplete="current-password"
            placeholder={page.passwordPlaceholder}
            invalid={Boolean(error)}
            value={password}
            disabled={submitting}
            onChange={(event) => setPassword(event.target.value)}
          />
          {error ? <p className="text-meta text-danger">{error}</p> : null}
        </div>
      </Dialog>
    </section>
  )
}
