/**
 * Login state for the marketplace frontend.
 *
 * Reads the current user from session storage and validates it against
 * GET /api/user/self. A stale access token is renewed once through the
 * HttpOnly refresh cookie before the user is considered signed out.
 */

import { copy } from '@/copy'

import { ApiError, api } from './api'
import {
  applyAuthBundle,
  clearSession,
  getSession,
  isLoggedIn,
  refreshAccessToken,
  setUser,
  useSession,
  type AuthBundlePayload,
  type AuthSession,
  type AuthUser,
} from './session'

export type { AuthSession, AuthUser }
export { getSession, isLoggedIn }

export const ROLE_ADMIN_USER = 10
export const ROLE_ROOT_USER = 100

export async function login(username: string, password: string): Promise<AuthUser> {
  const bundle = await api.post<AuthBundlePayload>('/user/login', { username, password })
  if (!bundle?.access_token) throw new ApiError(200, undefined, copy.errors.UNKNOWN)
  applyAuthBundle(bundle)
  if (bundle.user) return bundle.user
  return fetchSelf({ silent: true })
}

/**
 * Refresh the cached user from the backend. With `silent`, a 401 clears the
 * session but does not navigate — the caller decides where to go.
 */
export async function fetchSelf(options: { silent?: boolean } = {}): Promise<AuthUser> {
  const user = await api.get<AuthUser>('/user/self', {
    skipUnauthorizedHandler: options.silent,
  })
  setUser(user)
  return user
}

export async function logout(): Promise<void> {
  const sid = getSession()?.sid
  try {
    await api.post<unknown>('/user/auth/logout', undefined, {
      headers: sid ? { 'X-Auth-Session': sid } : undefined,
      skipAuthRetry: true,
      skipUnauthorizedHandler: true,
    })
  } catch {
    // Local state is what matters; the refresh cookie is cleared server-side
    // whenever the call succeeds.
  } finally {
    clearSession()
  }
}

let ensureInflight: Promise<boolean> | null = null

/**
 * Resolve whether a usable session exists, refreshing the access token if
 * needed. Used by console/seller/admin route guards.
 */
export function ensureAuth(): Promise<boolean> {
  if (ensureInflight) return ensureInflight

  ensureInflight = (async () => {
    try {
      if (getSession()?.accessToken) {
        try {
          await fetchSelf({ silent: true })
          return true
        } catch (error) {
          if (!(error instanceof ApiError) || error.status !== 401) return false
        }
      }
      // Always worth one attempt: a cookie-only session (local storage cleared)
      // can still be renewed even when nothing is cached locally.
      if (!(await refreshAccessToken())) return false
      try {
        await fetchSelf({ silent: true })
        return true
      } catch {
        return false
      }
    } finally {
      ensureInflight = null
    }
  })()

  return ensureInflight
}

export function useAuth(): {
  session: AuthSession | null
  user: AuthUser | null
  isLoggedIn: boolean
  role: number
  isAdmin: boolean
  isRoot: boolean
} {
  const session = useSession()
  const role = session?.user?.role ?? 0
  return {
    session,
    user: session?.user ?? null,
    isLoggedIn: Boolean(session?.accessToken),
    role,
    isAdmin: role >= ROLE_ADMIN_USER,
    isRoot: role >= ROLE_ROOT_USER,
  }
}
