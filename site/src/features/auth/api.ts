/**
 * Authentication calls used by the public login / register pages.
 *
 * The upstream console contract:
 *  - GET  /api/status            public flags (GitHub login, Turnstile, email code)
 *  - POST /api/user/login        { username, password } → access bundle
 *  - POST /api/user/register     model.User subset; answers success only
 *  - POST /api/oauth/state       { provider, intent } → { flow_token }
 *  - GET  /api/oauth/:provider   OAuth callback; answers the login bundle
 *
 * Turnstile is verified from the `turnstile` query parameter (see
 * middleware/turnstile-check.go), not from the JSON body.
 */

import { queryOptions } from '@tanstack/react-query'

import { copy } from '@/copy'
import { ApiError, api } from '@/lib/api'
import { applyAuthBundle, type AuthBundlePayload, type AuthUser } from '@/lib/session'

export interface SystemStatus {
  github_oauth?: boolean
  github_client_id?: string
  email_verification?: boolean
  turnstile_check?: boolean
  turnstile_site_key?: string
}

export const systemStatusQuery = () =>
  queryOptions({
    queryKey: ['system', 'status'],
    queryFn: () => api.get<SystemStatus>('/status'),
    staleTime: 120_000,
  })

/** Login accepts either the username or the account email (model.User query). */
export async function loginUser(
  identifier: string,
  password: string,
  turnstile?: string,
): Promise<AuthUser | null> {
  const bundle = await api.post<AuthBundlePayload>(
    '/user/login',
    { username: identifier, password },
    { query: turnstile ? { turnstile } : undefined },
  )
  if (!bundle?.access_token) {
    throw new ApiError(200, undefined, copy.pages.login.failed)
  }
  applyAuthBundle(bundle)
  return bundle.user ?? null
}

export interface RegisterPayload {
  username: string
  email: string
  password: string
  verificationCode?: string
}

/**
 * Registration does not open a session upstream (it answers success only), so
 * the page sends the user to /login afterwards instead of trying to reuse a
 * single-use Turnstile token.
 */
export async function registerUser(
  payload: RegisterPayload,
  turnstile?: string,
): Promise<void> {
  await api.post<unknown>(
    '/user/register',
    {
      username: payload.username,
      email: payload.email,
      password: payload.password,
      verification_code: payload.verificationCode,
    },
    { query: turnstile ? { turnstile } : undefined },
  )
}

/** GET /api/verification?email=&turnstile= — sends the 6-digit email code. */
export async function sendEmailCode(email: string, turnstile?: string): Promise<void> {
  await api.get<unknown>('/verification', {
    query: { email, ...(turnstile ? { turnstile } : {}) },
  })
}

/**
 * GET /api/reset_password?email=&turnstile= — always answers success so the
 * form cannot be used to discover registered addresses.
 */
export async function requestPasswordReset(email: string, turnstile?: string): Promise<void> {
  await api.get<unknown>('/reset_password', {
    query: { email, ...(turnstile ? { turnstile } : {}) },
    skipAuthRetry: true,
  })
}

/**
 * POST /api/user/reset — consumes the emailed token and returns the freshly
 * generated password. The backend has no "choose your own password" variant
 * for this flow, so the page must show the returned value once.
 */
export async function resetPassword(email: string, token: string): Promise<string> {
  const password = await api.post<string>(
    '/user/reset',
    { email, token },
    { skipAuthRetry: true },
  )
  if (typeof password !== 'string' || !password) {
    throw new ApiError(200, undefined, copy.pages.resetPassword.failed)
  }
  return password
}

const OAUTH_REDIRECT_KEY = 'token-market.oauth.redirect'

export function rememberOAuthRedirect(path: string): void {
  if (typeof window === 'undefined') return
  try {
    window.sessionStorage.setItem(OAUTH_REDIRECT_KEY, path)
  } catch {
    // Session storage can be unavailable; the callback falls back to /console.
  }
}

export function takeOAuthRedirect(): string | null {
  if (typeof window === 'undefined') return null
  try {
    const value = window.sessionStorage.getItem(OAUTH_REDIRECT_KEY)
    window.sessionStorage.removeItem(OAUTH_REDIRECT_KEY)
    return value
  } catch {
    return null
  }
}

/**
 * GitHub login start: create the CSRF state, then hand the browser to GitHub.
 * The OAuth app's callback URL points at this site's /oauth/github route, which
 * exchanges code+state for a login bundle.
 */
export async function startGithubLogin(redirectTo: string): Promise<void> {
  const status = await api.get<SystemStatus>('/status')
  if (!status.github_oauth || !status.github_client_id) {
    throw new ApiError(400, undefined, copy.pages.login.githubUnavailable)
  }
  const flow = await api.post<{ flow_token: string }>('/oauth/state', {
    provider: 'github',
    intent: 'login',
  })
  if (!flow?.flow_token) {
    throw new ApiError(200, undefined, copy.pages.login.githubFailed)
  }
  rememberOAuthRedirect(redirectTo)
  const url = new URL('https://github.com/login/oauth/authorize')
  url.searchParams.set('client_id', status.github_client_id)
  url.searchParams.set('state', flow.flow_token)
  url.searchParams.set('scope', 'user:email')
  window.location.assign(url.toString())
}

export class OAuthChallengeRequired extends Error {
  constructor() {
    super(copy.pages.oauth.twoFactorReason)
    this.name = 'OAuthChallengeRequired'
  }
}

/** Exchange the provider callback's code+state for a session. */
export async function completeOAuthLogin(
  provider: string,
  code: string,
  state: string,
): Promise<AuthUser | null> {
  const bundle = await api.get<AuthBundlePayload & { challenge?: unknown }>(
    `/oauth/${encodeURIComponent(provider)}`,
    { query: { code, state } },
  )
  if (!bundle?.access_token) {
    if (bundle && typeof bundle === 'object' && 'challenge' in bundle) {
      throw new OAuthChallengeRequired()
    }
    throw new ApiError(200, undefined, copy.pages.oauth.failedReason)
  }
  applyAuthBundle(bundle)
  return bundle.user ?? null
}
