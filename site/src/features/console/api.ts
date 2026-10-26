/**
 * Buyer console data access.
 *
 * All endpoints are same-origin and go through lib/api.ts:
 *  - upstream keys:   /token, /token/:id, /token/:id/key, /user/topup
 *  - marketplace:     /marketplace/key/*, /marketplace/records/usage*,
 *                     /marketplace/wallet*
 *
 * The upstream key list is the source of truth for key identity; marketplace
 * preferences hang off the same token id.
 */

import { queryOptions } from '@tanstack/react-query'

import { copy } from '@/copy'
import { ApiError, api } from '@/lib/api'

import type {
  ConsoleKey,
  CreateKeyInput,
  CreateKeyResult,
  EpayPayment,
  KeyDetail,
  KeyModel,
  KeyPref,
  KeyPrefInput,
  KeyTokenLimits,
  KeyTokenLimitsInput,
  PageInfo,
  TokenRecord,
  TopupInfo,
  TopUpRecord,
  UsageRecord,
  UsageRecordDetail,
  UsageStats,
  WalletLedgerEntry,
  WalletSummary,
  WalletTransaction,
  WalletTransactionKind,
} from './types'

export type LedgerPage = PageInfo<WalletLedgerEntry>
export type WalletTransactionPage = PageInfo<WalletTransaction>
export type UsagePage = PageInfo<UsageRecord>
export type TokenPage = PageInfo<TokenRecord>
export type TopUpPage = PageInfo<TopUpRecord>

async function listTokens(): Promise<TokenPage> {
  return api.get<TokenPage>('/token/', { query: { p: 1, page_size: 100 } })
}

async function fetchKeyPref(tokenId: number): Promise<KeyPref | null> {
  try {
    return await api.get<KeyPref>('/marketplace/key/pref', {
      query: { token_id: tokenId },
    })
  } catch (error) {
    // A token that never saved preferences is a normal 404, not a failure.
    if (error instanceof ApiError && error.status === 404) return null
    throw error
  }
}

async function fetchKeyModels(tokenId: number): Promise<KeyModel[]> {
  const payload = await api.get<{ models: KeyModel[] }>('/marketplace/key/models', {
    query: { token_id: tokenId },
  })
  return payload.models ?? []
}

/** Keys merged with their marketplace preference (models + strategy). */
export const consoleKeysQuery = () =>
  queryOptions({
    queryKey: ['console', 'keys'],
    queryFn: async (): Promise<ConsoleKey[]> => {
      const page = await listTokens()
      const prefs = await Promise.all(page.items.map((token) => fetchKeyPref(token.id)))
      return page.items.map((token, index) => ({ token, pref: prefs[index] }))
    },
    staleTime: 15_000,
  })

export const keyDetailQuery = (tokenId: number) =>
  queryOptions({
    queryKey: ['console', 'key', tokenId],
    queryFn: async (): Promise<KeyDetail> => {
      const [token, pref, models] = await Promise.all([
        api.get<TokenRecord>(`/token/${tokenId}`),
        fetchKeyPref(tokenId),
        fetchKeyModels(tokenId),
      ])
      return { token, pref, models }
    },
    staleTime: 10_000,
  })

export const usageStatsQuery = (days = 7) =>
  queryOptions({
    queryKey: ['console', 'usage-stats', days],
    queryFn: () => api.get<UsageStats>('/marketplace/records/usage/stats', { query: { days } }),
    staleTime: 60_000,
  })

export const usageRecordsQuery = (page: number, pageSize = 20) =>
  queryOptions({
    queryKey: ['console', 'usage-records', page, pageSize],
    queryFn: () =>
      api.get<UsagePage>('/marketplace/records/usage', {
        query: { page, page_size: pageSize },
      }),
    staleTime: 20_000,
  })

export const usageRecordQuery = (requestId: string) =>
  queryOptions({
    queryKey: ['console', 'usage-record', requestId],
    queryFn: () =>
      api.get<UsageRecordDetail>(
        `/marketplace/records/usage/${encodeURIComponent(requestId)}`,
      ),
    staleTime: 60_000,
  })

export const walletQuery = () =>
  queryOptions({
    queryKey: ['console', 'wallet'],
    queryFn: () => api.get<WalletSummary>('/marketplace/wallet'),
    staleTime: 30_000,
  })

/**
 * GET /user/ip — the caller's source IP as resolved by the trusted-proxy
 * configuration. The key forms offer it for one-click whitelisting; callers
 * hide the hint when the request fails.
 */
export const currentIPQuery = () =>
  queryOptions({
    queryKey: ['console', 'ip'],
    queryFn: () => api.get<{ ip: string }>('/user/ip'),
    staleTime: 5 * 60_000,
    retry: false,
  })

export const walletLedgerQuery = (page: number, pageSize = 20) =>
  queryOptions({
    queryKey: ['console', 'wallet-ledger', page, pageSize],
    queryFn: () =>
      api.get<LedgerPage>('/marketplace/wallet/ledger', {
        query: { page, page_size: pageSize },
      }),
    staleTime: 20_000,
  })

/**
 * Unified wallet stream: marketplace ledger rows and top-up orders merged,
 * newest first. kind=all|usage|topup selects both sources or one of them.
 */
export const walletTransactionsQuery = (
  page: number,
  kind: WalletTransactionKind | 'all' = 'all',
  pageSize = 20,
) =>
  queryOptions({
    queryKey: ['console', 'wallet-transactions', page, pageSize, kind],
    queryFn: () =>
      api.get<WalletTransactionPage>('/marketplace/wallet/transactions', {
        query: { p: page, page_size: pageSize, kind },
      }),
    staleTime: 20_000,
  })

/**
 * Create a key in one request: the backend writes the token, its routing
 * preference and the selected models in a single transaction and returns the
 * full key once. There is no "partial create" step in the client anymore.
 */
export async function createKey(input: CreateKeyInput): Promise<CreateKeyResult> {
  return api.post<CreateKeyResult>('/marketplace/key', input)
}

export async function saveKeyPref(input: KeyPrefInput): Promise<KeyPref> {
  return api.put<KeyPref>('/marketplace/key/pref', input)
}

/** PUT /marketplace/key/token — expiry and IP whitelist on the token row. */
export async function saveKeyTokenLimits(
  input: KeyTokenLimitsInput,
): Promise<KeyTokenLimits> {
  return api.put<KeyTokenLimits>('/marketplace/key/token', input)
}

export async function addKeyModel(
  tokenId: number,
  model: string,
  priceCapMilli: number,
): Promise<KeyModel[]> {
  const payload = await api.post<{ models: KeyModel[] }>('/marketplace/key/models', {
    token_id: tokenId,
    model,
    price_cap_milli: priceCapMilli,
  })
  return payload.models ?? []
}

export async function removeKeyModel(tokenId: number, model: string): Promise<void> {
  await api.delete<{ deleted: boolean }>('/marketplace/key/models', {
    body: { token_id: tokenId, model },
  })
}

export async function deleteKey(tokenId: number): Promise<void> {
  await api.delete<{ success: boolean }>(`/token/${tokenId}`)
}

export async function setTokenStatus(tokenId: number, status: number): Promise<void> {
  await api.put<{ success: boolean }>('/token/?status_only=1', {
    id: tokenId,
    status,
  })
}

/** POST /token/:id/key — returns the one-time full key for the copy button. */
export async function revealTokenKey(tokenId: number): Promise<string> {
  const payload = await api.post<{ key: string }>(`/token/${tokenId}/key`)
  return payload.key
}

/** POST /user/topup — redeem a code; resolves to the credited quota (微元). */
export async function redeemCode(code: string): Promise<number> {
  const quota = await api.post<number>('/user/topup', { key: code })
  return typeof quota === 'number' ? quota : 0
}

export const topupInfoQuery = () =>
  queryOptions({
    queryKey: ['console', 'topup-info'],
    queryFn: () => api.get<TopupInfo>('/user/topup/info'),
    staleTime: 5 * 60_000,
  })

/**
 * POST /user/amount — the authoritative cash amount for a top-up. The UI must
 * show this instead of multiplying locally: group ratios and discounts are
 * server-owned.
 */
export async function quoteTopupAmount(amount: number): Promise<number> {
  // paymentError answers HTTP 200 with {message:"error", data:"..."} instead
  // of the success envelope, so a bad amount would otherwise read as NaN.
  const quote = await api.post<string | { message?: string; data?: string }>(
    '/user/amount',
    { amount },
  )
  if (typeof quote === 'object' && quote?.message === 'error') {
    throw new ApiError(200, undefined, quote.data)
  }
  const value = Number(quote)
  if (!Number.isFinite(value) || value <= 0) {
    throw new ApiError(200, undefined, copy.pages.consoleWallet.recharge.quoteFailed)
  }
  return value
}

/**
 * POST /user/pay — the epay response is not an envelope: it carries the
 * gateway URL plus hidden form fields the browser must POST to it.
 */
export async function requestEpayTopup(
  amount: number,
  paymentMethod: string,
): Promise<EpayPayment> {
  return api.post<EpayPayment>('/user/pay', {
    amount,
    payment_method: paymentMethod,
  })
}

export const topUpRecordsQuery = (page = 1, pageSize = 10) =>
  queryOptions({
    queryKey: ['console', 'topup-records', page, pageSize],
    queryFn: () => api.get<TopUpPage>('/user/topup/self', { query: { p: page, page_size: pageSize } }),
    staleTime: 15_000,
  })

/** One order by trade number, used by the post-payment confirmation poll. */
export async function fetchTopUpRecord(tradeNo: string): Promise<TopUpRecord | null> {
  const page = await api.get<TopUpPage>('/user/topup/self', {
    query: { p: 1, page_size: 20, keyword: tradeNo },
  })
  return page.items.find((item) => item.trade_no === tradeNo) ?? null
}
