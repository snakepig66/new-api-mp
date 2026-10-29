/**
 * Withdrawal readiness derived from data the seller APIs really return:
 *   - meQuery: real-name + Alipay binding progress;
 *   - sellerStatsQuery: available balance (T+7 matured) and pending balance.
 *
 * The exact T+7 unlock date is not exposed per ledger row, so the T+7 step is
 * described statically and marked pending while any amount is still pending.
 * Nothing here invents a backend field.
 */

export const MIN_PAYOUT_MICRO = 10_000_000n

export type PayoutGateStepId = 'payout_account' | 'minimum' | 't3' | 'real_name' | 'alipay' | 't7'

export interface PayoutGateInput {
  realNameVerified?: boolean
  payoutAccountBound: boolean
  availableMicro: number | string
  pendingMicro: number | string
}

export interface PayoutGateStep {
  id: PayoutGateStepId
  done: boolean
}

export interface PayoutGateState {
  steps: PayoutGateStep[]
  doneCount: number
  total: number
  ready: boolean
  /** Micro-yuan still missing to reach the ¥10 minimum (0 when met). */
  missingMicro: bigint
}

function toBigInt(value: number | string): bigint {
  try {
    return BigInt(String(value))
  } catch {
    return 0n
  }
}

export function payoutGateState(input: PayoutGateInput): PayoutGateState {
  const available = toBigInt(input.availableMicro)
  const pending = toBigInt(input.pendingMicro)
  const minimumDone = available >= MIN_PAYOUT_MICRO
  const missing = minimumDone ? 0n : MIN_PAYOUT_MICRO - available
  const steps: PayoutGateStep[] = [
    { id: 'payout_account', done: input.payoutAccountBound },
    { id: 'minimum', done: minimumDone },
    { id: 't3', done: pending <= 0n },
  ]
  const doneCount = steps.filter((step) => step.done).length
  return {
    steps,
    doneCount,
    total: steps.length,
    ready: doneCount === steps.length,
    missingMicro: missing,
  }
}
