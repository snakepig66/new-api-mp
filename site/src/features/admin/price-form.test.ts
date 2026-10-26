import { describe, expect, it } from 'vitest'

import {
  emptyPriceForm,
  parseShanghaiDateTime,
  priceFormSchema,
  priceFormToInput,
  shanghaiDateTimeToRfc3339,
} from './schemas'

// 2026-10-24 14:00 Asia/Shanghai.
const NOW = Date.UTC(2026, 9, 24, 6, 0, 0)

function validForm() {
  return {
    model: 'deepseek-v4-pro',
    vendor: 'deepseek',
    tier: 'offpeak',
    input_micro_per_mtok: '2',
    output_micro_per_mtok: '5',
    cache_read_micro_per_mtok: '0.04',
    cache_write_micro_per_mtok: '2',
    version: '',
    effective_at: '2026-10-24T14:01',
  }
}

describe('priceFormSchema', () => {
  it('accepts a valid price version and keeps blank version auto-assigned', () => {
    const result = priceFormSchema(NOW).safeParse(validForm())
    expect(result.success).toBe(true)
    if (!result.success) return
    const input = priceFormToInput(result.data)
    expect(input.model).toBe('deepseek-v4-pro')
    expect(input.tier).toBe('offpeak')
    expect(input.input_micro_per_mtok).toBe(2_000_000)
    expect(input.version).toBeUndefined()
    expect(input.effective_at).toBe('2026-10-24T06:01:00.000Z')
  })

  it('rejects an unknown tier', () => {
    const result = priceFormSchema(NOW).safeParse({ ...validForm(), tier: 'weekend' })
    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues[0]?.message).toBe('时段只能是 peak、offpeak 或 flat')
  })

  it.each(['0', '-1', 'abc', '', '1.2345678'])('rejects an invalid price: %s', (value) => {
    const result = priceFormSchema(NOW).safeParse({
      ...validForm(),
      input_micro_per_mtok: value,
    })
    expect(result.success).toBe(false)
  })

  it('rejects a non-positive version but accepts a positive one', () => {
    expect(priceFormSchema(NOW).safeParse({ ...validForm(), version: '0' }).success).toBe(false)
    expect(priceFormSchema(NOW).safeParse({ ...validForm(), version: '3' }).success).toBe(true)
  })

  it('rejects an effective_at earlier than the current minute', () => {
    const result = priceFormSchema(NOW).safeParse({
      ...validForm(),
      effective_at: '2026-10-24T13:59',
    })
    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues[0]?.message).toBe('生效时间不得早于当前时间')
  })

  it('rejects an invalid calendar date', () => {
    expect(priceFormSchema(NOW).safeParse({ ...validForm(), effective_at: '2026-02-30T10:00' }).success).toBe(false)
  })
})

describe('Shanghai datetime parsing', () => {
  it('interprets datetime-local as Asia/Shanghai (UTC+08:00)', () => {
    expect(shanghaiDateTimeToRfc3339('2026-10-24T14:03')).toBe('2026-10-24T06:03:00.000Z')
    expect(shanghaiDateTimeToRfc3339('2026-01-01 00:00:30')).toBe('2025-12-31T16:00:30.000Z')
    expect(parseShanghaiDateTime('not-a-date')).toBeNull()
  })

  it('prefills the next minute in Shanghai', () => {
    // 2026-10-24 06:00:30 UTC → 14:00 Shanghai; next input minute is 14:01.
    expect(emptyPriceForm(Date.UTC(2026, 9, 24, 6, 0, 30)).effective_at).toBe('2026-10-24T14:01')
  })
})
