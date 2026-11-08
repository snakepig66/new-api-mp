/**
 * Route-level error helpers.
 *
 * A deploy replaces the hashed chunks under /assets. An already-open tab that
 * navigates afterwards asks for a chunk that no longer exists and the lazy
 * import rejects. The router error boundary then shows the normal error page,
 * which looks like a white screen to the user. These helpers recognise that
 * failure and allow exactly one automatic reload per path, so a genuinely
 * broken build cannot trap the tab in a reload loop.
 */

/** Messages emitted by browsers when a dynamic import chunk is unavailable. */
export const CHUNK_ERROR_PATTERNS = [
  'Failed to fetch dynamically imported module',
  'Importing a module script failed',
  'error loading dynamically imported module',
] as const

/** sessionStorage key prefix; the full key contains the current pathname. */
export const CHUNK_RELOAD_PREFIX = 'mp.chunk-reloaded.'

type ReloadStorage = Pick<Storage, 'getItem' | 'setItem'>

/** Best-effort message extraction from whatever the router boundary caught. */
export function errorMessage(error: unknown): string {
  if (typeof error === 'string') return error
  if (error && typeof error === 'object') {
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string' && message) return message
    const cause = (error as { cause?: unknown }).cause
    if (typeof cause === 'string') return cause
    if (cause && typeof cause === 'object') {
      const causeMessage = (cause as { message?: unknown }).message
      if (typeof causeMessage === 'string' && causeMessage) return causeMessage
    }
  }
  return ''
}

/** True when the error is a missing/failed lazy chunk rather than app logic. */
export function isChunkLoadError(error: unknown): boolean {
  let current: unknown = error
  // Browser error boundaries sometimes wrap the import failure in `cause`.
  for (let depth = 0; current && depth < 4; depth += 1) {
    const message = errorMessage(current)
    if (message && CHUNK_ERROR_PATTERNS.some((pattern) => message.includes(pattern))) {
      return true
    }
    current = typeof current === 'object' ? (current as { cause?: unknown }).cause : undefined
  }
  return false
}

/** Storage key remembering that this path already forced one reload. */
export function chunkReloadKey(pathname: string): string {
  return `${CHUNK_RELOAD_PREFIX}${pathname || '/'}`
}

/** Whether an automatic reload was already attempted for this path. */
export function hasChunkReloadMarker(pathname: string, storage: ReloadStorage): boolean {
  return storage.getItem(chunkReloadKey(pathname)) !== null
}

/**
 * Decide whether to reload automatically.
 *
 * Returns true at most once per path: the marker is written before the caller
 * reloads. A second chunk error after the reload returns false, so the error
 * page (with a manual refresh button) is shown instead of a reload loop.
 */
export function shouldAutoReload(
  error: unknown,
  pathname: string,
  storage: ReloadStorage,
): boolean {
  if (!isChunkLoadError(error)) return false
  const key = chunkReloadKey(pathname)
  if (storage.getItem(key) !== null) return false
  storage.setItem(key, '1')
  return true
}
