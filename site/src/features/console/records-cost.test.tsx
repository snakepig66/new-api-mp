import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import {
  RecordDetailBody,
  channelTypeLabel,
  formatPriceRatio,
  formatUnitPrice,
} from '@/app/routes/console/records'

import type { UsageCostRecordDetail } from './api'

const detail: UsageCostRecordDetail = {
  request_id: 'req-cost-1',
  model: 'deepseek-v3',
  seller_id: 10023,
  channel_type: 'official',
  tier: 'peak',
  tier_label: '高峰',
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
  usage_estimated: false,
  status: 'success',
  created_at: '2026-10-24T10:00:00+08:00',
  retry_chain: '[{"seq":1,"event":"select"}]',
}

afterEach(cleanup)

describe('usage cost detail', () => {
  it('shows channel type, fees, price ratio and both unit prices', () => {
    const { container } = render(<RecordDetailBody detail={detail} />)

    expect(screen.getByText('官方渠道')).toBeTruthy()
    expect(screen.getByText('平台服务费')).toBeTruthy()
    expect(screen.getByText('分享者到手')).toBeTruthy()
    expect(screen.getByText('30%')).toBeTruthy()
    expect(screen.getByText('¥36.67')).toBeTruthy()
    expect(screen.getByText('¥11.11')).toBeTruthy()
    expect(screen.getByText('¥0.0014')).toBeTruthy()
    expect(screen.getByText('¥0.0186')).toBeTruthy()
    expect(screen.getByText(/select/)).toBeTruthy()

    for (const forbidden of ['微元', '倍率', '挂单', '系数']) {
      expect(container.textContent).not.toContain(forbidden)
    }
  })

  it('maps channel kinds and formats unit prices safely', () => {
    expect(channelTypeLabel('official')).toBe('官方渠道')
    expect(channelTypeLabel('plan')).toBe('套餐渠道')
    expect(channelTypeLabel('third_party')).toBe('第三方渠道')
    expect(channelTypeLabel(undefined)).toBe('分享渠道')
    expect(channelTypeLabel('vendor-x')).toBe('其他渠道')

    expect(formatUnitPrice(66_000, 1800)).toBe('¥36.67')
    expect(formatUnitPrice(20_000, 1800)).toBe('¥11.11')
    expect(formatUnitPrice(1_000, 0)).toBe('—')
    expect(formatUnitPrice(1_000, Number.NaN)).toBe('—')

    expect(formatPriceRatio(300)).toBe('30%')
    expect(formatPriceRatio(855)).toBe('85.5%')
    expect(formatPriceRatio(0)).toBe('—')
  })
})
