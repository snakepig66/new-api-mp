import { describe, expect, it } from 'vitest'

import { copy } from '@/copy'

import {
  milliToMultiplierInput,
  parseMultiplierToMilli,
  parseSellerIds,
  parseYuanToMicro,
  prettyJson,
} from './format'

describe('parseYuanToMicro', () => {
  it('converts a typed yuan amount into integer micro-yuan', () => {
    expect(parseYuanToMicro('12.5')).toBe(12_500_000)
    expect(parseYuanToMicro(' 0 ')).toBe(0)
    expect(parseYuanToMicro('1.000001')).toBe(1_000_001)
  })

  it('rejects malformed or over-precise input', () => {
    expect(parseYuanToMicro('')).toBeNull()
    expect(parseYuanToMicro('abc')).toBeNull()
    expect(parseYuanToMicro('1.0000001')).toBeNull()
    expect(parseYuanToMicro('-1')).toBeNull()
    expect(parseYuanToMicro('1,000')).toBeNull()
  })
})

describe('multiplier caps', () => {
  it('converts between milli and input text', () => {
    expect(parseMultiplierToMilli('0.85')).toBe(850)
    expect(parseMultiplierToMilli('')).toBe(0)
    expect(parseMultiplierToMilli('1')).toBe(1000)
    expect(milliToMultiplierInput(850)).toBe('0.85')
    expect(milliToMultiplierInput(0)).toBe('')
  })

  it('rejects values outside 0–1x', () => {
    expect(parseMultiplierToMilli('1.001')).toBeNull()
    expect(parseMultiplierToMilli('2')).toBeNull()
    expect(parseMultiplierToMilli('0,8')).toBeNull()
  })
})

describe('parseSellerIds', () => {
  it('splits comma / whitespace lists and removes duplicates', () => {
    expect(parseSellerIds('10023, 10024 10023，10025')).toEqual({
      ids: [10023, 10024, 10025],
      invalid: [],
    })
  })

  it('reports non-positive and non-numeric tokens', () => {
    expect(parseSellerIds('0, -3, abc, 10023')).toEqual({
      ids: [10023],
      invalid: ['0', '-3', 'abc'],
    })
  })
})

describe('prettyJson', () => {
  it('pretty prints a retry chain and passes raw text through on parse errors', () => {
    expect(prettyJson('[{"hop":1}]')).toBe('[\n  {\n    "hop": 1\n  }\n]')
    expect(prettyJson('not json')).toBe('not json')
    expect(prettyJson('')).toBe('')
    expect(copy.pages.consoleRecords.detail.retry).toBeTruthy()
  })
})
