import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { ShareRecord } from '@/features/seller/api'

import { ShareRecordsTable } from './ShareRecordsTable'

const record: ShareRecord = {
  request_id: 'req_seller_1',
  model: 'deepseek-v3',
  buyer_code: '7f3a9c21',
  tier: 'peak',
  tier_label: '高峰',
  channel_group: 'vendor-a',
  listing_id: 42,
  key_fingerprint4: 'abcd',
  input_tokens: 1_000,
  output_tokens: 500,
  cache_read_tokens: 200,
  cache_write_tokens: 100,
  search_count: 0,
  fetch_count: 0,
  tool_debit_micro: 0,
  seller_credit_micro: 18_600,
  buyer_paid_micro: 20_000,
  platform_fee_micro: 1_400,
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

describe('ShareRecordsTable', () => {
  it('renders the seller columns', () => {
    render(<ShareRecordsTable records={[record]} onOpen={() => {}} />)

    const headers = screen
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent?.replace(/\s+/g, ' ').trim())
    expect(headers).toEqual([
      '模型 ⇅',
      '渠道 ⇅',
      '买家 ⇅',
      'TOKENS ⇅',
      '耗时 ⇅',
      '费用 ⇅',
      '收益 ⇅',
      '详情 ⇅',
      '时间 ⇅',
    ])
  })

  it('renders the pseudonymised buyer, channel and key suffix', () => {
    const { container } = render(<ShareRecordsTable records={[record]} onOpen={() => {}} />)

    expect(container.textContent).toMatch(/7f3a9c21/i)
    expect(container.textContent).toContain('#42')
    expect(container.textContent).toContain('···abcd')
    expect(container.textContent).toContain('vendor-a')
  })

  it('renders tokens, latency, cost and income breakdown', () => {
    render(<ShareRecordsTable records={[record]} onOpen={() => {}} />)

    expect(screen.getByText('deepseek-v3')).toBeTruthy()
    expect(screen.getByText('转换')).toBeTruthy()
    expect(screen.getByText('1,000 / 500')).toBeTruthy()
    expect(screen.getByText('0.80s')).toBeTruthy()
    expect(screen.getByText('¥0.0186')).toBeTruthy()
    expect(screen.getByText(/实付 ¥0\.02 · 平台费 ¥0\.0014/)).toBeTruthy()
    expect(screen.getByText('消耗')).toBeTruthy()
  })

  it('falls back to a dash for a missing channel group and key suffix', () => {
    const plain: ShareRecord = {
      ...record,
      request_id: 'req_seller_2',
      channel_group: '',
      key_fingerprint4: '',
      first_token_ms: 0,
      is_stream: false,
      status: 'failed',
    }
    render(<ShareRecordsTable records={[plain]} onOpen={() => {}} />)

    const row = screen.getAllByRole('row')[1]
    expect(within(row).getAllByText('—').length).toBeGreaterThanOrEqual(2)
    expect(within(row).getByText('失败')).toBeTruthy()
    expect(within(row).queryByText(/···/)).toBeNull()
  })

  it('renders in_progress state with 进行中 badge, dash tokens, and green zero income', () => {
    const inProgress: ShareRecord = {
      ...record,
      request_id: 'req_seller_in_prog',
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      seller_credit_micro: 0,
      buyer_paid_micro: 0,
      platform_fee_micro: 0,
      latency_ms: 0,
      first_token_ms: 0,
      status: 'in_progress',
    }
    render(<ShareRecordsTable records={[inProgress]} onOpen={() => {}} />)

    const row = screen.getAllByRole('row')[1]
    expect(within(row).getByText('进行中')).toBeTruthy()
    expect(within(row).getByText('-')).toBeTruthy()
    expect(within(row).getByText('0.0s')).toBeTruthy()
    expect(within(row).getByText('¥ 0')).toBeTruthy()
    expect(within(row).getByText('¥0')).toBeTruthy()
    expect(within(row).getByText('消耗')).toBeTruthy()
    expect(within(row).queryByText(/实付/)).toBeNull()
  })
})
