import { describe, expect, it } from 'vitest'

import { copy } from '@/copy'

import { buildModelSelection, keyNameSchema, prefFormSchema, prefFormToInput, prefToFormValues, spendCapSchema } from './schemas'

const wizardErrors = copy.pages.consoleKeys.wizard.errors
const detailErrors = copy.pages.consoleKeyDetail.errors

describe('key wizard validation', () => {
  it('requires a trimmed name of at most 50 characters', () => {
    expect(keyNameSchema.safeParse('生产环境').success).toBe(true)
    expect(keyNameSchema.safeParse('   ').success).toBe(false)
    expect(keyNameSchema.safeParse('x'.repeat(51)).success).toBe(false)
  })

  it('accepts a blank spend cap and rejects malformed amounts', () => {
    expect(spendCapSchema.safeParse('').success).toBe(true)
    expect(spendCapSchema.safeParse('50.5').success).toBe(true)
    expect(spendCapSchema.safeParse('1e6').success).toBe(false)
    expect(spendCapSchema.safeParse('-5').success).toBe(false)
    expect(spendCapSchema.safeParse('abc').success).toBe(false)
  })

  it('builds the model selection and converts per-model caps', () => {
    const result = buildModelSelection({ 'deepseek-v3': '0.85', 'qwen3-235b': '' })
    expect(result).toEqual({
      ok: true,
      models: [
        { model: 'deepseek-v3', price_cap_milli: 850 },
        { model: 'qwen3-235b', price_cap_milli: 0 },
      ],
    })
  })

  it('rejects an empty selection, an invalid cap, and names the offending model', () => {
    expect(buildModelSelection({})).toEqual({
      ok: false,
      error: wizardErrors.modelsRequired,
    })
    expect(buildModelSelection({ 'deepseek-v3': '3' })).toEqual({
      ok: false,
      error: wizardErrors.capInvalid,
      model: 'deepseek-v3',
    })
  })
})

describe('key detail preference form', () => {
  const base = {
    strategy: 'balanced' as const,
    priceCap: '0.85',
    rpm: '60',
    prefer: '10023, 10024',
    block: '',
    only: '',
    allowFallback: true,
    officialOnly: false,
  }

  it('accepts a valid preference form and builds the API body', () => {
    const parsed = prefFormSchema.safeParse(base)
    expect(parsed.success).toBe(true)
    if (!parsed.success) return
    expect(prefFormToInput(7, parsed.data)).toEqual({
      token_id: 7,
      strategy: 'balanced',
      price_cap_milli: 850,
      prefer_seller_ids: [10023, 10024],
      block_seller_ids: [],
      only_seller_id: 0,
      allow_fallback: true,
      rpm: 60,
      official_only: false,
    })
  })

  it('rejects only-seller together with prefer or block lists', () => {
    const parsed = prefFormSchema.safeParse({ ...base, only: '10023' })
    expect(parsed.success).toBe(false)
    if (parsed.success) return
    expect(parsed.error.issues.some((issue) => issue.message === detailErrors.onlyConflict)).toBe(true)
  })

  it('rejects invalid seller ids, rpm and price cap', () => {
    const parsed = prefFormSchema.safeParse({
      ...base,
      prefer: 'abc',
      rpm: '1.5',
      priceCap: '2',
    })
    expect(parsed.success).toBe(false)
    if (parsed.success) return
    const messages = parsed.error.issues.map((issue) => issue.message)
    expect(messages).toContain(detailErrors.sellerInvalid)
    expect(messages).toContain(detailErrors.rpmInvalid)
    expect(messages).toContain(detailErrors.priceCapInvalid)
  })

  it('hydrates defaults from a missing preference row', () => {
    expect(prefToFormValues(null)).toEqual({
      strategy: 'balanced',
      priceCap: '',
      rpm: '',
      prefer: '',
      block: '',
      only: '',
      allowFallback: false,
      officialOnly: false,
    })
  })
})
