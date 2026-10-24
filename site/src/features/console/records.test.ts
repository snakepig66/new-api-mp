import { describe, expect, it } from 'vitest'

import { pageLabel, toUsageRow } from './records'
import type { UsageRecord } from './types'

const record: UsageRecord = {
  request_id: 'req_123',
  model: 'deepseek-v3',
  seller_id: 10023,
  tier: 'peak',
  tier_label: '高峰',
  input_tokens: 1200,
  output_tokens: 340,
  cache_read_tokens: 50,
  cache_write_tokens: 10,
  cache_hit_tokens: 50,
  search_count: 2,
  fetch_count: 1,
  tool_debit_micro: 3000,
  official_cost_micro: 900_000,
  total_debit_micro: 600_000,
  saved_micro: 300_000,
  usage_estimated: false,
  status: 'success',
  created_at: '2026-10-24T10:00:00+08:00',
}

describe('toUsageRow', () => {
  it('maps API fields and combines tool call counts', () => {
    const row = toUsageRow(record)
    expect(row.requestId).toBe('req_123')
    expect(row.sellerId).toBe(10023)
    expect(row.toolCalls).toBe(3)
    expect(row.toolDebitMicro).toBe(3000)
    expect(row.savedMicro).toBe(300_000)
    expect(row.usageEstimated).toBe(false)
  })

  it('defaults missing numeric fields to zero', () => {
    const partial = {
      ...record,
      input_tokens: undefined,
      search_count: undefined,
      fetch_count: undefined,
      tool_debit_micro: undefined,
    } as unknown as UsageRecord
    const row = toUsageRow(partial)
    expect(row.inputTokens).toBe(0)
    expect(row.searchCount).toBe(0)
    expect(row.toolCalls).toBe(0)
    expect(row.toolDebitMicro).toBe(0)
  })
})

describe('pageLabel', () => {
  it('renders the current page against the computed total', () => {
    expect(pageLabel(2, 20, 45)).toBe('2 / 3')
    expect(pageLabel(9, 20, 45)).toBe('3 / 3')
    expect(pageLabel(1, 20, 0)).toBe('1 / 1')
  })
})
