import { describe, expect, it } from 'vitest'

import { formatYuan } from './money'

describe('formatYuan', () => {
  it('renders exact zero without decimals', () => {
    expect(formatYuan(0)).toBe('¥0')
    expect(formatYuan(0n)).toBe('¥0')
  })

  it('renders a normal per-million price', () => {
    expect(formatYuan(420_000)).toBe('¥0.42')
    expect(formatYuan('420000')).toBe('¥0.42')
  })

  it('keeps six fraction digits for tiny amounts', () => {
    expect(formatYuan(1)).toBe('¥0.000001')
    expect(formatYuan(999_999)).toBe('¥0.999999')
  })

  it('pads whole yuan to two decimals', () => {
    expect(formatYuan(1_000_000)).toBe('¥1.00')
    expect(formatYuan(1_500_000)).toBe('¥1.50')
  })

  it('trims trailing zeros beyond two decimals', () => {
    expect(formatYuan(1_234_560)).toBe('¥1.23456')
  })

  it('groups the integer part with commas', () => {
    expect(formatYuan(1_234_567_890)).toBe('¥1,234.56789')
  })

  it('handles int64 values as bigint or string without precision loss', () => {
    expect(formatYuan(9_223_372_036_854_775_807n)).toBe('¥9,223,372,036,854.775807')
    expect(formatYuan('9223372036854775807')).toBe('¥9,223,372,036,854.775807')
  })

  it('renders negative amounts with the sign before the currency mark', () => {
    expect(formatYuan(-1_500_000)).toBe('-¥1.50')
    expect(formatYuan('-1')).toBe('-¥0.000001')
  })

  it('returns an em dash for values that are not integer micro-yuan', () => {
    expect(formatYuan(1.5)).toBe('—')
    expect(formatYuan(Number.NaN)).toBe('—')
    expect(formatYuan(Number.POSITIVE_INFINITY)).toBe('—')
    expect(formatYuan(Number.MAX_SAFE_INTEGER + 1)).toBe('—')
  })

  it('returns an em dash for malformed strings', () => {
    expect(formatYuan('')).toBe('—')
    expect(formatYuan('abc')).toBe('—')
    expect(formatYuan('1.5')).toBe('—')
    expect(formatYuan('1 000')).toBe('—')
    expect(formatYuan('1e6')).toBe('—')
  })
})
