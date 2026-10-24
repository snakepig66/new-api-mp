/**
 * Session storage and access-token refresh.
 *
 * Backend contract (dashboard auth):
 *  - POST /api/user/login returns { access_token, access_expires_at, session, user }
 *  - the refresh token lives in an HttpOnly cookie; POST /api/user/auth/refresh
 *    rotates it and returns a new access token
 *  - dashboard requests authenticate with "Authorization: Bearer <access_token>"
 *
 * The access token is short lived (15 minutes), so every request goes through
 * api.ts and gets one transparent refresh on 401.
 */

import { useSyncExternalStore } from 'react'

export interface AuthUser {
  id: number
  username: string
  display_name?: string
  role: number
  group?: string
  quota?: number | string
  used_quota?: number | string
  [key: string]: unknown
}

export interface AuthSession {
  accessToken: string
  /** Unix seconds, as returned by the backend. */
  accessExpiresAt: number
  sid: string
  user: AuthUser | null
}

export interface AuthBundlePayload {
  access_token: string
  access_expires_at?: number
  token_type?: string
  session?: { sid?: string }
  user?: AuthUser
}

const STORAGE_KEY = 'token-market.auth.v1'

let current: AuthSession | null = readStoredSession()
const listeners = new Set<() => void>()

function emit(): void {
  for (const listener of listeners) listener()
}

function readStoredSession(): AuthSession | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<AuthSession> | null
    if (!parsed || typeof parsed.accessToken !== 'string' || !parsed.accessToken) return null
    return {
      accessToken: parsed.accessToken,
      accessExpiresAt: typeof parsed.accessExpiresAt === 'number' ? parsed.accessExpiresAt : 0,
      sid: typeof parsed.sid === 'string' ? parsed.sid : '',
      user: (parsed.user as AuthUser | null | undefined) ?? null,
    }
  } catch {
    return null
  }
}

function persist(next: AuthSession | null): void {
  current = next
  if (typeof window !== 'undefined') {
    try {
      if (next) window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
      else window.localStorage.removeItem(STORAGE_KEY)
    } catch {
      // Storage can be unavailable (private mode, quota); keep the in-memory copy.
    }
  }
  emit()
}

export function getSession(): AuthSession | null {
  return current
}

/** Stable snapshot for useSyncExternalStore. */
export function getSessionSnapshot(): AuthSession | null {
  return current
}

export function subscribeSession(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useSession(): AuthSession | null {
  return useSyncExternalStore(subscribeSession, getSessionSnapshot, () => null)
}

export function isLoggedIn(): boolean {
  return Boolean(current?.accessToken)
}

/** Apply the bundle returned by login or refresh. */
export function applyAuthBundle(bundle: AuthBundlePayload): void {
  if (!bundle?.access_token) return
  persist({
    accessToken: bundle.access_token,
    accessExpiresAt:
      typeof bundle.access_expires_at === 'number' ? bundle.access_expires_at : 0,
    sid: bundle.session?.sid ?? current?.sid ?? '',
    user: bundle.user ?? current?.user ?? null,
  })
}

export function setUser(user: AuthUser): void {
  if (!current) return
  persist({ ...current, user })
}

export function clearSession(): void {
  if (!current) return
  persist(null)
}

/** Whether a refresh attempt is worth making. */
export function canRefresh(): boolean {
  return Boolean(current?.accessToken || current?.sid)
}

export function isAccessTokenFresh(skewSeconds = 30): boolean {
  if (!current?.accessToken) return false
  if (!current.accessExpiresAt) return true
  return current.accessExpiresAt - Math.floor(Date.now() / 1000) > skewSeconds
}

let refreshInflight: Promise<boolean> | null = null

/**
 * Exchange the HttpOnly refresh cookie for a new access token.
 * Never throws; returns false when the session cannot be renewed.
 */
export function refreshAccessToken(): Promise<boolean> {
  if (refreshInflight) return refreshInflight

  refreshInflight = (async () => {
    try {
      const headers: Record<string, string> = {}
      const sid = current?.sid
      if (sid) headers['X-Auth-Session'] = sid
      const response = await fetch('/api/user/auth/refresh', {
        method: 'POST',
        credentials: 'include',
        headers,
      })
      if (!response.ok) return false
      const payload = (await response.json()) as {
        success?: boolean
        data?: AuthBundlePayload
      } | null
      if (!payload?.success || !payload.data?.access_token) return false
      applyAuthBundle(payload.data)
      return true
    } catch {
      return false
    }
  })()

  return refreshInflight.finally(() => {
    refreshInflight = null
  })
}

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key !== STORAGE_KEY) return
    const next = readStoredSession()
    if (JSON.stringify(next) === JSON.stringify(current)) return
    current = next
    emit()
  })
}
