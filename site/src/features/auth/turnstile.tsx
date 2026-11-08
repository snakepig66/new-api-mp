import { useEffect, useRef } from 'react'

/**
 * Cloudflare Turnstile placeholder.
 *
 * Turnstile is opt-in twice: the backend must enable it (`turnstile_check` in
 * /api/status) and the frontend must be built with VITE_TURNSTILE_SITE_KEY.
 * When the env var is absent this component renders nothing at all — the login
 * form stays clean on local/dev builds. If the backend has Turnstile enabled
 * but the site key is missing, login will be rejected by the server; set the
 * env var before deploying.
 *
 * The token is submitted as ?turnstile=... (middleware/turnstile-check.go),
 * not inside the JSON body.
 */

const SITE_KEY = (import.meta.env.VITE_TURNSTILE_SITE_KEY as string | undefined)?.trim()

export function isTurnstileConfigured(): boolean {
  return Boolean(SITE_KEY)
}

interface TurnstileApi {
  render: (element: HTMLElement, options: Record<string, unknown>) => string
  remove: (id: string) => void
}

declare global {
  interface Window {
    turnstile?: TurnstileApi
  }
}

const SCRIPT_ID = 'cf-turnstile-script'
let loading: Promise<TurnstileApi> | null = null

function loadTurnstile(): Promise<TurnstileApi> {
  if (window.turnstile) return Promise.resolve(window.turnstile)
  if (loading) return loading

  loading = new Promise<TurnstileApi>((resolve, reject) => {
    const existing = document.getElementById(SCRIPT_ID) as HTMLScriptElement | null
    const script = existing ?? document.createElement('script')
    if (!existing) {
      script.id = SCRIPT_ID
      script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
      script.async = true
      script.defer = true
      document.head.appendChild(script)
    }
    script.addEventListener('load', () => {
      if (window.turnstile) resolve(window.turnstile)
      else reject(new Error('turnstile unavailable'))
    })
    script.addEventListener('error', () => reject(new Error('turnstile script failed')))
  })

  return loading
}

export interface TurnstileProps {
  onToken: (token: string) => void
  /** Change this value to force a fresh widget after a failed submit. */
  resetKey?: number
}

export function Turnstile({ onToken, resetKey = 0 }: TurnstileProps) {
  const container = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!SITE_KEY || !container.current) return
    let widgetId: string | null = null
    let cancelled = false

    void loadTurnstile()
      .then((api) => {
        if (cancelled || !container.current) return
        widgetId = api.render(container.current, {
          sitekey: SITE_KEY,
          callback: (token: string) => onToken(token),
          'expired-callback': () => onToken(''),
          'error-callback': () => onToken(''),
        })
      })
      .catch(() => onToken(''))

    return () => {
      cancelled = true
      if (widgetId && window.turnstile) window.turnstile.remove(widgetId)
    }
  }, [onToken, resetKey])

  if (!SITE_KEY) return null
  return <div ref={container} className="mt-1 min-h-[65px]" />
}
