import { Link, createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import {
  OAuthChallengeRequired,
  completeOAuthLogin,
  takeOAuthRedirect,
} from '@/features/auth/api'
import { completeOAuthBind, takeOAuthBindCallback } from '@/features/auth/bindings'
import { ApiError } from '@/lib/api'
import { safeRedirect } from '@/lib/navigation'
import { Empty, PageHeader, buttonClasses } from '@/ui'

export const Route = createFileRoute('/oauth/$provider')({
  component: OAuthCallbackPage,
})

type CallbackState = 'working' | 'missing' | 'error'
type CallbackMode = 'login' | 'bind'

/**
 * OAuth landing page. The provider's registered callback URL points here; the
 * page forwards code+state to the backend exchange endpoint and stores the
 * returned access bundle. The backend also sets the HttpOnly refresh cookie,
 * so a later page load can renew the session.
 *
 * A handshake started from the account page carries a bind marker in session
 * storage instead of a login redirect: that branch links the provider to the
 * signed-in account and returns to 账号 without touching the session.
 */
function OAuthCallbackPage() {
  const { provider } = Route.useParams()
  const page = copy.pages.oauth
  const [state, setState] = useState<CallbackState>('working')
  const [mode, setMode] = useState<CallbackMode>('login')
  const [message, setMessage] = useState<string>(page.failedReason)

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code') ?? ''
    const flowState = params.get('state') ?? ''

    if (!code || !flowState) {
      setState('missing')
      return
    }

    // A bind started from the account page has no login bundle to exchange:
    // the backend links the provider and answers { action: "bind" }.
    const bindProvider = takeOAuthBindCallback(flowState)
    if (bindProvider) {
      setMode('bind')
      let cancelled = false
      void completeOAuthBind(provider, code, flowState)
        .then(() => {
          if (cancelled) return
          window.location.assign(
            `/console/account?bound=${encodeURIComponent(bindProvider)}`,
          )
        })
        .catch((error: unknown) => {
          if (cancelled) return
          setState('error')
          setMessage(
            error instanceof ApiError && error.message ? error.message : page.failedReason,
          )
        })
      return () => {
        cancelled = true
      }
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

  const isBind = mode === 'bind'

  return (
    <div className="mx-auto max-w-[760px] px-5 py-14">
      <PageHeader
        title={isBind ? page.bindTitle : page.title}
        lead={state === 'working' ? (isBind ? page.bindLead : page.lead) : undefined}
      />

      {state === 'working' ? (
        <div className="mt-8 space-y-4" aria-busy="true">
          <div className="h-4 w-56 animate-pulse rounded-sm bg-line" />
          <div className="h-4 w-full animate-pulse rounded-sm bg-line" />
          <div className="h-4 w-3/4 animate-pulse rounded-sm bg-line" />
        </div>
      ) : (
        <div className="mt-6">
          <Empty
            title={isBind ? page.bindFailedTitle : page.failedTitle}
            reason={state === 'missing' ? page.missingReason : message}
            next={isBind ? page.bindNext : page.next}
            action={
              <Link
                to={isBind ? '/console/account' : '/login'}
                className={buttonClasses('secondary', 'md')}
              >
                {isBind ? page.backAccount : page.backLogin}
              </Link>
            }
          />
        </div>
      )}
    </div>
  )
}
