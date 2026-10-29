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
      return { label: common.status.verifying, tone: 'neutral' }
    case 'canary':
      return { label: common.status.canary, tone: 'brand' }
    case 'live':
      return { label: common.status.live, tone: 'brand' }
    case 'paused':
      return { label: common.status.paused, tone: 'neutral' }
    case 'banned':
      return { label: common.status.banned, tone: 'danger' }
    default:
      return { label: common.status.unknown, tone: 'neutral' }
  }
}

export function shareStatusView(status: string): StatusView {
  switch (status) {
    case 'available':
      return { label: common.status.available, tone: 'brand' }
    case 'reversed':
    case 'failed':
      return { label: common.status.reversed, tone: 'danger' }
    case 'pending':
    case 'success':
      return { label: common.status.pending, tone: 'neutral' }
    default:
      return { label: common.status.unknown, tone: 'neutral' }
  }
}

export function payoutStatusView(status: string): StatusView {
  switch (status) {
    case 'pending':
      return { label: common.status.payoutPending, tone: 'neutral' }
    case 'approved':
      return { label: common.status.approved, tone: 'brand' }
    case 'paid':
      return { label: common.status.paid, tone: 'brand' }
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
  if (lower.includes('plan_quota_exhausted') || lower.includes('quota')) {
    return common.pauseReason.planQuota
  }
  if (lower.includes('migration')) return common.pauseReason.migration
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

/**
 * A seller may only lift their own pause. Platform pauses (plan quota window,
 * patrol failure, migration) carry a reason/resume_at and are restored by the
 * jobs, so showing 「恢复」 for them would only produce a 409.
 */
export function isListingSellerResumable(listing: {
  status: string
  pause_reason?: string
  resume_at?: string
}): boolean {
  if (listing.status !== 'paused') return false
  if (listing.pause_reason && listing.pause_reason !== 'manual') return false
  return !listing.resume_at
}

/** A draft listing is one whose verification has not passed (or failed). */
export function isListingReverifiable(listing: { status: string }): boolean {
  return listing.status === 'draft'
}

export function isListingKeyReplaceable(status: string): boolean {
  return status !== 'banned'
}

export function isSellerActive(seller: SellerIdentity | undefined | null): boolean {
  if (!seller?.exists) return false
  if (seller.status === 'active') return true
  // Sharing opens on the agreement alone, so an active row may still have the
  // optional real-name/payout steps left. next_step=done also counts as active
  // so a stale status field never locks the console.
  return seller.progress.next_step === 'done'
}

export function sellerStepIndex(progress: SellerProgress): number {
  switch (progress.next_step) {
    case 'agreement':
      return 0
    case 'real_name':
    case 'payout_account':
      return 1
    default:
      return 2
  }
}

export function completedStepCount(progress: SellerProgress): number {
  let done = 0
  if (progress.agreement_signed) done += 1
  if (progress.payout_account_bound) done += 1
  if (progress.agreement_signed && progress.payout_account_bound) done += 1
  return done
}

// ---------------------------------------------------------------------------
// 渠道健康度（P5）
// ---------------------------------------------------------------------------

export type ListingHealthLevel = 'healthy' | 'degraded' | 'offline' | 'unknown'

export interface ListingHealthLike {
  today_requests: number
  today_success_rate_milli: number
}

/**
 * Three-state health dot: ≥98% green(可用), ≥90% amber(降级), otherwise red
 * (离线). A card with no requests today has no samples and stays grey.
 */
export function listingHealthLevel(health: ListingHealthLike | null | undefined): ListingHealthLevel {
  if (!health || health.today_requests <= 0) return 'unknown'
  if (health.today_success_rate_milli >= 980) return 'healthy'
  if (health.today_success_rate_milli >= 900) return 'degraded'
  return 'offline'
}

/** "666" milli → "66.6%"; no sample → "—". */
export function successRateText(health: ListingHealthLike | null | undefined): string {
  if (!health || health.today_requests <= 0) return '—'
  const percent = health.today_success_rate_milli / 10
  return `${Number.isInteger(percent) ? percent : percent.toFixed(1)}%`
}
