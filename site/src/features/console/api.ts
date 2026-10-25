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

import { ApiError, api } from '@/lib/api'

import type {
  ConsoleKey,
  CreateKeyInput,
  CreateKeyResult,
  KeyDetail,
  KeyModel,
  KeyPref,
  KeyPrefInput,
  PageInfo,
  TokenRecord,
  UsageRecord,
  UsageRecordDetail,
  UsageStats,
  WalletLedgerEntry,
  WalletSummary,
} from './types'

export type LedgerPage = PageInfo<WalletLedgerEntry>
export type UsagePage = PageInfo<UsageRecord>
export type TokenPage = PageInfo<TokenRecord>

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

export const walletLedgerQuery = (page: number, pageSize = 20) =>
  queryOptions({
    queryKey: ['console', 'wallet-ledger', page, pageSize],
    queryFn: () =>
      api.get<LedgerPage>('/marketplace/wallet/ledger', {
        query: { page, page_size: pageSize },
      }),
    staleTime: 20_000,
  })

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

/**
 * POST /token returns only {success:true}, so the new id has to be recovered
 * from the list. Snapshot the ids first, then pick the newest row that was not
 * there before (name is the tie-breaker).
 */
async function findCreatedToken(
  name: string,
  beforeIds: Set<number>,
): Promise<TokenRecord | null> {
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const page = await listTokens()
    const candidates = page.items.filter(
      (token) => token.name === name && !beforeIds.has(token.id),
    )
    if (candidates.length > 0) {
      return candidates.reduce((newest, token) => (token.id > newest.id ? token : newest))
    }
    await sleep(250)
  }
  return null
}

/**
 * Create a key, save its routing preference and add the selected models.
 * A model that fails to attach does not roll the key back: the result reports
 * per-model failures so the UI can explain what is missing.
 */
export async function createKey(input: CreateKeyInput): Promise<CreateKeyResult> {
  const before = await listTokens()
  const beforeIds = new Set(before.items.map((token) => token.id))

  const noCap = input.spend_cap_micro === null
  await api.post<unknown>('/token/', {
    name: input.name,
    remain_quota: input.spend_cap_micro ?? 0,
    unlimited_quota: noCap,
    expired_time: -1,
    model_limits_enabled: false,
    model_limits: '',
    group: '',
  })

  const token = await findCreatedToken(input.name, beforeIds)
  if (!token) {
    return { tokenId: null, prefSaved: false, addedModels: [], failures: [] }
  }

  let prefSaved = true
  try {
    await api.put<KeyPref>('/marketplace/key/pref', {
      token_id: token.id,
      strategy: input.strategy,
      price_cap_milli: 0,
      prefer_seller_ids: [],
      block_seller_ids: [],
      only_seller_id: 0,
      allow_fallback: false,
      rpm: 0,
      official_only: false,
    })
  } catch {
    prefSaved = false
  }

  const addedModels: string[] = []
  const failures: { model: string; message: string }[] = []
  for (const model of input.models) {
    try {
      await api.post<{ models: KeyModel[] }>('/marketplace/key/models', {
        token_id: token.id,
        model: model.model,
        price_cap_milli: model.price_cap_milli,
      })
      addedModels.push(model.model)
    } catch (error) {
      failures.push({
        model: model.model,
        message: error instanceof ApiError ? error.message : String(error),
      })
    }
  }

  return { tokenId: token.id, prefSaved, addedModels, failures }
}

export async function saveKeyPref(input: KeyPrefInput): Promise<KeyPref> {
  return api.put<KeyPref>('/marketplace/key/pref', input)
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
