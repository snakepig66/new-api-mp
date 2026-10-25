import { describe, expect, it } from 'vitest'

import { vendorLabel, vendorOptions } from './vendors'

describe('vendor labels', () => {
  it('maps known slugs to Chinese display names', () => {
    expect(vendorLabel('deepseek')).toBe('DeepSeek')
    expect(vendorLabel('zhipu')).toBe('智谱')
    expect(vendorLabel('alibaba')).toBe('阿里云')
    expect(vendorLabel('anthropic')).toBe('Anthropic')
  })

  it('falls back to the raw slug for unknown vendors', () => {
    expect(vendorLabel('acme-ai')).toBe('acme-ai')
  })

  it('dedupes and sorts options for the pill row', () => {
    expect(vendorOptions(['zhipu', 'deepseek', 'zhipu', ''])).toEqual([
      { value: 'zhipu', label: '智谱' },
      { value: 'deepseek', label: 'DeepSeek' },
    ])
  })
})
