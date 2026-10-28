import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import {
  RecordDetailContent,
  formatLatency,
  parseRetryChain,
} from '@/features/records/RecordDetailDrawer'

import type { UsageCostRecordDetail } from './api'

const detail: UsageCostRecordDetail = {
  request_id: 'req-cost-1',
  model: 'deepseek-v3',
  upstream_model: 'deepseek-v3-0901',
  seller_id: 10023,
  channel_type: 'official',
  tier: 'peak',
  tier_label: '高峰',
  token_group: 'default',
  token_name: 'Key 1',
  input_tokens: 1000,
  output_tokens: 500,
  cache_read_tokens: 200,
  cache_write_tokens: 100,
  cache_hit_tokens: 200,
  search_count: 1,
  fetch_count: 2,
  tool_debit_micro: 15_000,
  multiplier_milli: 300,
  platform_fee_micro: 1_400,
  seller_credit_micro: 18_600,
  official_cost_micro: 66_000,
  total_debit_micro: 20_000,
  saved_micro: 46_000,
  saved_permille: 697,
  first_token_ms: 800,
  latency_ms: 12_400,
  is_stream: true,
  endpoint: 'openai-chat',
  converted: false,
  usage_estimated: false,
  status: 'success',
  created_at: '2026-10-24T10:00:00+08:00',
  retry_chain: JSON.stringify([
    { seq: 1, event: 'select', listing_id: 42, detail: '', at: '2026-10-24T02:00:00Z' },
    {
      seq: 2,
      event: 'error',
      listing_id: 42,
      detail: 'status=500 code=upstream_error',
      at: '2026-10-24T02:00:02Z',
    },
    { seq: 3, event: 'switch', listing_id: 43, detail: '', at: '2026-10-24T02:00:03Z' },
  ]),
}

afterEach(cleanup)

describe('record detail drawer', () => {
  it('renders the buyer sections and the retry timeline without raw JSON', () => {
    const { container } = render(<RecordDetailContent detail={detail} view="buyer" />)

    expect(screen.getByText('基本信息')).toBeTruthy()
    expect(screen.getByText('Token 与费用')).toBeTruthy()
    expect(screen.getByText('重试链')).toBeTruthy()
    expect(screen.getByText('req-cost-1')).toBeTruthy()
    expect(screen.getByText('deepseek-v3-0901')).toBeTruthy()
    expect(screen.getByText('Key 1')).toBeTruthy()
    expect(screen.getByText('官方价')).toBeTruthy()
    expect(screen.getByText('实付')).toBeTruthy()
    expect(screen.getByText('省 69.7%')).toBeTruthy()
    expect(screen.getByText('选择渠道')).toBeTruthy()
    expect(screen.getByText('请求失败')).toBeTruthy()
    expect(screen.getByText('HTTP 500')).toBeTruthy()
    expect(screen.getByText('切换渠道')).toBeTruthy()

    for (const raw of ['"seq"', 'listing_id', '{', '}', 'upstream_error']) {
      expect(container.textContent).not.toContain(raw)
    }
  })

  it('renders seller money and channel fields without a listing id in the chain', () => {
    const sellerDetail = {
      ...detail,
      buyer_code: '7F3A9C21',
      channel_group: 'vendor-a',
      listing_id: 42,
      key_fingerprint4: 'abcd',
      buyer_paid_micro: 20_000,
      channel_type: undefined,
    }
    render(<RecordDetailContent detail={sellerDetail} view="seller" />)

    expect(screen.getByText('买家实付')).toBeTruthy()
    expect(screen.getByText('平台费')).toBeTruthy()
    expect(screen.getByText('到手')).toBeTruthy()
    expect(screen.getByText(/Key ···abcd/)).toBeTruthy()
    expect(screen.getAllByText('本渠道')).toHaveLength(3)
  })
})

describe('retry chain parsing', () => {
  it('sorts by seq, derives step latency and labels events', () => {
    const hops = parseRetryChain(detail.retry_chain, 'buyer')
    expect(hops.map((hop) => hop.event)).toEqual(['select', 'error', 'switch'])
    expect(hops[0].latencyMs).toBe(2000)
    expect(hops[1].status).toBe('500')
    expect(hops[1].failed).toBe(true)
    expect(hops[2].channelLabel).toBe('#43')
  })

  it('returns no hops for empty or malformed chains', () => {
    expect(parseRetryChain('', 'buyer')).toEqual([])
    expect(parseRetryChain('not-json', 'buyer')).toEqual([])
    expect(parseRetryChain('[{"seq":1,"event":"error","detail":"{bad json}"}]', 'buyer')[0].message).toBeUndefined()
  })

  it('formats latency and treats unrecorded values as a dash', () => {
    expect(formatLatency(0)).toBe('—')
    expect(formatLatency(420)).toBe('0.42s')
    expect(formatLatency(3400)).toBe('3.4s')
  })
})
