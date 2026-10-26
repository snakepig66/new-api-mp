/**
 * Labels for the unified wallet stream (GET /wallet/transactions).
 *
 * Ledger statuses are the marketplace ones already used by the old ledger
 * table; top-up statuses come from the top-up section of the wallet copy.
 * Keeping the mapping here lets both the table and tests share it without
 * adding new keys to copy.ts.
 */

import { copy } from '@/copy'
import type { TagTone } from '@/ui'

import { ledgerStatusLabel, type StatusMeta } from './labels'
import type { WalletTransaction } from './types'

/** Type-column and tab label for a transaction kind. */
export function walletKindLabel(kind: string): string {
  switch (kind) {
    case 'usage':
      return '消费'
    case 'refund':
      return '退款'
    case 'topup':
      return '充值'
    default:
      return copy.pages.consoleWallet.ledger.types.unknown
  }
}

/**
 * Status label + tone. Ledger kinds reuse ledgerStatusLabel; top-up kinds reuse
 * the top-up status copy (pending/success/failed/expired/unknown).
 */
export function walletStatusMeta(kind: string, status: string): StatusMeta {
  if (kind === 'topup') {
    const labels = copy.pages.consoleWallet.topups.statuses as Record<string, string>
    const label = labels[status] ?? labels.unknown
    return { label, tone: topUpTone(status) }
  }
  const label = ledgerStatusLabel(status)
  return { label, tone: ledgerTone(status) }
}

export function walletStatusLabel(kind: string, status: string): string {
  return walletStatusMeta(kind, status).label
}

/**
 * Description column. A top-up shows the paid money (the beneficial amount is
 * already in the amount column); ledger rows describe their flow.
 */
export function walletDescription(tx: WalletTransaction): string {
  switch (tx.kind) {
    case 'topup':
      return tx.money !== undefined && tx.money !== null
        ? `在线充值 · 实付 ¥${Number(tx.money).toFixed(2)}`
        : '在线充值'
    case 'refund':
      return '退款入账'
    case 'usage':
      return '模型调用扣费'
    default:
      return copy.pages.consoleShared.none
  }
}

function topUpTone(status: string): TagTone {
  if (status === 'success') return 'brand'
  if (status === 'pending') return 'neutral'
  return 'danger'
}

function ledgerTone(status: string): TagTone {
  if (status === 'available') return 'brand'
  if (status === 'pending') return 'neutral'
  if (status === 'reversed') return 'danger'
  return 'neutral'
}
