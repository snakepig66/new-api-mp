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
  smtp_configured?: boolean
  turnstile_check?: boolean
  turnstile_site_key?: string
  custom_oauth_providers?: CustomOAuthProviderInfo[]
  /** 系统公告（后台 console_setting 开启时返回，controller/misc.go）。 */
  announcements_enabled?: boolean
  announcements?: AnnouncementInfo[]
}

/** One entry of status.announcements (setting/console_setting/validation.go). */
export interface AnnouncementInfo {
  id?: number | string
  title?: string
  content?: string
  publishDate?: string
  type?: string
  link?: string
}

export interface CustomOAuthProviderInfo {
  id: number
  name: string
  slug: string
  icon?: string
  client_id: string
  authorization_endpoint: string
  scopes: string
}

/** One verification method offered by the login challenge (service/login_verification.go). */
export interface LoginChallengeMethod {
  method: '2fa' | 'passkey'
  available: boolean
  reason?: string
}

/**
 * POST /api/user/login answers this instead of a bundle when the account has
 * 2FA / passkey login verification enabled (service.LoginChallenge). The
 * session is only issued after POST /api/user/login/verify succeeds.
 */
export interface LoginChallenge {
  require_verification: true
  flow_token: string
  expires_at: number
  methods: LoginChallengeMethod[]
}

export function isLoginChallenge(value: unknown): value is LoginChallenge {
  if (!value || typeof value !== 'object') return false
  const it = value as Partial<LoginChallenge>
  return (
    it.require_verification === true &&
    typeof it.flow_token === 'string' &&
    it.flow_token.length > 0 &&
    typeof it.expires_at === 'number' &&
    Array.isArray(it.methods)
  )
}

export type LoginOutcome =
  | { kind: 'session'; user: AuthUser | null }
  | { kind: 'challenge'; challenge: LoginChallenge }

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
): Promise<LoginOutcome> {
  const payload = await api.post<AuthBundlePayload | LoginChallenge>(
    '/user/login',
    { username: identifier, password },
    { query: turnstile ? { turnstile } : undefined },
  )
  if (isLoginChallenge(payload)) {
    return { kind: 'challenge', challenge: payload }
  }
  if (!payload?.access_token) {
    throw new ApiError(200, undefined, copy.pages.login.failed)
  }
  applyAuthBundle(payload)
  return { kind: 'session', user: payload.user ?? null }
}

/**
 * Second login step: exchange the challenge flow token plus the authenticator
 * code for the session (controller.VerifyLogin → POST /api/user/login/verify).
 * Only the 2FA code method is handled here; a passkey-only account is rejected
 * by the caller before this call.
 */
export async function verifyLoginCode(
  flowToken: string,
  code: string,
): Promise<AuthUser | null> {
  const bundle = await api.post<AuthBundlePayload>(
    '/user/login/verify',
    { flow_token: flowToken, method: '2fa', code },
    { skipAuthRetry: true },
  )
  if (!bundle?.access_token) {
    throw new ApiError(200, undefined, copy.pages.login.verifyFailed)
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
  // Pin the callback explicitly: a GitHub App can register several redirect
  // URIs and, without this, GitHub falls back to the first one (often the
  // bare site root), where no route handles the code.
  url.searchParams.set('redirect_uri', `${window.location.origin}/oauth/github`)
  url.searchParams.set('state', flow.flow_token)
  url.searchParams.set('scope', 'user:email')
  window.location.assign(url.toString())
}

/**
 * Google login start via the generic custom-provider flow: create the CSRF
 * state, then hand the browser to the provider's authorization endpoint. The
 * callback URL https://<origin>/oauth/google lands on the shared /oauth/$provider
 * route, which exchanges code+state for a login bundle.
 */
export async function startGoogleLogin(redirectTo: string): Promise<void> {
  const status = await api.get<SystemStatus>('/status')
  const provider = status.custom_oauth_providers?.find((p) => p.slug === 'google')
  if (!provider) {
    throw new ApiError(400, undefined, copy.pages.login.googleUnavailable)
  }
  const flow = await api.post<{ flow_token: string }>('/oauth/state', {
    provider: 'google',
    intent: 'login',
  })
  if (!flow?.flow_token) {
    throw new ApiError(200, undefined, copy.pages.login.googleFailed)
  }
  rememberOAuthRedirect(redirectTo)
  const url = new URL(provider.authorization_endpoint)
  url.searchParams.set('client_id', provider.client_id)
  url.searchParams.set('redirect_uri', `${window.location.origin}/oauth/google`)
  url.searchParams.set('response_type', 'code')
  url.searchParams.set('scope', provider.scopes || 'openid email profile')
  url.searchParams.set('state', flow.flow_token)
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
