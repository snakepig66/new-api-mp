import { afterEach, describe, expect, it } from 'vitest'

import { queryClient } from '@/lib/query-client'

import { normalizePayout, vendorName, vendorsQuery, type SellerVendor } from './api'

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
