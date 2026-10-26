import { describe, expect, it } from 'vitest'

import { healthProbeLatencyMs, historyProbeLatencyMs, probeTypeLabel } from './probe'

const model = (latency_ms: number, ok = true) => ({
  model: 'deepseek-flash',
  upstream_model: 'deepseek-flash',
  ok,
  prompt_cache_support: false,
  latency_ms,
})

describe('probe presentation', () => {
  it('labels known probe types in Chinese and keeps unknown ones raw', () => {
    expect(probeTypeLabel('patrol')).toBe('巡检')
    expect(probeTypeLabel('verify')).toBe('验真')
    expect(probeTypeLabel('mystery')).toBe('mystery')
  })

  it('reads the slowest successful model call, not the /models round-trip', () => {
    expect(
      healthProbeLatencyMs({
        type: 'verify',
        ok: true,
        latency_ms: 12,
        fail_reason: '',
        model_results: [model(5782), model(940)],
        created_at: '2026-10-26T15:17:37+08:00',
      }),
    ).toBe(5782)
  })

  it('ignores failed models and falls back to the round-trip without samples', () => {
    expect(
      healthProbeLatencyMs({
        type: 'patrol',
        ok: false,
        latency_ms: 178,
        fail_reason: 'upstream 500',
        model_results: [model(0, false)],
        created_at: '2026-10-26T18:39:36+08:00',
      }),
    ).toBe(178)
    expect(
      healthProbeLatencyMs({
        type: 'patrol',
        ok: true,
        latency_ms: 220,
        fail_reason: '',
        model_results: [],
        created_at: '2026-10-26T18:39:36+08:00',
      }),
    ).toBe(220)
    expect(healthProbeLatencyMs(null)).toBeNull()
  })

  it('derives detail-history latency from the probe detail payload', () => {
    expect(
      historyProbeLatencyMs({
        id: 17,
        type: 'verify',
        ok: true,
        latency_ms: 999,
        detail: { model_results: [model(1228), model(1278)] },
        created_at: '2026-10-26T15:05:31+08:00',
      }),
    ).toBe(1278)
    expect(
      historyProbeLatencyMs({
        id: 23,
        type: 'patrol',
        ok: true,
        latency_ms: 178,
        detail: { key_valid: true, models: 5 },
        created_at: '2026-10-26T18:39:36+08:00',
      }),
    ).toBe(178)
    expect(
      historyProbeLatencyMs({
        id: 24,
        type: 'patrol',
        ok: true,
        latency_ms: 0,
        detail: null,
        created_at: '2026-10-26T18:39:36+08:00',
      }),
    ).toBeNull()
  })
})
