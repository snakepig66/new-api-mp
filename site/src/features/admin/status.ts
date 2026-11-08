/**
 * 运营后台状态值 → 中文标签 / 色调。组件里不写状态映射。
 */

import { copy } from '@/copy'
import type { TagTone } from '@/ui'

export interface AdminStatusView {
  label: string
  tone: TagTone
}

export function adminListingStatus(status: string): AdminStatusView {
  const labels = copy.pages.adminListings.statusLabels
  switch (status) {
    case 'draft':
      return { label: labels.draft, tone: 'neutral' }
    case 'verifying':
      return { label: labels.verifying, tone: 'neutral' }
    case 'canary':
      return { label: labels.canary, tone: 'brand' }
    case 'live':
      return { label: labels.live, tone: 'brand' }
    case 'paused':
      return { label: labels.paused, tone: 'neutral' }
    case 'banned':
      return { label: labels.banned, tone: 'danger' }
    default:
      return { label: labels.unknown, tone: 'neutral' }
  }
}

export function adminPayoutStatus(status: string): AdminStatusView {
  const labels = copy.pages.adminPayouts.statusLabels
  switch (status) {
    case 'pending':
      return { label: labels.pending, tone: 'neutral' }
    case 'approved':
      return { label: labels.approved, tone: 'brand' }
    case 'paid':
      return { label: labels.paid, tone: 'brand' }
    case 'rejected':
      return { label: labels.rejected, tone: 'danger' }
    default:
      return { label: labels.unknown, tone: 'neutral' }
  }
}

export function adminHolidayStatus(kind: string): AdminStatusView {
  const labels = copy.pages.adminHolidays.kind
  if (kind === 'holiday') return { label: labels.holiday, tone: 'neutral' }
  if (kind === 'workday') return { label: labels.workday, tone: 'brand' }
  return { label: copy.pages.adminCommon.none, tone: 'neutral' }
}
