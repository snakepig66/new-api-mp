import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { UsageRecord } from '@/features/console/types'

import { BuyerRecordsTable } from './BuyerRecordsTable'

const record: UsageRecord = {
  request_id: 'req_buyer_1',
  model: 'deepseek-v3',
  seller_id: 10023,
  channel_type: 'official',
  tier: 'peak',
  tier_label: '高峰',
  token_group: 'default',
  token_name: 'Key 1',
  input_tokens: 1_000,
  output_tokens: 500,
  cache_read_tokens: 200,
  cache_write_tokens: 100,
  cache_hit_tokens: 200,
  search_count: 0,
  fetch_count: 0,
  tool_debit_micro: 0,
  multiplier_milli: 300,
  platform_fee_micro: 1_400,
  seller_credit_micro: 18_600,
  official_cost_micro: 900_000,
  total_debit_micro: 600_000,
  saved_micro: 300_000,
  saved_permille: 333,
  first_token_ms: 800,
  latency_ms: 12_400,
  is_stream: true,
  endpoint: 'openai-chat',
  converted: true,
  usage_estimated: false,
  status: 'success',
  created_at: '2026-10-24T10:00:00+08:00',
}

afterEach(cleanup)

describe('BuyerRecordsTable', () => {
  it('renders the buyer columns', () => {
    render(<BuyerRecordsTable records={[record]} onOpen={() => {}} />)

    const headers = screen
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent?.replace(/\s+/g, ' ').trim())
    expect(headers).toEqual([
      '模型 ⇅',
      '卖家 ⇅',
      '令牌 ⇅',
      'TOKENS ⇅',
      '耗时 ⇅',
      '费用 ⇅',
      '详情 ⇅',
      '时间 ⇅',
    ])
  })

  it('renders model, converted marker, tokens and cache subline', () => {
    render(<BuyerRecordsTable records={[record]} onOpen={() => {}} />)

    expect(screen.getByText('deepseek-v3')).toBeTruthy()
    expect(screen.getByText('转换')).toBeTruthy()
    expect(screen.getByText('1,000 / 500')).toBeTruthy()
    expect(screen.getByText(/缓存↓ 200/)).toBeTruthy()
    expect(screen.getByText(/↑ 100/)).toBeTruthy()
  })

  it('renders latency, cost and status without distracting savings', () => {
    const { container } = render(<BuyerRecordsTable records={[record]} onOpen={() => {}} />)

    expect(screen.getByText('0.80s')).toBeTruthy()
    expect(screen.getByText(/12\.4s/)).toBeTruthy()
    expect(screen.getByText('¥0.60')).toBeTruthy()
    expect(screen.queryByText('¥0.90')).toBeNull()
    expect(screen.queryByText('省 33.3%')).toBeNull()
    expect(screen.getByText('消耗')).toBeTruthy()
    expect(container.textContent).toContain('2026-10-24 10:00:00')
    expect(container.textContent).toContain('Key 1')
    expect(container.textContent).toContain('#10023')
  })

  it('shows a deleted token, dashes for missing values and a failed status', () => {
    const failed: UsageRecord = {
      ...record,
      request_id: 'req_buyer_2',
      token_name: '',
      token_group: '',
      tier_label: '',
      first_token_ms: 0,
      is_stream: false,
      converted: false,
      status: 'failed',
    }
    render(<BuyerRecordsTable records={[failed]} onOpen={() => {}} />)

    const row = screen.getAllByRole('row')[1]
    expect(within(row).getByText('已删除')).toBeTruthy()
    expect(within(row).getByText('失败')).toBeTruthy()
    expect(within(row).getAllByText('—').length).toBeGreaterThanOrEqual(2)
    expect(within(row).queryByText('转换')).toBeNull()
  })

  it('renders in_progress state with 进行中 badge, dash tokens, and 0.0s latency', () => {
    const inProgress: UsageRecord = {
      ...record,
      request_id: 'req_buyer_in_prog',
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      total_debit_micro: 0,
      latency_ms: 0,
      first_token_ms: 0,
      status: 'in_progress',
    }
    render(<BuyerRecordsTable records={[inProgress]} onOpen={() => {}} />)

    const row = screen.getAllByRole('row')[1]
    expect(within(row).getByText('进行中')).toBeTruthy()
    expect(within(row).getAllByText('-').length).toBe(2)
    expect(within(row).getByText('0.0s')).toBeTruthy()
    expect(within(row).getByText('¥ 0')).toBeTruthy()
    expect(within(row).getByText('消耗')).toBeTruthy()
  })
})
