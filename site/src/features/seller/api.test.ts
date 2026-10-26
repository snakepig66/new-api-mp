import { afterEach, describe, expect, it } from 'vitest'

import { queryClient } from '@/lib/query-client'

import {
  normalizePayout,
  sellerChannelKind,
  sellerCommonAddressesQuery,
  VERIFY_POLL_INTERVAL_MS,
  verifyingRefetchInterval,
  vendorName,
  vendorsQuery,
  type ProbeResult,
  type SellerCommonAddress,
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
