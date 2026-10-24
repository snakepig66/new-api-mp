/**
 * Pure row mapping for the buyer usage-record table and detail dialog.
 * Display formatting (money/time) stays in the components; this module only
 * normalizes the API shape so it can be unit tested.
 */

import type { UsageRecord } from './types'

export interface UsageRow {
  requestId: string
  createdAt: string
  model: string
  sellerId: number
  tier: string
  tierLabel: string
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  cacheHitTokens: number
  searchCount: number
  fetchCount: number
  toolCalls: number
  toolDebitMicro: number
  officialCostMicro: number
  totalDebitMicro: number
  savedMicro: number
  status: string
  usageEstimated: boolean
}

function safeInt(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

/** Normalize one API row; missing numeric fields become 0 so rendering never crashes. */
export function toUsageRow(record: UsageRecord): UsageRow {
  const searchCount = safeInt(record.search_count)
  const fetchCount = safeInt(record.fetch_count)
  return {
    requestId: record.request_id ?? '',
    createdAt: record.created_at ?? '',
    model: record.model ?? '',
    sellerId: safeInt(record.seller_id),
    tier: record.tier ?? 'flat',
    tierLabel: record.tier_label ?? '',
    inputTokens: safeInt(record.input_tokens),
    outputTokens: safeInt(record.output_tokens),
    cacheReadTokens: safeInt(record.cache_read_tokens),
    cacheWriteTokens: safeInt(record.cache_write_tokens),
    cacheHitTokens: safeInt(record.cache_hit_tokens),
    searchCount,
    fetchCount,
    toolCalls: searchCount + fetchCount,
    toolDebitMicro: safeInt(record.tool_debit_micro),
    officialCostMicro: safeInt(record.official_cost_micro),
    totalDebitMicro: safeInt(record.total_debit_micro),
    savedMicro: safeInt(record.saved_micro),
    status: record.status ?? '',
    usageEstimated: Boolean(record.usage_estimated),
  }
}

/** A stable page label such as "第 2 / 5 页"; totalPages is at least 1. */
export function pageLabel(page: number, pageSize: number, total: number): string {
  const safeSize = pageSize > 0 ? pageSize : 1
  const totalPages = Math.max(1, Math.ceil(Math.max(0, total) / safeSize))
  return `${Math.min(Math.max(1, page), totalPages)} / ${totalPages}`
}
