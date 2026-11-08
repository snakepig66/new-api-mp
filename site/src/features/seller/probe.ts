/**
 * Probe presentation for 我的渠道.
 *
 * The backend records a probe's `latency_ms` as the /models round-trip, not
 * the model call: it is tens to hundreds of milliseconds and repeats almost
 * exactly on every run. The real response time of a channel lives in each
 * probe's `model_results`, so the health card and the history table read the
 * slowest listed model instead and only fall back to the round-trip for old
 * patrol rows written before model_results were stored.
 */

import { copy } from '@/copy'

import type { SellerHealthProbe, SellerProbe, VerifyModelResult } from './api'

const probeTypes = copy.pages.sellerListings.health.probeTypes

/** Chinese label for a probe type; unknown types stay raw. */
export function probeTypeLabel(type: string): string {
  return probeTypes[type] ?? type
}

function slowestModelLatency(results: VerifyModelResult[] | null | undefined): number {
  let slowest = 0
  for (const result of results ?? []) {
    if (result.ok && result.latency_ms > slowest) slowest = result.latency_ms
  }
  return slowest
}

/** Displayed latency for the newest probe on a health card, in ms. */
export function healthProbeLatencyMs(
  probe: SellerHealthProbe | null | undefined,
): number | null {
  if (!probe) return null
  const slowest = slowestModelLatency(probe.model_results)
  if (slowest > 0) return slowest
  return probe.latency_ms > 0 ? probe.latency_ms : null
}

/** Displayed latency for one detail-history row, whose results sit in `detail`. */
export function historyProbeLatencyMs(probe: SellerProbe): number | null {
  const detail = probe.detail as { model_results?: VerifyModelResult[] } | null
  const slowest = slowestModelLatency(detail?.model_results)
  if (slowest > 0) return slowest
  return probe.latency_ms > 0 ? probe.latency_ms : null
}
