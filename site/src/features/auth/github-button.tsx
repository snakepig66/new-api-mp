import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import { startGithubLogin, systemStatusQuery } from './api'

function GithubMark() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8a8 8 0 0 0 5.47 7.59c.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.5-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.4 7.4 0 0 1 2-.27c.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  )
}

export interface GithubButtonProps {
  redirectTo: string
  onError: (message: string) => void
  className?: string
}

/**
 * Starts the GitHub OAuth handshake. The button stays visible even when the
 * provider is not configured so the option is discoverable, but it is
 * disabled with an explanation instead of failing after a click.
 */
export function GithubButton({ redirectTo, onError, className }: GithubButtonProps) {
  const status = useQuery(systemStatusQuery())
  const [starting, setStarting] = useState(false)
  const enabled = status.data?.github_oauth === true
  const disabled = starting || status.isLoading || !enabled

  async function handleClick() {
    if (disabled) return
    setStarting(true)
    try {
      await startGithubLogin(redirectTo)
    } catch (error) {
      setStarting(false)
      onError(error instanceof Error && error.message ? error.message : copy.pages.login.githubFailed)
    }
  }

  return (
    <div className={className}>
      <button
        type="button"
        onClick={() => void handleClick()}
        disabled={disabled}
        className={cn(
          'inline-flex h-12 w-full items-center justify-center gap-2 rounded-full border border-line-strong bg-bg px-5 text-[15px] font-medium text-ink transition-colors',
          'hover:bg-bg-soft disabled:cursor-not-allowed disabled:opacity-50',
        )}
      >
        <GithubMark />
        {starting ? copy.pages.login.githubStarting : copy.pages.login.github}
      </button>
      {!status.isLoading && !enabled ? (
        <p className="mt-2 text-[13px] text-ink-3">{copy.pages.login.githubUnavailable}</p>
      ) : null}
    </div>
  )
}
