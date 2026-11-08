import { describe, expect, it } from 'vitest'

import { listingHealthLevel, successRateText } from './status'

describe('listingHealthLevel', () => {
  it('treats a listing without samples as grey (unknown)', () => {
    expect(listingHealthLevel(undefined)).toBe('unknown')
    expect(listingHealthLevel(null)).toBe('unknown')
    expect(listingHealthLevel({ today_requests: 0, today_success_rate_milli: 1000 })).toBe(
      'unknown',
    )
  })

  it('maps ≥98% to healthy and ≥90% to degraded', () => {
    expect(listingHealthLevel({ today_requests: 1, today_success_rate_milli: 1000 })).toBe(
      'healthy',
    )
    expect(listingHealthLevel({ today_requests: 10, today_success_rate_milli: 980 })).toBe(
      'healthy',
    )
    expect(listingHealthLevel({ today_requests: 10, today_success_rate_milli: 979 })).toBe(
      'degraded',
    )
    expect(listingHealthLevel({ today_requests: 10, today_success_rate_milli: 900 })).toBe(
      'degraded',
    )
  })

  it('maps anything below 90% to offline', () => {
    expect(listingHealthLevel({ today_requests: 10, today_success_rate_milli: 899 })).toBe(
      'offline',
    )
    expect(listingHealthLevel({ today_requests: 1, today_success_rate_milli: 0 })).toBe(
      'offline',
    )
  })
})

describe('successRateText', () => {
  it('renders milli as a percent and keeps one decimal when needed', () => {
    expect(successRateText({ today_requests: 3, today_success_rate_milli: 666 })).toBe('66.6%')
    expect(successRateText({ today_requests: 2, today_success_rate_milli: 950 })).toBe('95%')
    expect(successRateText({ today_requests: 1, today_success_rate_milli: 1000 })).toBe('100%')
  })

  it('renders an em dash when there are no samples', () => {
    expect(successRateText(undefined)).toBe('—')
    expect(successRateText({ today_requests: 0, today_success_rate_milli: 0 })).toBe('—')
  })
})
