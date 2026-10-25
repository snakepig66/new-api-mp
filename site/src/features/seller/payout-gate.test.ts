import { describe, expect, it } from 'vitest'

import { MIN_PAYOUT_MICRO, payoutGateState } from './payout-gate'

describe('payoutGateState', () => {
  it('lists every remaining step for a fresh seller', () => {
    const state = payoutGateState({
      realNameVerified: false,
      payoutAccountBound: false,
      availableMicro: 0,
      pendingMicro: 0,
    })
    expect(state.steps.map((step) => [step.id, step.done])).toEqual([
      ['real_name', false],
      ['alipay', false],
      ['minimum', false],
      ['t7', true],
    ])
    expect(state.doneCount).toBe(1)
    expect(state.total).toBe(4)
    expect(state.ready).toBe(false)
    expect(state.missingMicro).toBe(MIN_PAYOUT_MICRO)
  })

  it('marks real-name, Alipay and the ¥50 minimum as met', () => {
    const state = payoutGateState({
      realNameVerified: true,
      payoutAccountBound: true,
      availableMicro: '50000000',
      pendingMicro: 0,
    })
    expect(state.ready).toBe(true)
    expect(state.doneCount).toBe(4)
    expect(state.missingMicro).toBe(0n)
  })

  it('keeps T+7 pending while money is still settling', () => {
    const state = payoutGateState({
      realNameVerified: true,
      payoutAccountBound: true,
      availableMicro: 50_000_000,
      pendingMicro: 12_500_000,
    })
    expect(state.ready).toBe(false)
    expect(state.steps.find((step) => step.id === 't7')?.done).toBe(false)
    expect(state.doneCount).toBe(3)
  })

  it('computes the remaining amount below the minimum', () => {
    const state = payoutGateState({
      realNameVerified: true,
      payoutAccountBound: false,
      availableMicro: 20_000_000,
      pendingMicro: 0,
    })
    expect(state.missingMicro).toBe(30_000_000n)
    expect(state.steps.find((step) => step.id === 'minimum')?.done).toBe(false)
    expect(state.steps.find((step) => step.id === 'alipay')?.done).toBe(false)
  })
})
