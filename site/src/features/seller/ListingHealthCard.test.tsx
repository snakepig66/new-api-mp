import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { copy } from '@/copy'

import type { SellerListing, SellerListingHealth } from './api'
import { ListingHealthCard } from './ListingHealthCard'

const listing: SellerListing = {
  id: 7,
  vendor: 'codely',
  models: ['glm-5.3'],
  multiplier_milli: 500,
  concurrency_limit: 5,
  rpm_limit: 60,
  daily_cap_micro: 0,
  hours: '',
  status: 'live',
  key_fingerprint8: 'a1b2c3d4',
  created_at: '2026-10-26T14:13:47+08:00',
}

const health: SellerListingHealth = {
  listing_id: 7,
  latest_probe: {
    type: 'patrol',
    ok: true,
    latency_ms: 178,
    fail_reason: '',
    model_results: [
      {
        model: 'glm-5.3',
        upstream_model: 'codely-core',
        ok: true,
        prompt_cache_support: false,
        latency_ms: 1228,
      },
    ],
    created_at: '2026-10-26T18:39:36+08:00',
  },
  today_requests: 4,
  today_success_rate_milli: 1000,
  today_income_micro: 794,
}

afterEach(() => {
  cleanup()
})

describe('listing health card', () => {
  it('shows the model call latency and a non-repeating probe label', () => {
    render(<ListingHealthCard listing={listing} health={health} />)

    expect(screen.getByText('1228 ms')).toBeTruthy()
    expect(screen.queryByText('178 ms')).toBeNull()
    expect(screen.getByText(`${copy.pages.sellerListings.health.lastProbe} · 巡检`)).toBeTruthy()
  })

  it('renders the income block with the card header', () => {
    render(<ListingHealthCard listing={listing} health={health} />)

    expect(screen.getByText('今日收益')).toBeTruthy()
    expect(screen.getByText('¥0.000794')).toBeTruthy()
    expect(screen.getByText('成功率')).toBeTruthy()
  })
})
