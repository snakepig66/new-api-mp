import { describe, expect, it } from 'vitest'

import type { ProbeResult } from './api'
import {
  canJumpTo,
  canLeaveStep,
  canSubmit,
  nextWizardStep,
  previousWizardStep,
  type WizardSnapshot,
} from './wizard'

const okProbe: ProbeResult = {
  ok: true,
  models: ['deepseek-flash', 'deepseek-v4-pro'],
  missing_models: [],
  latency_ms: 120,
  prompt_cache: true,
  balance_micro: 10_000_000,
  key_valid: true,
  fail_reason: '',
}

function snapshot(overrides: Partial<WizardSnapshot> = {}): WizardSnapshot {
  return {
    vendorId: 'deepseek',
    apiKey: 'sk-test-1234567890',
    probe: okProbe,
    selectedModels: ['deepseek-flash'],
    multiplierMilli: 500,
    concurrency: 5,
    rpm: 0,
    dailyCapYuan: '0',
    hoursValid: true,
    ...overrides,
  }
}

describe('wizard step gates', () => {
  it('requires a vendor before leaving step 0', () => {
    expect(canLeaveStep(0, snapshot({ vendorId: '' }))).toBe(false)
    expect(canLeaveStep(0, snapshot())).toBe(true)
    expect(nextWizardStep(0, snapshot({ vendorId: '' }))).toBe(0)
    expect(nextWizardStep(0, snapshot())).toBe(1)
  })

  it('requires a non-empty key without whitespace before leaving step 1', () => {
    expect(canLeaveStep(1, snapshot({ apiKey: '' }))).toBe(false)
    expect(canLeaveStep(1, snapshot({ apiKey: 'short' }))).toBe(false)
    expect(canLeaveStep(1, snapshot({ apiKey: 'sk-with space 123' }))).toBe(false)
    expect(canLeaveStep(1, snapshot({ apiKey: 'sk-valid-123456' }))).toBe(true)
  })

  it('requires a successful probe and at least one available model', () => {
    expect(canLeaveStep(2, snapshot({ probe: null }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ probe: { ...okProbe, ok: false } }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ selectedModels: [] }))).toBe(false)
    expect(canLeaveStep(2, snapshot({ selectedModels: ['not-served'] }))).toBe(false)
    expect(canLeaveStep(2, snapshot())).toBe(true)
  })

  it('does not skip steps', () => {
    expect(canJumpTo(2, snapshot({ apiKey: '' }))).toBe(false)
    expect(canJumpTo(2, snapshot())).toBe(true)
    expect(canJumpTo(3, snapshot({ selectedModels: [] }))).toBe(false)
  })
})

describe('wizard submission gate', () => {
  it('accepts the default limits and a valid multiplier', () => {
    expect(canSubmit(snapshot())).toBe(true)
  })

  it('rejects an out-of-range multiplier or malformed daily cap', () => {
    expect(canSubmit(snapshot({ multiplierMilli: 0 }))).toBe(false)
    expect(canSubmit(snapshot({ multiplierMilli: 1001 }))).toBe(false)
    expect(canSubmit(snapshot({ dailyCapYuan: 'abc' }))).toBe(false)
    expect(canSubmit(snapshot({ dailyCapYuan: '50.5' }))).toBe(true)
  })

  it('rejects an invalid hours window', () => {
    expect(canSubmit(snapshot({ hoursValid: false }))).toBe(false)
  })

  it('clamps previousWizardStep at zero and nextWizardStep at the last step', () => {
    expect(previousWizardStep(0)).toBe(0)
    expect(previousWizardStep(2)).toBe(1)
    expect(nextWizardStep(3, snapshot())).toBe(3)
  })
})
