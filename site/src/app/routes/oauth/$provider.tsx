import { Link, createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import {
  OAuthChallengeRequired,
  completeOAuthLogin,
  takeOAuthRedirect,
} from '@/features/auth/api'
import { safeRedirect } from '@/lib/navigation'
import { Empty, PageHeader, buttonClasses } from '@/ui'

export const Route = createFileRoute('/oauth/$provider')({
  component: OAuthCallbackPage,
})

type CallbackState = 'working' | 'missing' | 'error'

/**
 * OAuth landing page. The provider's registered callback URL points here; the
 * page forwards code+state to the backend exchange endpoint and stores the
 * returned access bundle. The backend also sets the HttpOnly refresh cookie,
 * so a later page load can renew the session.
 *
 * The redirect target from before the handshake is remembered in session
 * storage (see features/auth/api.ts) and validated same-origin before use.
 */
function OAuthCallbackPage() {
  const { provider } = Route.useParams()
  const page = copy.pages.oauth
  const [state, setState] = useState<CallbackState>('working')
  const [message, setMessage] = useState<string>(page.failedReason)

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code') ?? ''
    const flowState = params.get('state') ?? ''

    if (!code || !flowState) {
      setState('missing')
      return
    }

    let cancelled = false
    void completeOAuthLogin(provider, code, flowState)
      .then(() => {
        if (cancelled) return
        const target = takeOAuthRedirect() ?? undefined
        window.location.assign(safeRedirect(target, '/console'))
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setState('error')
        setMessage(
          error instanceof OAuthChallengeRequired ? error.message : page.failedReason,
        )
      })

    return () => {
      cancelled = true
    }
  }, [provider, page.failedReason])

  return (
    <div className="mx-auto max-w-[760px] px-5 py-14">
      <PageHeader title={page.title} lead={state === 'working' ? page.lead : undefined} />

      {state === 'working' ? (
        <div className="mt-8 space-y-4" aria-busy="true">
          <div className="h-4 w-56 animate-pulse rounded-[3px] bg-line" />
          <div className="h-4 w-full animate-pulse rounded-[3px] bg-line" />
          <div className="h-4 w-3/4 animate-pulse rounded-[3px] bg-line" />
        </div>
      ) : (
        <div className="mt-6">
          <Empty
            title={page.failedTitle}
            reason={state === 'missing' ? page.missingReason : message}
            next={page.next}
            action={
              <Link to="/login" className={buttonClasses('secondary', 'md')}>
                {page.backLogin}
              </Link>
            }
          />
        </div>
      )}
    </div>
  )
}
