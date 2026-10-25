import { describe, expect, it } from 'vitest'

import { PEAK_WINDOWS, shanghaiMinuteOfDay } from './peak-bar'

describe('peak windows', () => {
  it('uses the documented weekday windows', () => {
    expect(PEAK_WINDOWS).toEqual([
      { start: 540, end: 720 },
      { start: 840, end: 1080 },
    ])
  })

  it('reads the clock in Asia/Shanghai regardless of the host timezone', () => {
    // 01:00 UTC is 09:00 in Shanghai (UTC+8, no DST).
    expect(shanghaiMinuteOfDay(new Date('2026-01-05T01:00:00Z'))).toBe(540)
    // 16:30 UTC is 00:30 the next day in Shanghai.
    expect(shanghaiMinuteOfDay(new Date('2026-01-05T16:30:00Z'))).toBe(30)
  })
})
