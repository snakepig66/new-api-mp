import path from 'node:path'

/**
 * Shared E2E constants and HTTP helpers. The fixtures talk to the Go backend
 * directly (not through the Vite proxy) so they also work when the suite is
 * pointed at a deployed instance with E2E_BASE_URL.
 */

export const LOCAL = process.env.E2E_LOCAL === '1'
export const BACKEND = (process.env.E2E_BACKEND_URL ?? 'http://127.0.0.1:3100').replace(/\/+$/, '')
export const PASSWORD = process.env.E2E_PASS ?? ''
export const DB_PATH = process.env.E2E_DB_PATH ?? ''

export const SELLER_REAL_NAME = 'Test User'
export const SELLER_ID_CARD = '110101********1234'
export const SELLER_ALIPAY = 'e2e-payout@example.com'

export function uniqueName(prefix: string): string {
  const stamp = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  // users.username max length is 20 characters.
  return `${prefix}-${stamp}`.slice(0, 20)
}

export interface ApiEnvelope<T> {
  success: boolean
  message?: string
  data?: T
}

async function request<T>(
  method: string,
  apiPath: string,
  options: { token?: string; body?: unknown } = {},
): Promise<ApiEnvelope<T>> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (options.token) headers.Authorization = `Bearer ${options.token}`
  const response = await fetch(`${BACKEND}${apiPath}`, {
    method,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  })
  const text = await response.text()
  let parsed: ApiEnvelope<T> | null = null
  try {
    parsed = text ? (JSON.parse(text) as ApiEnvelope<T>) : null
  } catch {
    parsed = null
  }
  if (!parsed) {
    throw new Error(`${method} ${apiPath} returned ${response.status}: ${text.slice(0, 200)}`)
  }
  return parsed
}

export async function registerAccount(username: string): Promise<void> {
  const result = await request('POST', '/api/user/register', {
    body: {
      username,
      password: PASSWORD,
      email: `${username}@example.com`,
    },
  })
  if (!result.success) {
    // Idempotent accounts: an existing username is fine as long as login works.
    const login = await tryLogin(username)
    if (!login) {
      throw new Error(`register ${username} failed: ${result.message ?? 'unknown error'}`)
    }
  }
}

export async function tryLogin(username: string): Promise<string | null> {
  const bundle = await tryLoginBundle(username)
  return bundle?.accessToken ?? null
}

export interface LoginBundle {
  accessToken: string
  accessExpiresAt: number
  sid: string
  user: Record<string, unknown> | null
}

const SESSION_KEY = 'token-market.auth.v1'
const bundleCache = new Map<string, LoginBundle>()

async function tryLoginBundle(username: string): Promise<LoginBundle | null> {
  const cached = bundleCache.get(username)
  if (cached) return cached
  const result = await request<{
    access_token?: string
    access_expires_at?: number
    session?: { sid?: string }
    user?: Record<string, unknown>
  }>('POST', '/api/user/login', { body: { username, password: PASSWORD } })
  if (!result.success || !result.data?.access_token) return null
  const bundle: LoginBundle = {
    accessToken: result.data.access_token,
    accessExpiresAt: result.data.access_expires_at ?? 0,
    sid: result.data.session?.sid ?? '',
    user: result.data.user ?? null,
  }
  bundleCache.set(username, bundle)
  return bundle
}

/**
 * Injects the API-issued access token into the site's localStorage so the
 * browser context starts authenticated without a second login request (the
 * backend rate-limits login attempts). Call this before the next goto.
 */
export async function authenticatePage(
  page: import('@playwright/test').Page,
  username: string,
): Promise<void> {
  const bundle = await tryLoginBundle(username)
  if (!bundle) throw new Error(`login ${username} failed`)
  const value = JSON.stringify(bundle)
  await page.addInitScript(
    ({ key, raw }) => {
      window.localStorage.setItem(key, raw)
    },
    { key: SESSION_KEY, raw: value },
  )
}

export async function login(username: string): Promise<string> {
  const token = await tryLogin(username)
  if (!token) throw new Error(`login ${username} failed`)
  return token
}

export async function ensureAccount(username: string): Promise<string> {
  await registerAccount(username)
  return login(username)
}

/** 协议 → 实名 → 支付宝. Every call is idempotent at the API level. */
export async function onboardSeller(token: string): Promise<void> {
  const steps: Record<string, unknown>[] = [
    { agreement_version: 'v1' },
    { real_name: SELLER_REAL_NAME, id_card_masked: SELLER_ID_CARD },
    { alipay_account: SELLER_ALIPAY },
  ]
  for (const body of steps) {
    const result = await request('POST', '/api/marketplace/seller/onboarding', { token, body })
    if (!result.success) {
      throw new Error(`seller onboarding failed: ${result.message ?? 'unknown error'}`)
    }
  }
}

export async function createListing(
  token: string,
  apiKey: string,
  models: string[] | string = ['deepseek-flash'],
  multiplierMilli = 900,
): Promise<number> {
  const result = await request<{ id: number; status: string }>(
    'POST',
    '/api/marketplace/seller/listings',
    {
      token,
      body: {
        vendor_id: 'deepseek',
        api_key: apiKey,
        models: Array.isArray(models) ? models : [models],
        multiplier_milli: multiplierMilli,
        concurrency: 5,
      },
    },
  )
  if (!result.success || !result.data?.id) {
    throw new Error(`create listing failed: ${result.message ?? 'unknown error'}`)
  }
  return result.data.id
}

export interface TokenRow {
  id: number
  key: string
  name: string
  status: number
}

export async function listTokens(token: string): Promise<TokenRow[]> {
  const result = await request<{ items: TokenRow[] }>('GET', '/api/token/?p=1&size=50', { token })
  return result.data?.items ?? []
}

export async function fullTokenKey(token: string, tokenId: number): Promise<string> {
  const result = await request<{ key?: string }>('POST', `/api/token/${tokenId}/key`, {
    token,
    body: {},
  })
  if (!result.success || !result.data?.key) {
    throw new Error(`could not read the full key of token ${tokenId}`)
  }
  return result.data.key
}

export async function signInThroughUi(page: import('@playwright/test').Page, username: string) {
  await page.goto('/login')
  await page.getByLabel('邮箱或用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill(PASSWORD)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForURL(/\/console(\/|$)/, { timeout: 30_000 })
}
