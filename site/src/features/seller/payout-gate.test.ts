import { describe, expect, it } from 'vitest'

import { MIN_PAYOUT_MICRO, payoutGateState } from './payout-gate'

describe('payoutGateState', () => {
  it('lists every remaining step for a fresh seller', () => {
    const state = payoutGateState({
      payoutAccountBound: false,
      availableMicro: 0,
      pendingMicro: 0,
    })
    expect(state.steps.map((step) => [step.id, step.done])).toEqual([
      ['payout_account', false],
      ['minimum', false],
      ['t3', true],
    ])
    expect(state.doneCount).toBe(1)
    expect(state.total).toBe(3)
    expect(state.ready).toBe(false)
    expect(state.missingMicro).toBe(MIN_PAYOUT_MICRO)
  })

  it('marks payout account and the ¥10 minimum as met', () => {
    const state = payoutGateState({
      payoutAccountBound: true,
      availableMicro: '10000000',
      pendingMicro: 0,
    })
    expect(state.ready).toBe(true)
    expect(state.doneCount).toBe(3)
    expect(state.missingMicro).toBe(0n)
  })

  it('keeps T+3 pending while money is still settling', () => {
    const state = payoutGateState({
      payoutAccountBound: true,
      availableMicro: 10_000_000,
      pendingMicro: 12_500_000,
    })
    expect(state.ready).toBe(false)
    expect(state.steps.find((step) => step.id === 't3')?.done).toBe(false)
    expect(state.doneCount).toBe(2)
  })

  it('computes the remaining amount below the minimum', () => {
    const state = payoutGateState({
      payoutAccountBound: false,
      availableMicro: 6_000_000,
      pendingMicro: 0,
    })
    expect(state.missingMicro).toBe(4_000_000n)
    expect(state.steps.find((step) => step.id === 'minimum')?.done).toBe(false)
    expect(state.steps.find((step) => step.id === 'payout_account')?.done).toBe(false)
  })
})
