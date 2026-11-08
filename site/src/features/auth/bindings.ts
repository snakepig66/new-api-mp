/**
 * Third-party account binding for the console account page.
 *
 * Binding is a step-up operation on the backend:
 *  1. POST /api/verify with the current password issues a one-use proof bound to
 *     `account.binding.bind` + { provider }.
 *  2. POST /api/oauth/state { intent: "bind" } consumes that proof through the
 *     X-Security-Proof header and returns a flow token; the browser then visits
 *     the provider.
 *  3. The /oauth/$provider callback finishes the bind and answers
 *     { action: "bind" } instead of an access bundle.
 *
 * Unbinding follows the same pattern with `account.binding.unbind` and
 * DELETE /api/user/oauth/bindings/:provider_id.
 */

import { copy } from '@/copy'
import { ApiError, api } from '@/lib/api'

import { rememberOAuthRedirect, type SystemStatus } from './api'

const page = copy.pages.consoleAccount.bindings

const BIND_KEY = 'token-market.oauth.bind'

export const BIND_SCOPE = 'account.binding.bind'
export const UNBIND_SCOPE = 'account.binding.unbind'

export type BindableProvider = 'github' | 'google'

export interface OAuthBinding {
  provider_id: number
  provider_name: string
  provider_slug: string
  provider_icon?: string
  provider_user_id: string
}

interface VerificationMethodOption {
  method: string
  available: boolean
  reason?: string
}

interface VerificationRequirements {
  scope: string
  methods: VerificationMethodOption[]
  password_encryption_enabled?: boolean
}

interface SecurityProof {
  proof_token: string
}

interface PendingBind {
  provider: BindableProvider
  state: string
}

export async function fetchOAuthBindings(): Promise<OAuthBinding[]> {
  const bindings = await api.get<OAuthBinding[]>('/user/oauth/bindings')
  return Array.isArray(bindings) ? bindings : []
}

/** Remember which flow token started a bind so the callback can tell it apart. */
function rememberOAuthBind(provider: BindableProvider, state: string): void {
  if (typeof window === 'undefined') return
  try {
    const pending: PendingBind = { provider, state }
    window.sessionStorage.setItem(BIND_KEY, JSON.stringify(pending))
  } catch {
    // Session storage can be unavailable; the callback then treats the request
    // as a login and reports the failure instead of silently logging in.
  }
}

/** Consume the pending bind marker; only matches the flow that started it. */
export function takeOAuthBindCallback(state: string): BindableProvider | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.sessionStorage.getItem(BIND_KEY)
    window.sessionStorage.removeItem(BIND_KEY)
    if (!raw) return null
    const pending = JSON.parse(raw) as PendingBind
    if (!pending?.provider || pending.state !== state) return null
    return pending.provider
  } catch {
    return null
  }
}

async function requestProof(
  password: string,
  scope: string,
  context: Record<string, unknown>,
): Promise<string> {
  const requirements = await api.get<VerificationRequirements>('/verify/methods', {
    query: { scope },
  })
  if (requirements.password_encryption_enabled) {
    throw new ApiError(400, 'PASSWORD_ENCRYPTION_UNSUPPORTED', page.encryptionUnsupported)
  }
  const passwordMethod = requirements.methods?.find((method) => method.method === 'password')
  if (!passwordMethod?.available) {
    throw new ApiError(
      400,
      'PASSWORD_VERIFY_UNAVAILABLE',
      passwordMethod?.reason ?? page.verifyUnavailable,
    )
  }
  const proof = await api.post<SecurityProof>('/verify', {
    method: 'password',
    scope,
    context,
    password,
  })
  if (!proof?.proof_token) {
    throw new ApiError(400, undefined, page.failed)
  }
  return proof.proof_token
}

async function startBind(provider: BindableProvider, password: string): Promise<void> {
  const status = await api.get<SystemStatus>('/status')
  const providerInfo = status.custom_oauth_providers?.find((item) => item.slug === 'google')
  if (provider === 'github' ? !status.github_oauth || !status.github_client_id : !providerInfo) {
    throw new ApiError(400, undefined, page.unavailable)
  }

  const proof = await requestProof(password, BIND_SCOPE, { provider })
  const flow = await api.post<{ flow_token: string }>(
    '/oauth/state',
    { provider, intent: 'bind' },
    { headers: { 'X-Security-Proof': proof } },
  )
  if (!flow?.flow_token) {
    throw new ApiError(200, undefined, page.failed)
  }

  rememberOAuthBind(provider, flow.flow_token)
  rememberOAuthRedirect('/console/account')

  if (provider === 'github') {
    const url = new URL('https://github.com/login/oauth/authorize')
    url.searchParams.set('client_id', status.github_client_id as string)
    url.searchParams.set('redirect_uri', `${window.location.origin}/oauth/github`)
    url.searchParams.set('state', flow.flow_token)
    url.searchParams.set('scope', 'user:email')
    window.location.assign(url.toString())
    return
  }

  const url = new URL(providerInfo!.authorization_endpoint)
  url.searchParams.set('client_id', providerInfo!.client_id)
  url.searchParams.set('redirect_uri', `${window.location.origin}/oauth/google`)
  url.searchParams.set('response_type', 'code')
  url.searchParams.set('scope', providerInfo!.scopes || 'openid email profile')
  url.searchParams.set('state', flow.flow_token)
  window.location.assign(url.toString())
}

export const startGithubBind = (password: string) => startBind('github', password)
export const startGoogleBind = (password: string) => startBind('google', password)

/** Finish a bind callback. The backend answers { action: "bind" }, no bundle. */
export async function completeOAuthBind(
  provider: string,
  code: string,
  state: string,
): Promise<void> {
  const result = await api.get<{ action?: string }>(
    `/oauth/${encodeURIComponent(provider)}`,
    { query: { code, state } },
  )
  if (result?.action !== 'bind') {
    throw new ApiError(200, undefined, page.failed)
  }
}

export async function unbindOAuthProvider(
  providerId: number,
  password: string,
): Promise<void> {
  const proof = await requestProof(password, UNBIND_SCOPE, { provider_id: providerId })
  await api.delete<unknown>(`/user/oauth/bindings/${providerId}`, {
    headers: { 'X-Security-Proof': proof },
  })
}
