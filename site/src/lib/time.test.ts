import { describe, expect, it } from 'vitest'

import { formatDate, formatDateTime, formatRelativeTime } from './time'

describe('time formatting helpers', () => {
  it('formats dates in Shanghai timezone', () => {
    // 2026-10-24 06:03:22 UTC = 2026-10-24 14:03:22 Asia/Shanghai
    const unix = 1792821802
    expect(formatDateTime(unix)).toBe('2026-10-24 14:03:22')
    expect(formatDate(unix)).toBe('2026-10-24')
  })

  it('formats relative time correctly', () => {
    const now = 1000_000_000
    const nowMs = now * 1000

    expect(formatRelativeTime(null, nowMs)).toBe('从未使用')
    expect(formatRelativeTime(0, nowMs)).toBe('从未使用')
    expect(formatRelativeTime(now - 10, nowMs)).toBe('刚刚')
    expect(formatRelativeTime(now - 300, nowMs)).toBe('5 分钟前')
    expect(formatRelativeTime(now - 7200, nowMs)).toBe('2 小时前')
    expect(formatRelativeTime(now - 86400 * 5, nowMs)).toBe('5 天前')
    expect(formatRelativeTime(now - 86400 * 65, nowMs)).toBe('2 个月前')
    expect(formatRelativeTime(now - 86400 * 400, nowMs)).toBe('1 年前')
  })
})
