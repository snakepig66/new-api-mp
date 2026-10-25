import { describe, expect, it } from 'vitest'

import {
  isOfficialVendor,
  toVendorLookup,
  vendorLabel,
  vendorOptions,
} from './vendors'

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

  it('prefers catalogue display names when a metadata lookup is given', () => {
    const lookup = toVendorLookup([
      { id: 'volcengine', display_name: '火山方舟', tier: 'official' },
    ])
    expect(vendorLabel('volcengine', lookup)).toBe('火山方舟')
    expect(vendorLabel('acme-ai', lookup)).toBe('acme-ai')
    expect(isOfficialVendor('volcengine', lookup)).toBe(true)
    expect(isOfficialVendor('acme-ai', lookup)).toBe(false)
  })

  it('accepts a plain-object lookup too', () => {
    const lookup = { deepseek: { display_name: '深度求索', tier: 'official' } }
    expect(vendorLabel('deepseek', lookup)).toBe('深度求索')
  })

  it('dedupes and sorts options for the pill row', () => {
    expect(vendorOptions(['zhipu', 'deepseek', 'zhipu', ''])).toEqual([
      { value: 'zhipu', label: '智谱' },
      { value: 'deepseek', label: 'DeepSeek' },
    ])
  })
})
