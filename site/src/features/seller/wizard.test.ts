import { describe, expect, it } from 'vitest'

import type { ProbeResult } from './api'
import {
  buildVerificationMatrix,
  canJumpTo,
  canLeaveStep,
  canSubmit,
  maskApiKey,
  nextWizardStep,
  parseApiKeys,
  previousWizardStep,
  validCustomURL,
  validRequestHeaders,
  type WizardSnapshot,
} from './wizard'

const okProbe: ProbeResult = {
  ok: true,
  models: ['deepseek-flash', 'deepseek-v4-pro'],
  verified_models: ['deepseek-flash'],
  missing_models: [],
  latency_ms: 120,
  prompt_cache: true,
  balance_micro: 10_000_000,
  key_valid: true,
  fail_reason: '',
  trust_pool: {
    matched: true,
    trust: 'trusted',
    kind: 'official',
    vendor_name: 'DeepSeek',
    endpoints: ['openai_chat', 'anthropic_messages'],
    badge_text: 'DeepSeek · 官方 · OpenAI + Anthropic · 免审核',
  },
}

function snapshot(overrides: Partial<WizardSnapshot> = {}): WizardSnapshot {
  return {
    url: 'https://api.deepseek.com',
    vendorId: 'deepseek',
    apiKeys: ['sk-test-1234567890'],
    trust: okProbe.trust_pool ?? null,
    probe: okProbe,
    availableModels: ['deepseek-flash', 'deepseek-v4-pro'],
    selectedModels: ['deepseek-flash'],
    modelMultipliers: { 'deepseek-flash': 500 },
    concurrency: 5,
    rpm: 0,
    dailyCapYuan: '0',
    hoursValid: true,
    ...overrides,
  }
}

describe('wizard key parsing', () => {
  it('splits lines, trims, drops blanks and de-duplicates', () => {
    expect(parseApiKeys(' sk-a \n\nsk-b\r\nsk-a\n')).toEqual(['sk-a', 'sk-b'])
    expect(parseApiKeys('')).toEqual([])
  })
})

describe('wizard step gates', () => {
  it('requiring an https address, keys and a non-blocked trust state before leaving step 0', () => {
    expect(canLeaveStep(0, snapshot({ url: 'http://api.deepseek.com' }))).toBe(false)
    expect(canLeaveStep(0, snapshot({ url: 'https://api.deepseek.com' }))).toBe(true)
    expect(canLeaveStep(0, snapshot({ apiKeys: [] }))).toBe(false)
    expect(
      canLeaveStep(
        0,
        snapshot({
          trust: { ...okProbe.trust_pool!, trust: 'blocked' },
        }),
      ),
    ).toBe(false)
    expect(
      canLeaveStep(
        0,
        snapshot({
          trust: { ...okProbe.trust_pool!, trust: 'unlisted' },
        }),
      ),
    ).toBe(true)
    expect(nextWizardStep(0, snapshot({ apiKeys: [] }))).toBe(0)
    expect(nextWizardStep(0, snapshot())).toBe(1)
  })

  it('requires a selected model the keys can call and a 0.1x–1.0x multiplier', () => {
    expect(canLeaveStep(1, snapshot({ selectedModels: [] }))).toBe(false)
    expect(canLeaveStep(1, snapshot({ selectedModels: ['not-served'] }))).toBe(false)
    expect(
      canLeaveStep(1, snapshot({ modelMultipliers: { 'deepseek-flash': 50 } })),
    ).toBe(false)
    expect(
      canLeaveStep(1, snapshot({ modelMultipliers: { 'deepseek-flash': 1100 } })),
    ).toBe(false)
    expect(canLeaveStep(1, snapshot())).toBe(true)
  })

  it('validates limits on step 2', () => {
    expect(canLeaveStep(2, snapshot({ concurrency: 0 }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ rpm: -1 }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ dailyCapYuan: 'abc' }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ hoursValid: false }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ dailyCapYuan: '50.5' }))).toBe(true)
  })

  it('requires valid header lines on step 3', () => {
    expect(canLeaveStep(3, snapshot({ headersValid: false }))).toBe(false)
    expect(canLeaveStep(3, snapshot({ headersValid: true }))).toBe(true)
    expect(canLeaveStep(3, snapshot())).toBe(true)
  })

  it('does not skip steps', () => {
    expect(canJumpTo(2, snapshot({ apiKeys: [] }))).toBe(false)
    expect(canJumpTo(2, snapshot())).toBe(true)
    expect(canJumpTo(3, snapshot({ selectedModels: [] }))).toBe(false)
    expect(canJumpTo(4, snapshot())).toBe(true)
  })
})

describe('wizard submission gate', () => {
  it('accepts the default limits and a valid multiplier', () => {
    expect(canSubmit(snapshot())).toBe(true)
  })

  it('requires the plan-terms checkbox for a plan channel', () => {
    expect(canSubmit(snapshot({ requiresPlanTerms: true }))).toBe(false)
    expect(
      canSubmit(snapshot({ requiresPlanTerms: true, planTermsAccepted: true })),
    ).toBe(true)
  })

  it('clamps previousWizardStep and nextWizardStep', () => {
    expect(previousWizardStep(0)).toBe(0)
    expect(previousWizardStep(3)).toBe(2)
    expect(nextWizardStep(4, snapshot())).toBe(4)
  })
})

describe('wizard custom URL and headers', () => {
  it('validCustomURL refuses userinfo, query and whitespace', () => {
    expect(validCustomURL('https://user:pass@api.example.com')).toBe(false)
    expect(validCustomURL('https://api.example.com/v1?x=1')).toBe(false)
    expect(validCustomURL('  https://api.example.com  ')).toBe(true)
    expect(validCustomURL('https://api.example.com#frag')).toBe(false)
    expect(validCustomURL('https://')).toBe(false)
  })

  it('accepts key: value and key=value header lines', () => {
    expect(validRequestHeaders('')).toBe(true)
    expect(validRequestHeaders('X-Foo: bar\nX-Baz=qux')).toBe(true)
    expect(validRequestHeaders('X-Foo bar')).toBe(false)
    expect(validRequestHeaders('X Foo: bar')).toBe(false)
    expect(validRequestHeaders('X-Foo:')).toBe(false)
  })
})

describe('verification matrix', () => {
  it('crosses every key, model and endpoint and marks callable models', () => {
    const cells = buildVerificationMatrix(
      ['sk-one-123456789', 'sk-two-123456789'],
      ['deepseek-flash', 'deepseek-v4-pro'],
      ['openai_chat', 'anthropic_messages'],
      {
        'sk-one-123456789': okProbe,
        'sk-two-123456789': { ...okProbe, key_valid: false, fail_reason: 'key rejected' },
      },
    )
    expect(cells).toHaveLength(8)
    const firstKeyFlash = cells.filter(
      (cell) => cell.key === 'sk-one-123456789' && cell.model === 'deepseek-flash',
    )
    expect(firstKeyFlash).toHaveLength(2)
    expect(firstKeyFlash.every((cell) => cell.ok)).toBe(true)
    expect(
      cells.filter((cell) => cell.key === 'sk-one-123456789' && cell.model === 'deepseek-v4-pro'),
    ).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ ok: false, failReason: '该模型不在这个 Key 的可用列表中' }),
      ]),
    )
    expect(
      cells
        .filter((cell) => cell.key === 'sk-two-123456789')
        .every((cell) => !cell.ok && cell.failReason === 'key rejected'),
    ).toBe(true)
  })

  it('marks an unprobed key as not checked and defaults to one endpoint', () => {
    const cells = buildVerificationMatrix(['sk-missing'], ['deepseek-flash'], [], {})
    expect(cells).toHaveLength(1)
    expect(cells[0]).toMatchObject({ endpoint: 'openai_chat', ok: false, failReason: '尚未检测' })
  })

  it('masks keys to the last four characters', () => {
    expect(maskApiKey('sk-abcdefgh')).toBe('••••efgh')
    expect(maskApiKey('abc')).toBe('••••')
  })
})
