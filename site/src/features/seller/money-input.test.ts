import { describe, expect, it } from 'vitest'

import { microToYuanInput, parseYuanToMicro } from './money-input'

describe('parseYuanToMicro', () => {
  it('parses whole and fractional yuan without floats', () => {
    expect(parseYuanToMicro('50')).toBe(50_000_000n)
    expect(parseYuanToMicro('50.5')).toBe(50_500_000n)
    expect(parseYuanToMicro('0.000001')).toBe(1n)
    expect(parseYuanToMicro('1,000')).toBe(1_000_000_000n)
  })

  it('rejects malformed input', () => {
    expect(parseYuanToMicro('')).toBeNull()
    expect(parseYuanToMicro('abc')).toBeNull()
    expect(parseYuanToMicro('-1')).toBeNull()
    expect(parseYuanToMicro('1.2345678')).toBeNull()
    expect(parseYuanToMicro('1.2.3')).toBeNull()
  })
})

describe('microToYuanInput', () => {
  it('formats micros for an input without a currency mark', () => {
    expect(microToYuanInput(50_000_000)).toBe('50')
    expect(microToYuanInput(50_500_000)).toBe('50.5')
    expect(microToYuanInput('123400000')).toBe('123.4')
    expect(microToYuanInput(1)).toBe('0.000001')
    expect(microToYuanInput(0)).toBe('0')
  })
})
