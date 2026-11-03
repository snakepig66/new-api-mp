/**
 * Latency colors mirror new-api's usage-log rules:
 *
 * - First token: < 5s green, 5–10s amber, ≥ 10s red.
 * - Total duration: with ≥ 100 output tokens, color by throughput
 *   (≥ 30 t/s green, ≥ 15 amber, below red); otherwise < 10s green,
 *   10–30s amber, ≥ 30s red. Small completions fall back to wall time so a
 *   short reply is not punished for having few tokens.
 *
 * Missing values stay muted instead of reading as a healthy zero.
 */

export type LatencyColor = {
  text: string
  bar: string
}

const NEUTRAL: LatencyColor = {
  text: 'text-ink-3',
  bar: 'bg-slate-300 dark:bg-slate-700',
}

const OK: LatencyColor = {
  text: 'text-ok',
  bar: 'bg-ok/90',
}

const WARN: LatencyColor = {
  text: 'text-warn',
  bar: 'bg-warn/80',
}

const DANGER: LatencyColor = {
  text: 'text-danger',
  bar: 'bg-danger/80',
}

/** 首字耗时（毫秒）→ 颜色。 */
export function getFirstTokenColor(ms: number): LatencyColor {
  if (ms <= 0) return NEUTRAL
  const seconds = ms / 1000
  if (seconds < 5) return OK
  if (seconds < 10) return WARN
  return DANGER
}

/** 总耗时（毫秒）→ 颜色；输出 token 足够多时按吞吐量判定。 */
export function getTotalLatencyColor(
  ms: number,
  completionTokens = 0
): LatencyColor {
  if (ms <= 0) return NEUTRAL
  const seconds = ms / 1000
  if (completionTokens < 100) {
    if (seconds < 10) return OK
    if (seconds < 30) return WARN
    return DANGER
  }
  const tokensPerSecond = completionTokens / seconds
  if (tokensPerSecond >= 30) return OK
  if (tokensPerSecond >= 15) return WARN
  return DANGER
}
