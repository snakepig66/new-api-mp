import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { SquareModel, SquareModelDetail } from './api'
import { formatCompactYuan, ModelCard } from './ModelCard'

// Mock clipboard
vi.mock('./clipboard', () => ({
  copyText: vi.fn().mockResolvedValue(true),
}))

// Mock Link from tanstack router
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, className, ...props }: any) => (
    <a href={`/models/${params?.id ?? ''}`} className={className} {...props}>
      {children}
    </a>
  ),
}))

describe('ModelCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('formats compact yuan properly', () => {
    expect(formatCompactYuan(1_000_000)).toBe('¥1')
    expect(formatCompactYuan(4_000_000)).toBe('¥4')
    expect(formatCompactYuan(200_000)).toBe('¥0.2')
    expect(formatCompactYuan(140_000)).toBe('¥0.14')
    expect(formatCompactYuan(560_000)).toBe('¥0.56')
    expect(formatCompactYuan(28_000)).toBe('¥0.028')
    expect(formatCompactYuan(958_904)).toBe('¥0.958904')
    expect(formatCompactYuan(0)).toBe('¥0')
  })

  it('renders discount pricing with strikethrough official price and discount percentage badge', () => {
    const model: SquareModel = {
      model: 'deepseek-v4.1-flash',
      origin_vendor: 'deepseek',
      vendor: 'deepseek',
      prices: {
        flat: {
          input_micro_per_mtok: 140_000,
          output_micro_per_mtok: 560_000,
          cache_read_micro_per_mtok: 28_000,
          cache_write_micro_per_mtok: 0,
        },
      },
      listing_count: 3,
      channel_count: 3,
      buyer_price_source: 'market',
      generated_at: '2026-11-01T00:00:00Z',
      capabilities: ['text', 'vision'],
      metrics: {
        status: 'available',
        success_rate_1h_milli: 1000,
        success_rate_24h_milli: 10000,
        success_rate_7d_milli: 10000,
        requests_24h: 500,
        latency_ms: 1180,
        tps_permille: 119800,
        cache_hit_permille: 0,
        last_success_at: null,
        sampled_at: '2026-11-01T00:00:00Z',
      },
    }

    const detail: SquareModelDetail = {
      ...model,
      multiplier_min_milli: 800,
      multiplier_max_milli: 1000,
      official_prices: {
        flat: {
          version: 1,
          input_micro_per_mtok: 1_000_000,
          output_micro_per_mtok: 4_000_000,
          cache_read_micro_per_mtok: 200_000,
          cache_write_micro_per_mtok: 0,
        },
      },
      channels: [],
    }

    render(<ModelCard model={model} detail={detail} />)

    expect(screen.getByText('deepseek-v4.1-flash')).toBeDefined()
    expect(screen.getByText('DeepSeek')).toBeDefined()

    // Official prices strikethrough
    expect(screen.getByText('¥1')).toBeDefined()
    expect(screen.getByText('¥4')).toBeDefined()
    expect(screen.getByText('¥0.2')).toBeDefined()

    // Discount badges: (1 - 0.14/1) * 100 = 86%
    const badges = screen.getAllByText('-86%')
    expect(badges.length).toBe(3)

    // Latency & Throughput footer
    expect(screen.getByText(/延迟 1.18s · 吞吐 119.8t\/s/)).toBeDefined()
  })

  it('renders dynamic pricing when no official benchmark exists', () => {
    const model: SquareModel = {
      model: 'deepseek-flash',
      origin_vendor: 'deepseek',
      vendor: 'deepseek',
      prices: {
        flat: {
          input_micro_per_mtok: 958_904,
          output_micro_per_mtok: 3_835_600,
          cache_read_micro_per_mtok: 19_178,
          cache_write_micro_per_mtok: 0,
        },
      },
      listing_count: 1,
      channel_count: 1,
      buyer_price_source: 'market',
      generated_at: '2026-11-01T00:00:00Z',
    }

    render(<ModelCard model={model} />)

    expect(screen.getByText('deepseek-flash')).toBeDefined()
    expect(screen.getByText('动态计费')).toBeDefined()
    expect(screen.getByText('¥0.958904')).toBeDefined()
    expect(screen.getByText('¥3.8356')).toBeDefined()
    expect(screen.getByText('¥0.019178')).toBeDefined()
  })

  it('copies model name on copy button click', async () => {
    const model: SquareModel = {
      model: 'glm-5.3',
      origin_vendor: 'zhipu',
      vendor: 'zhipu',
      prices: {
        flat: {
          input_micro_per_mtok: 240_000,
          output_micro_per_mtok: 840_000,
          cache_read_micro_per_mtok: 60_000,
          cache_write_micro_per_mtok: 0,
        },
      },
      listing_count: 2,
      channel_count: 2,
      buyer_price_source: 'market',
      generated_at: '2026-11-01T00:00:00Z',
    }

    render(<ModelCard model={model} />)

    const copyBtn = screen.getByRole('button', { name: '复制模型名' })
    fireEvent.click(copyBtn)

    const { copyText } = await import('./clipboard')
    expect(copyText).toHaveBeenCalledWith('glm-5.3')
  })
})
