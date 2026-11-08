import { copy } from '@/copy'
import type { TagTone } from '@/ui'

import {
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  TOKEN_STATUS_EXHAUSTED,
  TOKEN_STATUS_EXPIRED,
  type Strategy,
  type TokenRecord,
} from './types'

export interface StatusMeta {
  label: string
  tone: TagTone
}

export function tokenStatusMeta(status: number): StatusMeta {
  const labels = copy.pages.consoleKeys.status
  switch (status) {
    case TOKEN_STATUS_ENABLED:
      return { label: labels.enabled, tone: 'brand' }
    case TOKEN_STATUS_DISABLED:
      return { label: labels.disabled, tone: 'neutral' }
    case TOKEN_STATUS_EXPIRED:
      return { label: labels.expired, tone: 'neutral' }
    case TOKEN_STATUS_EXHAUSTED:
      return { label: labels.exhausted, tone: 'danger' }
    default:
      return { label: labels.unknown, tone: 'neutral' }
  }
}

/**
 * A token is expired when the upstream status says so (3) or when its
 * expired_time has passed. The status flag is not refreshed on every read, so
 * the timestamp is authoritative too.
 */
export function isTokenExpired(
  token: Pick<TokenRecord, 'status' | 'expired_time'>,
  nowSeconds: number = Math.floor(Date.now() / 1000),
): boolean {
  return (
    token.status === TOKEN_STATUS_EXPIRED ||
    (token.expired_time > 0 && token.expired_time <= nowSeconds)
  )
}

export function strategyLabel(strategy: Strategy | null | undefined): string | null {
  if (!strategy) return null
  return copy.pages.consoleKeys.strategy[strategy] ?? null
}

export function strategyHint(strategy: Strategy | null | undefined): string | null {
  if (!strategy) return null
  return copy.pages.consoleKeys.strategyHint[strategy] ?? null
}

export function ledgerEntryLabel(entryType: string): string {
  const labels = copy.pages.consoleWallet.ledger.types as Record<string, string>
  return labels[entryType] ?? labels.unknown
}

export function ledgerStatusLabel(status: string): string {
  const labels = copy.pages.consoleWallet.ledger.status as Record<string, string>
  return labels[status] ?? labels.unknown
}
