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

export function sellerErrorMessage(error: unknown): string {
  const apiError = error instanceof ApiError ? error : null
  const raw = apiError?.message ?? (error instanceof Error ? error.message : '')

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
