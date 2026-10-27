import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'
import { queryClient } from '@/lib/query-client'

import {
  batchListingModels,
  deleteListing,
  modelChangeErrors,
  normalizePayout,
  restoreListingModels,
  sellerChannelKind,
  sellerCommonAddressesQuery,
  sellerGroupsQuery,
  sellerListingHeadersQuery,
  sellerListingModelsQuery,
  sellerListingsHealthQuery,
  sellerMultiplierBoundsQuery,
  sellerOverviewQuery,
  setListingModelMultiplier,
  setListingModelState,
  setListingModelsState,
  submitListingHeaders,
  updateListingSettings,
  VERIFY_POLL_INTERVAL_MS,
  verifyingRefetchInterval,
  vendorName,
  vendorsQuery,
  type ModelChangeError,
  type MultiplierBounds,
  type ProbeResult,
  type SellerCommonAddress,
  type SellerHeadersSubmitResult,
  type SellerHeadersView,
  type SellerListingHealth,
  type SellerListingModelsResponse,
  type SellerModelChangeResult,
  type SellerModelChangeRow,
  type SellerModelsRestoreResult,
  type SellerOverview,
  type SellerVendor,
} from './api'

const deepseek: SellerVendor = {
  id: 'deepseek',
  display_name: 'DeepSeek',
  tier: 'official',
  protocols: {
    openai: 'https://api.deepseek.com',
    anthropic: 'https://api.deepseek.com/anthropic',
  },
  models: ['deepseek-flash', 'deepseek-v4-pro'],
  passthrough_body: true,
}

afterEach(() => {
  queryClient.clear()
  vi.unstubAllGlobals()
})

describe('normalizePayout', () => {
  it('accepts the Go-shaped payout keys (no json tags on the struct)', () => {
    expect(
      normalizePayout({
        ID: 7,
        SellerID: 10023,
        AmountMicro: 50_000_000,
        Status: 'pending',
        IdempotencyKey: 'key-1',
        ReviewerID: 0,
        ReviewedAt: null,
        CreatedAt: '2026-10-24T10:00:00+08:00',
        UpdatedAt: '2026-10-24T10:00:00+08:00',
      }),
    ).toEqual({
      id: 7,
      seller_id: 10023,
      amount_micro: 50_000_000,
      status: 'pending',
      idempotency_key: 'key-1',
      reviewer_id: 0,
      reviewed_at: null,
      created_at: '2026-10-24T10:00:00+08:00',
      updated_at: '2026-10-24T10:00:00+08:00',
    })
  })

  it('also accepts snake_case if the backend adds tags later', () => {
    const payout = normalizePayout({
      id: 9,
      amount_micro: '50000000',
      status: 'paid',
      created_at: 1_700_000_000,
    })
    expect(payout.id).toBe(9)
    expect(payout.amount_micro).toBe('50000000')
    expect(payout.status).toBe('paid')
    expect(payout.created_at).toBe('1700000000')
  })
})

describe('sellerChannelKind', () => {
  it('prefers kind and falls back to the legacy tier field', () => {
    expect(sellerChannelKind({ ...deepseek, kind: 'plan' })).toBe('plan')
    expect(sellerChannelKind(deepseek)).toBe('official')
    expect(sellerChannelKind({ ...deepseek, tier: 'third_party' })).toBe('third_party')
  })
})

describe('verifyingRefetchInterval', () => {
  it('polls while any row is verifying and stops otherwise', () => {
    expect(verifyingRefetchInterval(undefined)).toBe(false)
    expect(verifyingRefetchInterval([])).toBe(false)
    expect(
      verifyingRefetchInterval([{ status: 'live' }, { status: 'paused' }]),
    ).toBe(false)
    expect(verifyingRefetchInterval([{ status: 'live' }, { status: 'verifying' }])).toBe(
      VERIFY_POLL_INTERVAL_MS,
    )
    expect(VERIFY_POLL_INTERVAL_MS).toBe(3000)
  })
})

describe('vendorName', () => {
  it('resolves the display name from an explicit vendors list', () => {
    expect(vendorName('deepseek', [deepseek])).toBe('DeepSeek')
    expect(vendorName('unknown', [deepseek])).toBe('unknown')
  })

  it('reads the vendors query cache before falling back to the slug', () => {
    queryClient.setQueryData(vendorsQuery().queryKey, [deepseek])
    expect(vendorName('deepseek')).toBe('DeepSeek')
    expect(vendorName('acme-ai')).toBe('acme-ai')
  })
})

describe('P4 group API', () => {
  it('groups query uses the seller-scoped key', () => {
    expect(sellerGroupsQuery().queryKey).toEqual(['marketplace', 'seller', 'groups'])
  })

  it('per-model multiplier PUTs the encoded model path', async () => {
    const calls: [string, RequestInit | undefined][] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push([String(input), init])
        return new Response(JSON.stringify({ success: true, data: {} }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        })
      }),
    )

    await setListingModelMultiplier(7, 'kimi/k3', 450)
    expect(calls).toHaveLength(1)
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/7/models/kimi%2Fk3/multiplier')
    expect(calls[0][1]?.method).toBe('PUT')
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({ multiplier_milli: 450 })
  })
})

describe('P2 trust-pool shapes', () => {
  it('probe results carry the trust-pool recognition fields', () => {
    const probe: ProbeResult = {
      ok: true,
      models: ['deepseek-flash'],
      missing_models: [],
      latency_ms: 10,
      prompt_cache: false,
      balance_micro: null,
      key_valid: true,
      fail_reason: '',
      trust_pool: {
        matched: true,
        trust: 'trusted',
        kind: 'official',
        vendor_name: 'DeepSeek',
        endpoints: ['openai_chat'],
        badge_text: 'DeepSeek · 官方 · OpenAI · 免审核',
      },
    }
    expect(probe.trust_pool?.trust).toBe('trusted')
    expect(probe.trust_pool?.endpoints).toEqual(['openai_chat'])
  })

  it('common addresses use the seller-scoped query key', () => {
    const options = sellerCommonAddressesQuery()
    expect(options.queryKey).toEqual(['marketplace', 'seller', 'common-addresses'])
    const address: SellerCommonAddress = {
      host: 'api.deepseek.com',
      vendor_name: 'DeepSeek',
      kind: 'official',
      endpoints: ['openai_chat'],
    }
    expect(address.kind).toBe('official')
  })
})

// ---------------------------------------------------------------------------
// 模型与价格 / 倍率边界 / 请求头 / 概览 / 健康窗口 (api_seller.go)
// ---------------------------------------------------------------------------

/** Records fetch calls and answers every request with one canned JSON body. */
function stubFetch(status: number, body: unknown) {
  const calls: [string, RequestInit | undefined][] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push([String(input), init])
      return new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json' },
      })
    }),
  )
  return calls
}

/**
 * Invokes a queryFn directly. The seller queries ignore the react-query
 * context argument, so tests can drive them without a QueryClient.
 */
function invokeQuery<T>(options: { queryFn?: (context: never) => T | Promise<T> }): Promise<T> {
  const fn = options.queryFn
  if (!fn) throw new Error('queryFn is missing')
  return Promise.resolve(fn(undefined as never))
}

describe('seller console queries (models, bounds, headers, overview, health)', () => {
  it('listing models query unwraps the envelope from the per-listing models path', async () => {
    const data: SellerListingModelsResponse = {
      items: [
        {
          model: 'deepseek-flash',
          upstream_model: 'deepseek-chat',
          enabled: true,
          auto_disabled: false,
          disabled_reason: '',
          disabled_detail: '',
          disabled_upstream_status: 0,
          disabled_upstream_message: '',
          multiplier_milli: 600,
          effective_multiplier_milli: 660,
          pending_multiplier_milli: 0,
          coefficient_milli: 1100,
          origin_vendor: 'deepseek',
          multiplier_min_milli: 100,
          multiplier_max_milli: 1000,
          official_prices: {
            peak: {
              input_micro_per_mtok: 1_000,
              output_micro_per_mtok: 2_000,
              cache_read_micro_per_mtok: 100,
              cache_write_micro_per_mtok: 0,
            },
          },
          buyer_prices: {
            peak: {
              input_micro_per_mtok: 660,
              output_micro_per_mtok: 1_320,
              cache_read_micro_per_mtok: 66,
              cache_write_micro_per_mtok: 0,
            },
          },
        },
        {
          model: 'kimi/k3',
          upstream_model: 'moonshot-v3',
          enabled: false,
          auto_disabled: true,
          disabled_reason: 'upstream_error',
          disabled_detail: 'upstream returned 402',
          disabled_upstream_status: 402,
          disabled_upstream_message: 'insufficient balance',
          disabled_at: '2026-10-26T18:00:00+08:00',
          multiplier_milli: 600,
          effective_multiplier_milli: 600,
          pending_multiplier_milli: 800,
          pending_effective_at: '2026-10-27T21:00:00+08:00',
          coefficient_milli: 1000,
          origin_vendor: 'moonshot',
          multiplier_min_milli: 100,
          multiplier_max_milli: 1000,
          official_prices: {},
          buyer_prices: {},
        },
      ],
      platform_fee_percent: 7,
      tier: 'peak',
      tier_label: '高峰',
      multiplier_min_milli: 100,
      multiplier_max_milli: 1000,
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    const options = sellerListingModelsQuery(5)
    expect(options.queryKey).toEqual(['marketplace', 'seller', 'listing', 5, 'models'])
    await expect(invokeQuery(options)).resolves.toEqual(data)
    expect(calls).toHaveLength(1)
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/5/models')
    expect(calls[0][1]?.method ?? 'GET').toBe('GET')
  })

  it('multiplier bounds query reads the per-kind floors/ceilings', async () => {
    const data: MultiplierBounds = {
      absolute_min_milli: 100,
      absolute_max_milli: 1000,
      kinds: {
        official: { min_milli: 500, max_milli: 1000 },
        plan: { min_milli: 300, max_milli: 1000 },
        third_party: { min_milli: 100, max_milli: 1000 },
      },
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    const options = sellerMultiplierBoundsQuery()
    expect(options.queryKey).toEqual(['marketplace', 'seller', 'multiplier-bounds'])
    await expect(invokeQuery(options)).resolves.toEqual(data)
    expect(calls[0][0]).toBe('/api/marketplace/seller/multiplier-bounds')
  })

  it('listing headers query returns masked headers plus the pending candidate', async () => {
    const data: SellerHeadersView = {
      custom_channel: true,
      headers: [{ name: 'X-App', value_masked: 'sk-****' }],
      pending: {
        version: 3,
        submitted_at: '2026-10-27T09:00:00+08:00',
        headers: [{ name: 'X-App', value_masked: 'sk-****' }],
      },
      can_edit: true,
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    const options = sellerListingHeadersQuery(5)
    expect(options.queryKey).toEqual(['marketplace', 'seller', 'listing', 5, 'headers'])
    await expect(invokeQuery(options)).resolves.toEqual(data)
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/5/headers')
  })

  it('overview query unwraps cards, range and the chart series', async () => {
    const data: SellerOverview = {
      cards: { calls: 12, tokens: 3400, income_micro: 1_200_000, pending_micro: 80_000 },
      range: { days: 30, start: '2026-09-28', end: '2026-10-27' },
      by_model: [{ model: 'deepseek-flash', calls: 12, tokens: 3400, income_micro: 1_200_000 }],
      daily_by_model: [
        {
          model: 'deepseek-flash',
          days: [{ date: '2026-10-27', tokens: 3400, income_micro: 1_200_000 }],
        },
      ],
      by_channel: [
        { listing_id: 5, label: '主力通道', calls: 12, tokens: 3400, income_micro: 1_200_000 },
      ],
      as_of: '2026-10-27T12:00:00+08:00',
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    const options = sellerOverviewQuery()
    expect(options.queryKey).toEqual(['marketplace', 'seller', 'overview'])
    await expect(invokeQuery(options)).resolves.toEqual(data)
    expect(calls[0][0]).toBe('/api/marketplace/seller/overview')
  })

  it('health rows pass the -1 "no sample" success rates through untouched', async () => {
    const data: SellerListingHealth[] = [
      {
        listing_id: 5,
        latest_probe: null,
        today_requests: 0,
        today_success_rate_milli: 0,
        today_income_micro: 0,
        requests_1h: 0,
        success_rate_1h_milli: -1,
        requests_24h: 0,
        success_rate_24h_milli: -1,
        requests_7d: 0,
        success_rate_7d_milli: -1,
      },
      {
        listing_id: 6,
        latest_probe: null,
        today_requests: 40,
        today_success_rate_milli: 950,
        today_income_micro: 250_000,
        requests_1h: 4,
        success_rate_1h_milli: 1000,
        requests_24h: 40,
        success_rate_24h_milli: 950,
        requests_7d: 400,
        success_rate_7d_milli: 990,
      },
    ]
    stubFetch(200, { success: true, message: '', data })
    const rows = await invokeQuery(sellerListingsHealthQuery())
    expect(rows).toEqual(data)
    // -1 means "not enough samples" and must survive the envelope unwrap
    // unchanged (0 is a real all-failed window).
    expect(rows[0].success_rate_1h_milli).toBe(-1)
    expect(rows[0].success_rate_24h_milli).toBe(-1)
    expect(rows[0].success_rate_7d_milli).toBe(-1)
    expect(rows[1].success_rate_24h_milli).toBe(950)
  })
})

describe('seller listing mutations', () => {
  it('deleteListing issues a DELETE on the listing path without a body', async () => {
    const calls = stubFetch(200, { success: true, message: '', data: { id: 9, status: 'deleted' } })
    await expect(deleteListing(9)).resolves.toEqual({ id: 9, status: 'deleted' })
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/9')
    expect(calls[0][1]?.method).toBe('DELETE')
    expect(calls[0][1]?.body).toBeUndefined()
  })

  it('updateListingSettings PUTs only the dirty settings fields', async () => {
    const calls = stubFetch(200, { success: true, message: '', data: { id: 9, status: 'live' } })
    await expect(updateListingSettings(9, { label: '主力通道', rpm_limit: 600 })).resolves.toEqual({
      id: 9,
      status: 'live',
    })
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/9')
    expect(calls[0][1]?.method).toBe('PUT')
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({ label: '主力通道', rpm_limit: 600 })
  })

  it('batchListingModels POSTs one change across listings', async () => {
    const calls = stubFetch(200, { success: true, message: '', data: { ok: true } })
    await expect(
      batchListingModels({ listing_ids: [5, 9], multiplier_milli: 600 }),
    ).resolves.toEqual({ ok: true })
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/batch-models')
    expect(calls[0][1]?.method).toBe('POST')
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({
      listing_ids: [5, 9],
      multiplier_milli: 600,
    })
  })

  it('setListingModelsState PUTs the model subset and unwraps {items}', async () => {
    const data: SellerModelChangeResult = {
      items: [
        {
          model: 'deepseek-flash',
          enabled: false,
          multiplier_milli: 600,
          pending_multiplier_milli: 0,
          auto_disabled: false,
          disabled_reason: '',
        },
      ],
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    await expect(
      setListingModelsState(9, { models: ['deepseek-flash'], enabled: false }),
    ).resolves.toEqual(data)
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/9/models')
    expect(calls[0][1]?.method).toBe('PUT')
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({
      models: ['deepseek-flash'],
      enabled: false,
    })
  })

  it('setListingModelState PUTs the encoded model path and unwraps the row', async () => {
    const data: SellerModelChangeRow = {
      model: 'kimi/k3',
      enabled: true,
      multiplier_milli: 450,
      pending_multiplier_milli: 0,
      auto_disabled: false,
      disabled_reason: '',
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    await expect(setListingModelState(9, 'kimi/k3', { multiplier_milli: 450 })).resolves.toEqual(
      data,
    )
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/9/models/kimi%2Fk3')
    expect(calls[0][1]?.method).toBe('PUT')
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({ multiplier_milli: 450 })
  })

  it('restoreListingModels POSTs the restore path and returns the probe outcome', async () => {
    const data: SellerModelsRestoreResult = {
      restored: 2,
      still_failing: [
        { listing_id: 9, model: 'kimi/k3', code: 'PROBE_FAILED', message: 'upstream 500' },
      ],
      checked_at: '2026-10-27T12:00:00+08:00',
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    await expect(restoreListingModels(9)).resolves.toEqual(data)
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/9/models/restore')
    expect(calls[0][1]?.method).toBe('POST')
    expect(calls[0][1]?.body).toBeUndefined()
  })

  it('submitListingHeaders PUTs the header object under {headers}', async () => {
    const data: SellerHeadersSubmitResult = {
      pending: true,
      unchanged: false,
      version: 4,
      headers: [{ name: 'X-App', value_masked: 'sk-****' }],
    }
    const calls = stubFetch(200, { success: true, message: '', data })
    await expect(submitListingHeaders(9, { 'X-App': 'secret' })).resolves.toEqual(data)
    expect(calls[0][0]).toBe('/api/marketplace/seller/listings/9/headers')
    expect(calls[0][1]?.method).toBe('PUT')
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({ headers: { 'X-App': 'secret' } })
  })
})

describe('modelChangeErrors', () => {
  it('extracts the per-row items from a 409 MODEL_CHANGE_REJECTED response', async () => {
    const items: ModelChangeError[] = [
      {
        listing_id: 9,
        model: 'deepseek-flash',
        code: 'PRICE_JUMP',
        message: 'multiplier may rise at most 30% per change',
        earliest_change_at: '2026-10-27T13:00:00+08:00',
      },
      { listing_id: 9, model: 'kimi/k3', code: 'MODEL_NOT_FOUND', message: 'model is not listed' },
    ]
    stubFetch(409, {
      success: false,
      message: '2 model change(s) rejected',
      code: 'MODEL_CHANGE_REJECTED',
      items,
    })
    const err = await setListingModelsState(9, {
      models: ['deepseek-flash', 'kimi/k3'],
      multiplier_milli: 900,
    }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(409)
    expect(modelChangeErrors(err)).toEqual(items)
  })

  it('returns an empty array for other errors and non-ApiError values', async () => {
    stubFetch(409, { success: false, message: 'price conflict', code: 'PRICE_CONFLICT' })
    const err = await setListingModelState(9, 'deepseek-flash', { multiplier_milli: 900 }).catch(
      (e: unknown) => e,
    )
    expect(err).toBeInstanceOf(ApiError)
    expect(modelChangeErrors(err)).toEqual([])
    expect(modelChangeErrors(new Error('offline'))).toEqual([])
    expect(modelChangeErrors(null)).toEqual([])
  })
})
