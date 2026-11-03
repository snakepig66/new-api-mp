import { describe, expect, it } from 'vitest'

import { getFirstTokenColor, getTotalLatencyColor } from './latency-colors'

describe('latency colors (new-api rules)', () => {
  it('colors the first token by seconds', () => {
    expect(getFirstTokenColor(0).text).toBe('text-ink-3')
    expect(getFirstTokenColor(4999).text).toBe('text-ok')
    expect(getFirstTokenColor(5000).text).toBe('text-warn')
    expect(getFirstTokenColor(9999).text).toBe('text-warn')
    expect(getFirstTokenColor(10000).text).toBe('text-danger')
  })

  it('colors total duration by throughput once output reaches 100 tokens', () => {
    // 500 tokens / 12.4s ≈ 40.3 t/s
    expect(getTotalLatencyColor(12400, 500).text).toBe('text-ok')
    // 200 tokens / 10s = 20 t/s
    expect(getTotalLatencyColor(10000, 200).text).toBe('text-warn')
    // 100 tokens / 7.7s ≈ 13 t/s
    expect(getTotalLatencyColor(7700, 100).text).toBe('text-danger')
  })

  it('falls back to wall time for small completions', () => {
    expect(getTotalLatencyColor(9000, 50).text).toBe('text-ok')
    expect(getTotalLatencyColor(10000, 50).text).toBe('text-warn')
    expect(getTotalLatencyColor(30000, 0).text).toBe('text-danger')
    expect(getTotalLatencyColor(0, 500).text).toBe('text-ink-3')
  })
})
