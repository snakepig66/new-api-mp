import { afterEach, describe, expect, it, vi } from 'vitest'

import { buildRecordQuery, presetToRange } from './filters'

afterEach(() => {
  vi.useRealTimers()
})

describe('presetToRange', () => {
  it('starts today at local midnight and ends now', () => {
    // 2026-10-24 15:30 local time.
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 24, 15, 30, 0))

    const [start, end] = presetToRange('today')
    expect(end).toBe(Math.floor(new Date(2026, 9, 24, 15, 30, 0).getTime() / 1000))
    expect(start).toBe(Math.floor(new Date(2026, 9, 24, 0, 0, 0).getTime() / 1000))
  })

  it('rolls back 7 and 30 days from now in seconds', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 24, 15, 30, 0))
    const now = Math.floor(new Date(2026, 9, 24, 15, 30, 0).getTime() / 1000)

    expect(presetToRange('7d')).toEqual([now - 7 * 86_400, now])
    expect(presetToRange('30d')).toEqual([now - 30 * 86_400, now])
  })
})

describe('buildRecordQuery', () => {
  it('drops empty and non-positive filters', () => {
    expect(
      buildRecordQuery({
        start: 1_700_000_000,
        end: 1_700_086_400,
        model: '',
        token_id: 0,
        group: '',
        status: '',
        page: 1,
        page_size: 20,
      }),
    ).toEqual({
      start: 1_700_000_000,
      end: 1_700_086_400,
      page: 1,
      page_size: 20,
    })
  })

  it('keeps every provided filter, including an explicit stream=false', () => {
    expect(
      buildRecordQuery({
        model: 'deepseek-v3',
        token_id: 7,
        listing_id: 42,
        group: 'default',
        stream: false,
        status: 'failed',
      }),
    ).toEqual({
      model: 'deepseek-v3',
      token_id: 7,
      listing_id: 42,
      group: 'default',
      stream: false,
      status: 'failed',
    })
  })
})
