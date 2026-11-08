/**
 * 后端错误文案的中文映射。
 *
 * 分享者接口用上游的 {success:false,message} 信封，多数错误没有稳定 code，
 * 只有英文 message。这里按语义把常见错误翻译成中文，其余回落到通用文案；
 * 不会把英文原文直接展示给用户。
 */

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'

const errors = copy.pages.sellerCommon.errors
const probeErrors = copy.pages.sellerCommon.probeErrors

const CJK = /[\u4e00-\u9fff]/

function isChinese(text: string): boolean {
  return CJK.test(text)
}

function match(raw: string, needles: string[]): boolean {
  const lower = raw.toLowerCase()
  return needles.some((needle) => lower.includes(needle))
}

/** The two duplicate-key outcomes the create/replace flows render differently. */
export type DuplicateKeyIssue =
  | { kind: 'own'; listingId?: number }
  | { kind: 'other' }

/**
 * Distinguishes "you already listed this key" (link to the listing) from
 * "another account listed it" (no identifiers). The backend's stable codes are
 * authoritative; message matching is only a fallback for older payloads.
 */
export function duplicateKeyIssue(error: unknown): DuplicateKeyIssue | null {
  if (!(error instanceof ApiError)) return null
  if (error.code === 'DUPLICATE_KEY_OWN') {
    return { kind: 'own', listingId: detailListingId(error.details) }
  }
  if (error.code === 'DUPLICATE_KEY_OTHER') return { kind: 'other' }

  const raw = error.message.toLowerCase()
  if (raw.includes('already listed by you')) {
    return { kind: 'own', listingId: detailListingId(error.details) }
  }
  if (raw.includes('already listed by another')) return { kind: 'other' }
  return null
}

function detailListingId(details: Record<string, unknown> | undefined): number | undefined {
  const value = details?.listing_id
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && /^\d+$/.test(value)) return Number(value)
  return undefined
}

export function sellerErrorMessage(error: unknown): string {
  const apiError = error instanceof ApiError ? error : null
  const raw = apiError?.message ?? (error instanceof Error ? error.message : '')

  if (apiError?.code === 'DUPLICATE_KEY_OWN') return errors.duplicateKeyOwn
  if (apiError?.code === 'DUPLICATE_KEY_OTHER') return errors.duplicateKeyOther
  if (apiError?.code === 'PLAN_TERMS_REQUIRED') return errors.planTermsRequired

  if (raw && isChinese(raw)) return raw

  if (match(raw, ['multiplier was changed less than 10 minutes', 'too soon'])) {
    return errors.multiplierCooldown
  }
  if (match(raw, ['multiplier must be 1..1000'])) return errors.multiplierRange
  if (match(raw, ['below the minimum', 'below minimum'])) return errors.payoutMinimum
  if (match(raw, ['once per week', 'too frequent'])) return errors.payoutWeekly
  if (match(raw, ['insufficient available balance'])) return errors.payoutInsufficient
  if (match(raw, ['idempotency key already exists', 'duplicate idempotency'])) {
    return errors.payoutDuplicate
  }
  if (match(raw, ['already listed', 'duplicate key'])) return errors.duplicateKey
  if (match(raw, ['payout account is not bound'])) return errors.payoutAccountRequired
  if (match(raw, ['seller is not active', 'not active'])) return errors.notActive
  if (match(raw, ['vendor is not whitelisted', 'not whitelisted'])) return errors.vendorUnknown
  if (match(raw, ['not served by this vendor', 'not served by vendor'])) return errors.modelUnknown
  if (match(raw, ['verification is busy'])) return errors.probeBusy
  if (match(raw, ['masked id', 'id_card_masked'])) return errors.idCard
  if (match(raw, ['alipay', 'payout account'])) return errors.alipay
  if (match(raw, ['real name'])) return errors.realName
  if (match(raw, ['api_key is required'])) return errors.keyRequired
  if (match(raw, ['at least one model'])) return errors.modelsRequired
  if (match(raw, ['hours'])) return errors.hours
  if (match(raw, ['listing not found'])) return errors.listingNotFound
  if (match(raw, ['verification is already running'])) return errors.verifyAlreadyRunning
  if (match(raw, ['only a draft listing can be re-verified'])) return errors.verifyDraftOnly

  if (apiError) {
    if (apiError.status === 404) return errors.listingNotFound
    if (apiError.status === 429) return errors.probeBusy
    if (apiError.status === 403) return errors.notActive
    const byCode = copy.errors[apiError.code as keyof typeof copy.errors]
    if (byCode) return byCode
  }

  return errors.generic
}

/** Probe fail_reason is English (and may mention the upstream status). */
export function probeFailReasonLabel(reason: string | undefined | null): string {
  if (!reason) return probeErrors.generic
  if (isChinese(reason)) return reason
  if (match(reason, ['401', '403', 'unauthorized', 'invalid api key'])) {
    return probeErrors.rejected
  }
  if (match(reason, ['balance', 'insufficient', 'quota'])) {
    return probeErrors.balance
  }
  if (match(reason, ['timeout', 'deadline', 'context deadline'])) {
    return probeErrors.timeout
  }
  if (match(reason, ['malformed', 'json', 'decode'])) {
    return probeErrors.malformed
  }
  if (match(reason, ['request failed', 'dial', 'connection'])) {
    return probeErrors.network
  }
  return probeErrors.generic
}

/**
 * Per-model verification reason. The backend may already return Chinese
 * (notably "该模型不在这个 Key 的可用模型列表中"), so those are mapped to the
 * explicit /models explanation first; everything else falls back to the
 * probe-error translation.
 */
export function modelVerifyReason(reason: string | undefined | null): string {
  if (!reason) return probeErrors.generic
  if (isChinese(reason)) {
    if (reason.includes('可用模型列表') || reason.includes('/models')) {
      return probeErrors.modelMissing
    }
    if (reason.includes('未能读取')) return probeErrors.modelsUnreadable
    return reason
  }
  if (
    match(reason, ['model list', 'models list', 'models returned', 'not in this key', 'not available'])
  ) {
    return probeErrors.modelMissing
  }
  return probeFailReasonLabel(reason)
}
