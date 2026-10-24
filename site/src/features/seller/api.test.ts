import { describe, expect, it } from 'vitest'

import { normalizePayout } from './api'

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
