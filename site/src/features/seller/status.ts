/**
 * 后端状态值 → 中文标签 / 色调。组件里不写状态映射。
 */

import { copy } from '@/copy'
import type { TagTone } from '@/ui'

import type { SellerIdentity, SellerProgress } from './api'

export interface StatusView {
  label: string
  tone: TagTone
}

const common = copy.pages.sellerCommon

export function listingStatusView(status: string): StatusView {
  switch (status) {
    case 'draft':
      return { label: common.status.draft, tone: 'neutral' }
    case 'verifying':
      return { label: common.status.verifying, tone: 'warning' }
    case 'canary':
      return { label: common.status.canary, tone: 'brand' }
    case 'live':
      return { label: common.status.live, tone: 'success' }
    case 'paused':
      return { label: common.status.paused, tone: 'warning' }
    case 'banned':
      return { label: common.status.banned, tone: 'danger' }
    default:
      return { label: common.status.unknown, tone: 'neutral' }
  }
}

export function shareStatusView(status: string): StatusView {
  switch (status) {
    case 'available':
      return { label: common.status.available, tone: 'success' }
    case 'reversed':
    case 'failed':
      return { label: common.status.reversed, tone: 'danger' }
    case 'pending':
    case 'success':
      return { label: common.status.pending, tone: 'warning' }
    default:
      return { label: common.status.unknown, tone: 'neutral' }
  }
}

export function payoutStatusView(status: string): StatusView {
  switch (status) {
    case 'pending':
      return { label: common.status.payoutPending, tone: 'warning' }
    case 'approved':
      return { label: common.status.approved, tone: 'brand' }
    case 'paid':
      return { label: common.status.paid, tone: 'success' }
    case 'rejected':
      return { label: common.status.rejected, tone: 'danger' }
    default:
      return { label: common.status.unknown, tone: 'neutral' }
  }
}

/** Pause reason is either the literal "manual" or a probe fail_reason. */
export function pauseReasonView(reason: string | undefined | null): string {
  if (!reason) return copy.pages.sellerListings.noPauseReason
  if (reason === 'manual') return common.pauseReason.manual
  const lower = reason.toLowerCase()
  if (lower.includes('balance') || lower.includes('insufficient')) return common.pauseReason.balance
  if (
    lower.includes('key') ||
    lower.includes('401') ||
    lower.includes('unauthorized') ||
    lower.includes('models returned')
  ) {
    return common.pauseReason.key
  }
  return common.pauseReason.other
}

export function isListingPausable(status: string): boolean {
  return status === 'live'
}

export function isListingResumable(status: string): boolean {
  return status === 'paused'
}

export function isListingKeyReplaceable(status: string): boolean {
  return status !== 'banned'
}

export function isSellerActive(seller: SellerIdentity | undefined | null): boolean {
  if (!seller?.exists) return false
  if (seller.status === 'active') return true
  // An active row is expected to have next_step=done; treat that as active too
  // so a stale status field never locks the console.
  return seller.progress.next_step === 'done'
}

export function sellerStepIndex(progress: SellerProgress): number {
  switch (progress.next_step) {
    case 'agreement':
      return 0
    case 'real_name':
      return 1
    case 'payout_account':
      return 2
    default:
      return 3
  }
}

export function completedStepCount(progress: SellerProgress): number {
  let done = 0
  if (progress.agreement_signed) done += 1
  if (progress.real_name_verified) done += 1
  if (progress.payout_account_bound) done += 1
  return done
}
