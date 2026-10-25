import { describe, expect, it } from 'vitest'

import { compareMultiplier, previewMultiplier } from './preview'

describe('previewMultiplier', () => {
  it('prices a 0.30x listing with the 7% platform fee', () => {
    const preview = previewMultiplier(2_000_000, 300)
    expect(preview).not.toBeNull()
    expect(preview?.buyerMicro).toBe(600_000n)
    expect(preview?.feeMicro).toBe(42_000n)
    expect(preview?.sellerMicro).toBe(558_000n)
  })

  it('handles a 0.875x multiplier with integer micros', () => {
    const preview = previewMultiplier('2000000', 875)
    expect(preview?.buyerMicro).toBe(1_750_000n)
    expect(preview?.feeMicro).toBe(122_500n)
    expect(preview?.sellerMicro).toBe(1_627_500n)
  })

  it('rounds the buyer price up once, exactly like the backend Quote', () => {
    const preview = previewMultiplier(999, 500)
    expect(preview?.buyerMicro).toBe(500n)
    expect(preview?.feeMicro).toBe(35n)
    expect(preview?.sellerMicro).toBe(465n)
  })

  it('caps the multiplier at 1.000x (no markup)', () => {
    const preview = previewMultiplier(2_000_000, 1000)
    expect(preview?.buyerMicro).toBe(2_000_000n)
    expect(preview?.sellerMicro).toBe(1_860_000n)
  })

  it('uses the backend fee percentage when provided', () => {
    const preview = previewMultiplier(2_000_000, 500, 10)
    expect(preview?.buyerMicro).toBe(1_000_000n)
    expect(preview?.feeMicro).toBe(100_000n)
    expect(preview?.sellerMicro).toBe(900_000n)
    expect(preview?.feePercent).toBe(10)
  })

  it('multiplies the official price by both the channel coefficient and the multiplier', () => {
    const preview = previewMultiplier(2_000_000, 300, 7, 1200)
    expect(preview?.buyerMicro).toBe(720_000n)
    expect(preview?.feeMicro).toBe(50_400n)
    expect(preview?.sellerMicro).toBe(669_600n)
    expect(preview?.coefficientMilli).toBe(1200)

    // Missing/invalid coefficients fall back to the official price, exactly
    // like the backend Quote.
    expect(previewMultiplier(2_000_000, 300, 7, 0)?.buyerMicro).toBe(600_000n)
    expect(previewMultiplier(2_000_000, 300, 7, 0)?.coefficientMilli).toBe(1000)
  })

  it('defaults to the 7% fee and rejects an invalid fee', () => {
    expect(previewMultiplier(2_000_000, 500)?.feePercent).toBe(7)
    expect(previewMultiplier(2_000_000, 500, 101)).toBeNull()
  })

  it('rejects out-of-range multipliers and malformed official prices', () => {
    expect(previewMultiplier(2_000_000, 0)).toBeNull()
    expect(previewMultiplier(2_000_000, 1001)).toBeNull()
    expect(previewMultiplier(2_000_000, 500.5)).toBeNull()
    expect(previewMultiplier('-1', 500)).toBeNull()
    expect(previewMultiplier('abc', 500)).toBeNull()
  })
})

describe('compareMultiplier', () => {
  it('compares against the market minimum', () => {
    expect(compareMultiplier(300, 500)).toBe('lower')
    expect(compareMultiplier(500, 500)).toBe('same')
    expect(compareMultiplier(700, 500)).toBe('higher')
  })

  it('returns null when there is no market supply', () => {
    expect(compareMultiplier(300, null)).toBeNull()
    expect(compareMultiplier(300, undefined)).toBeNull()
    expect(compareMultiplier(300, 0)).toBeNull()
  })
})
