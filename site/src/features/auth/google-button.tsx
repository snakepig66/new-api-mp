import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import { startGoogleLogin, systemStatusQuery } from './api'

export function GoogleMark() {
  return (
    <svg width="15" height="15" viewBox="0 0 18 18" aria-hidden="true">
      <path
        fill="#4285F4"
        d="M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.48h4.84a4.14 4.14 0 0 1-1.8 2.72v2.26h2.92c1.7-1.57 2.68-3.88 2.68-6.62Z"
      />
      <path
        fill="#34A853"
        d="M9 18c2.43 0 4.47-.8 5.96-2.18l-2.92-2.26c-.8.54-1.84.86-3.04.86-2.34 0-4.32-1.58-5.03-3.7H.96v2.33A9 9 0 0 0 9 18Z"
      />
      <path
        fill="#FBBC05"
        d="M3.97 10.71a5.4 5.4 0 0 1 0-3.42V4.96H.96a9 9 0 0 0 0 8.08l3.01-2.33Z"
      />
      <path
        fill="#EA4335"
        d="M9 3.58c1.32 0 2.5.45 3.44 1.35l2.58-2.58C13.46.9 11.42 0 9 0A9 9 0 0 0 .96 4.96l3.01 2.33C4.68 5.16 6.66 3.58 9 3.58Z"
      />
    </svg>
  )
}

export interface GoogleButtonProps {
  redirectTo: string
  onError: (message: string) => void
  className?: string
}

/**
 * Google login via the generic custom-provider flow. The button stays visible
 * even when the provider is not configured so the option is discoverable, but
 * it is disabled with an explanation instead of failing after a click.
 */
export function GoogleButton({ redirectTo, onError, className }: GoogleButtonProps) {
  const status = useQuery(systemStatusQuery())
  const [starting, setStarting] = useState(false)
  const enabled = Boolean(
    status.data?.custom_oauth_providers?.some((p) => p.slug === 'google'),
  )
  const disabled = starting || status.isLoading || !enabled

  async function handleClick() {
    if (disabled) return
    setStarting(true)
    try {
      await startGoogleLogin(redirectTo)
    } catch (error) {
      setStarting(false)
      onError(error instanceof Error && error.message ? error.message : copy.pages.login.googleFailed)
    }
  }

  return (
    <div className={className}>
      <button
        type="button"
        onClick={() => void handleClick()}
        disabled={disabled}
        className={cn(
          'inline-flex h-12 w-full items-center justify-center gap-2 rounded-full border border-white/70 bg-white/70 px-5 text-body font-medium text-ink transition-colors',
          'hover:bg-brand/[0.06] disabled:cursor-not-allowed disabled:opacity-50',
        )}
      >
        <GoogleMark />
        {starting ? copy.pages.login.googleStarting : copy.pages.login.google}
      </button>
      {!status.isLoading && !enabled ? (
        <p className="mt-2 text-meta text-ink-3">{copy.pages.login.googleUnavailable}</p>
      ) : null}
    </div>
  )
}
