import { copy } from '@/copy'
import type { TagTone } from '@/ui'

import {
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  TOKEN_STATUS_EXHAUSTED,
  TOKEN_STATUS_EXPIRED,
  type Strategy,
} from './types'

export interface StatusMeta {
  label: string
  tone: TagTone
}

export function tokenStatusMeta(status: number): StatusMeta {
  const labels = copy.pages.consoleKeys.status
  switch (status) {
    case TOKEN_STATUS_ENABLED:
      return { label: labels.enabled, tone: 'success' }
    case TOKEN_STATUS_DISABLED:
      return { label: labels.disabled, tone: 'warning' }
    case TOKEN_STATUS_EXPIRED:
      return { label: labels.expired, tone: 'neutral' }
    case TOKEN_STATUS_EXHAUSTED:
      return { label: labels.exhausted, tone: 'danger' }
    default:
      return { label: labels.unknown, tone: 'neutral' }
  }
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
