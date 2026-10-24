/**
 * 运营后台错误文案。
 *
 * admin 接口用上游信封，错误 message 是英文；这里按语义翻译成中文，
 * 状态码兜底，绝不把英文原文直接展示给用户。
 */

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'

const errors = copy.pages.adminCommon.errors
const CJK = /[\u4e00-\u9fff]/

function match(raw: string, needles: string[]): boolean {
  const lower = raw.toLowerCase()
  return needles.some((needle) => lower.includes(needle))
}

export function adminErrorMessage(error: unknown): string {
  const apiError = error instanceof ApiError ? error : null
  const raw = apiError?.message ?? (error instanceof Error ? error.message : '')
  if (raw && CJK.test(raw)) return raw

  if (match(raw, ['root privileges', 'root auth'])) return errors.forbidden
  if (match(raw, ['effective_at must not be earlier'])) return errors.effectivePast
  if (match(raw, ['price version already exists', 'per-call price version already exists'])) {
    return errors.versionExists
  }
  if (match(raw, ['tier must be'])) return errors.tierInvalid
  if (match(raw, ['prices must be >= 0', 'unit_price_micro must be >= 0'])) {
    return errors.priceNegative
  }
  if (match(raw, ['model, vendor and tier are required'])) return errors.invalidBody
  if (match(raw, ['effective_at must be rfc3339'])) return errors.invalidBody
  if (match(raw, ['item must be search or fetch'])) return errors.invalidBody
  if (match(raw, ['vendor is required'])) return errors.invalidBody
  if (match(raw, ['calendar reload failed'])) return errors.calendarReload
  if (match(raw, ['invalid kind', 'kind must be'])) return errors.holidayKind
  if (match(raw, ['invalid date', 'date must be'])) return errors.holidayDate
  if (match(raw, ['year must be yyyy'])) return errors.invalidBody
  if (match(raw, ['administrators cannot review their own payout', 'operate on their own payout'])) {
    return errors.selfReview
  }
  if (match(raw, ['payout is not in a pending state'])) return errors.payoutNotPending
  if (match(raw, ['payout not found'])) return errors.payoutNotFound
  if (match(raw, ['not reversible'])) return errors.notReversible
  if (match(raw, ['already reversed'])) return errors.alreadyReversed
  if (match(raw, ['no buyer matches this code'])) return errors.deanonNotFound
  if (match(raw, ['request_id is required'])) return errors.requestIdRequired
  if (match(raw, ['invalid request body'])) return errors.invalidBody
  if (match(raw, ['marketplace is not initialized'])) return errors.generic

  if (apiError) {
    if (apiError.status === 403) return errors.forbidden
    if (apiError.status === 404) return errors.notFound
    if (apiError.status === 409) return errors.conflict
    const mapped = copy.errors[apiError.code as keyof typeof copy.errors]
    if (mapped) return mapped
  }
  return errors.generic
}
