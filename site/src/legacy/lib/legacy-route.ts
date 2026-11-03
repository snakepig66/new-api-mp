/**
 * Search/navigation helpers for the ported channel management page.
 *
 * The original feature reads and writes URL search params through the new-api
 * route API. In the site the page always lives at `/console/ops/channels`; the
 * helpers patch the URL through the History API so the ported table also works
 * as a plain component tree (unit tests render it without a router).
 */
import { useNavigate } from '@tanstack/react-router'
import { useMemo, useSyncExternalStore } from 'react'

export type SearchRecord = Record<string, unknown>

export type LegacyNavigateOptions = {
  search?:
    | true
    | SearchRecord
    | ((prev: SearchRecord) => Partial<SearchRecord> | SearchRecord)
  replace?: boolean
}

export type LegacyNavigateFn = (opts: LegacyNavigateOptions) => void

const CHANNELS_PATH = '/console/ops/channels'

const listeners = new Set<() => void>()

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  if (typeof window !== 'undefined') {
    window.addEventListener('popstate', listener)
  }
  return () => {
    listeners.delete(listener)
    if (typeof window !== 'undefined') {
      window.removeEventListener('popstate', listener)
    }
  }
}

function getSnapshot(): string {
  return typeof window === 'undefined' ? '' : window.location.search
}

function parseSearch(raw: string): SearchRecord {
  return Object.fromEntries(new URLSearchParams(raw).entries()) as SearchRecord
}

function resolveSearch(
  search: NonNullable<LegacyNavigateOptions['search']>,
  prev: SearchRecord
): SearchRecord {
  if (search === true) return prev
  if (typeof search === 'function') return search(prev) as SearchRecord
  return search
}

function buildQuery(next: SearchRecord): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(next)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      for (const item of value) params.append(key, String(item))
    } else {
      params.set(key, String(value))
    }
  }
  return params.toString()
}

/** Current channel-page search params, re-rendering on URL changes. */
export function useLegacyChannelsSearch(): SearchRecord {
  const raw = useSyncExternalStore(subscribe, getSnapshot, () => '')
  return useMemo(() => parseSearch(raw), [raw])
}

/**
 * Patch channel-page search params. The router's own navigate keeps the route
 * in sync when the page is mounted under the app router; otherwise this falls
 * back to the History API.
 */
export function useLegacyChannelsNavigate(): LegacyNavigateFn {
  const navigate = useNavigate({ from: CHANNELS_PATH })
  return (opts) => {
    if (typeof window === 'undefined') return
    const prev = parseSearch(window.location.search)
    const next =
      opts.search === undefined ? prev : resolveSearch(opts.search, prev)
    const query = buildQuery(next)
    const url = query ? `${CHANNELS_PATH}?${query}` : CHANNELS_PATH

    window.history.replaceState(null, '', url)
    for (const listener of listeners) listener()
    void navigate({
      to: CHANNELS_PATH,
      search: next,
      replace: opts.replace,
    })
  }
}
