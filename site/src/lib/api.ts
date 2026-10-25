/**
 * Fetch wrapper for the Go backend.
 *
 * Responsibilities:
 *  - prefix /api, JSON in / JSON out, credentials included
 *  - unwrap the { success, message, data } envelope and the OpenAI-style
 *    { error: { message, type, code } } envelope
 *  - one transparent token refresh on 401, then redirect to /login
 *  - stable ApiError with an error code, so the UI can map Chinese copy
 *
 * Usage:
 *   const models = await api.get<SquareModel[]>('/marketplace/square/models')
 *   await api.post('/user/login', { username, password })
 */

import { copy } from '@/copy'

import { canRefresh, clearSession, getSession, refreshAccessToken } from './session'

export type ApiQuery = Record<string, string | number | boolean | null | undefined>

export interface ApiRequestInit extends Omit<RequestInit, 'body' | 'headers'> {
  /** Serialized as JSON unless it is already BodyInit (string, FormData, ...). */
  body?: unknown
  headers?: Record<string, string>
  query?: ApiQuery
  /** Do not try to refresh the access token on 401. */
  skipAuthRetry?: boolean
  /** Clear the session on 401 but let the caller handle navigation. */
  skipUnauthorizedHandler?: boolean
}

export interface UnauthorizedInfo {
  path: string
  redirectTo: string
}

export type UnauthorizedHandler = (info: UnauthorizedInfo) => void

const API_PREFIX = '/api'
const AUTH_ENDPOINTS = new Set([
  '/api/user/login',
  '/api/user/login/2fa',
  '/api/user/login/verify',
  '/api/user/register',
  '/api/user/auth/refresh',
])

const errorCopy = copy.errors as Record<string, string | undefined>

function localizedMessage(code: string | undefined, fallback: string | undefined): string {
  if (code && errorCopy[code]) return errorCopy[code]
  return fallback?.trim() || copy.errors.UNKNOWN
}

export class ApiError extends Error {
  readonly status: number
  readonly code?: string
  /** Raw error envelope, e.g. {listing_id} on a duplicate-key response. */
  readonly details?: Record<string, unknown>

  constructor(
    status: number,
    code?: string,
    fallbackMessage?: string,
    details?: unknown,
  ) {
    super(localizedMessage(code, fallbackMessage))
    this.name = 'ApiError'
    this.status = status
    this.code = code
    if (details && typeof details === 'object') {
      this.details = details as Record<string, unknown>
    }
  }
}

let customUnauthorizedHandler: UnauthorizedHandler | null = null

/**
 * Replace the 401 navigation behaviour (tests, or a router-aware handler).
 * Returns the previous handler so callers can restore it.
 */
export function setUnauthorizedHandler(
  next: UnauthorizedHandler | null,
): UnauthorizedHandler | null {
  const previous = customUnauthorizedHandler
  customUnauthorizedHandler = next
  return previous
}

function loginRedirectTarget(): string {
  if (typeof window === 'undefined') return '/login'
  const { pathname, search, hash } = window.location
  if (pathname === '/login' || pathname.startsWith('/login/')) return `${pathname}${search}${hash}`
  return `/login?redirect=${encodeURIComponent(`${pathname}${search}${hash}`)}`
}

function handleUnauthorized(path: string, skipNavigation: boolean): void {
  clearSession()
  const redirectTo = loginRedirectTarget()
  if (skipNavigation) return
  if (customUnauthorizedHandler) {
    customUnauthorizedHandler({ path, redirectTo })
    return
  }
  if (typeof window !== 'undefined' && !window.location.pathname.startsWith('/login')) {
    window.location.assign(redirectTo)
  }
}

function resolveApiPath(path: string): string {
  if (path === API_PREFIX || path.startsWith(`${API_PREFIX}/`)) return path
  return `${API_PREFIX}${path.startsWith('/') ? '' : '/'}${path}`
}

function buildUrl(path: string, query?: ApiQuery): string {
  const url = resolveApiPath(path)

  if (!query) return url
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null) continue
    params.set(key, String(value))
  }
  const search = params.toString()
  if (!search) return url
  return `${url}${url.includes('?') ? '&' : '?'}${search}`
}

function prepareBody(body: unknown, headers: Headers): BodyInit | undefined {
  if (body === undefined) return undefined
  if (typeof body === 'string') return body
  if (typeof FormData !== 'undefined' && body instanceof FormData) return body
  if (typeof Blob !== 'undefined' && body instanceof Blob) return body
  if (typeof URLSearchParams !== 'undefined' && body instanceof URLSearchParams) return body
  headers.set('content-type', 'application/json')
  return JSON.stringify(body)
}

interface Envelope {
  success: boolean
  message?: string
  data?: unknown
  code?: string
}

function isEnvelope(payload: unknown): payload is Envelope {
  return Boolean(payload) && typeof payload === 'object' && typeof (payload as Envelope).success === 'boolean'
}

function pickString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined
}

function readErrorFields(payload: unknown): { code?: string; message?: string } {
  if (!payload || typeof payload !== 'object') return {}
  const record = payload as Record<string, unknown>
  const nested =
    record.error && typeof record.error === 'object'
      ? (record.error as Record<string, unknown>)
      : undefined
  return {
    code: pickString(record.code) ?? pickString(nested?.code) ?? pickString(nested?.type),
    message: pickString(record.message) ?? pickString(nested?.message),
  }
}

async function readPayload(response: Response): Promise<unknown> {
  const text = await response.text()
  if (!text) return null
  try {
    return JSON.parse(text) as unknown
  } catch {
    return text
  }
}

function requestTimeoutSignal(): AbortSignal | undefined {
  if (typeof AbortSignal !== 'undefined' && typeof AbortSignal.timeout === 'function') {
    return AbortSignal.timeout(30_000)
  }
  return undefined
}

async function request<T>(method: string, path: string, init: ApiRequestInit = {}): Promise<T> {
  const url = buildUrl(path, init.query)
  const isAuthEndpoint = AUTH_ENDPOINTS.has(resolveApiPath(path))
  let retriedAfterRefresh = false

  for (;;) {
    const headers = new Headers(init.headers)
    const token = getSession()?.accessToken
    if (token) headers.set('authorization', `Bearer ${token}`)
    const body = prepareBody(init.body, headers)

    let response: Response
    try {
      response = await fetch(url, {
        method,
        headers,
        credentials: init.credentials ?? 'include',
        body,
        mode: init.mode,
        cache: init.cache,
        redirect: init.redirect,
        referrer: init.referrer,
        referrerPolicy: init.referrerPolicy,
        integrity: init.integrity,
        keepalive: init.keepalive,
        signal: init.signal ?? requestTimeoutSignal(),
      })
    } catch (error) {
      const aborted =
        error instanceof DOMException &&
        (error.name === 'AbortError' || error.name === 'TimeoutError')
      throw new ApiError(0, aborted ? 'TIMEOUT' : 'NETWORK_ERROR')
    }

    const payload = await readPayload(response)

    if (response.status === 401 && !isAuthEndpoint) {
      if (!retriedAfterRefresh && !init.skipAuthRetry && canRefresh()) {
        retriedAfterRefresh = true
        if (await refreshAccessToken()) continue
      }
      const { code, message } = readErrorFields(payload)
      handleUnauthorized(path, Boolean(init.skipUnauthorizedHandler))
      throw new ApiError(response.status, code, message, payload)
    }

    if (!response.ok) {
      const { code, message } = readErrorFields(payload)
      throw new ApiError(response.status, code, message, payload)
    }

    if (isEnvelope(payload) && payload.success === false) {
      const { code, message } = readErrorFields(payload)
      throw new ApiError(response.status, code, message, payload)
    }

    return (isEnvelope(payload) ? payload.data : payload) as T
  }
}

export const api = {
  get<T>(path: string, init?: ApiRequestInit): Promise<T> {
    return request<T>('GET', path, init)
  },
  post<T>(path: string, body?: unknown, init?: ApiRequestInit): Promise<T> {
    return request<T>('POST', path, { ...init, body })
  },
  put<T>(path: string, body?: unknown, init?: ApiRequestInit): Promise<T> {
    return request<T>('PUT', path, { ...init, body })
  },
  patch<T>(path: string, body?: unknown, init?: ApiRequestInit): Promise<T> {
    return request<T>('PATCH', path, { ...init, body })
  },
  delete<T>(path: string, init?: ApiRequestInit): Promise<T> {
    return request<T>('DELETE', path, init)
  },
}
