/**
 * Time display in Asia/Shanghai.
 *
 * Backend contract: timestamps are either Unix seconds (number or numeric
 * string) or RFC 3339 strings. All user-facing times are formatted in
 * Asia/Shanghai regardless of the browser timezone.
 */

export type ApiTime = Date | string | number

const SHANGHAI = 'Asia/Shanghai'

const formatter = new Intl.DateTimeFormat('en-US', {
  timeZone: SHANGHAI,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

function partsOf(input: ApiTime): Record<string, string> | null {
  const date = parseApiTime(input)
  if (!date) return null
  const parts: Record<string, string> = {}
  for (const part of formatter.formatToParts(date)) {
    if (part.type !== 'literal') parts[part.type] = part.value
  }
  return parts
}

/** Parse an API timestamp; returns null for empty or invalid values. */
export function parseApiTime(input: ApiTime): Date | null {
  if (input instanceof Date) {
    return Number.isNaN(input.getTime()) ? null : input
  }
  if (typeof input === 'number') {
    if (!Number.isFinite(input) || input <= 0) return null
    return new Date(input * 1000)
  }
  const text = input.trim()
  if (!text) return null
  if (/^\d+$/.test(text)) return parseApiTime(Number(text))
  const parsed = Date.parse(text)
  return Number.isNaN(parsed) ? null : new Date(parsed)
}

/** "2026-10-24 14:03:22" in Asia/Shanghai. */
export function formatDateTime(input: ApiTime): string {
  const parts = partsOf(input)
  if (!parts) return '—'
  return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}:${parts.second}`
}

/** "2026-10-24" in Asia/Shanghai. */
export function formatDate(input: ApiTime): string {
  const parts = partsOf(input)
  if (!parts) return '—'
  return `${parts.year}-${parts.month}-${parts.day}`
}

/** "14:03" in Asia/Shanghai. */
export function formatTime(input: ApiTime): string {
  const parts = partsOf(input)
  if (!parts) return '—'
  return `${parts.hour}:${parts.minute}`
}

/** "10-24" in Asia/Shanghai. */
export function formatMonthDay(input: ApiTime): string {
  const parts = partsOf(input)
  if (!parts) return '—'
  return `${parts.month}-${parts.day}`
}

/** Whole seconds until the target, clamped at zero. */
export function secondsUntil(input: ApiTime, now: number = Date.now()): number {
  const target = parseApiTime(input)
  if (!target) return 0
  return Math.max(0, Math.ceil((target.getTime() - now) / 1000))
}

/**
 * Countdown text. At or above one hour: "01:23:45"; below: "23:45".
 * Negative or invalid input renders "00:00".
 */
export function formatCountdown(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds <= 0) return '00:00'
  const seconds = Math.floor(totalSeconds)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60
  const mm = String(minutes).padStart(2, '0')
  const ss = String(rest).padStart(2, '0')
  if (hours <= 0) return `${mm}:${ss}`
  return `${String(hours).padStart(2, '0')}:${mm}:${ss}`
}

/**
 * Format relative time (e.g. "5 天前", "从未使用", "刚刚").
 */
export function formatRelativeTime(
  input: ApiTime | null | undefined,
  nowMs: number = Date.now(),
): string {
  if (!input) return '从未使用'
  const date = parseApiTime(input)
  if (!date) return '从未使用'
  const diffSeconds = Math.max(0, Math.floor((nowMs - date.getTime()) / 1000))
  if (diffSeconds < 60) return '刚刚'
  if (diffSeconds < 3600) return `${Math.floor(diffSeconds / 60)} 分钟前`
  if (diffSeconds < 86400) return `${Math.floor(diffSeconds / 3600)} 小时前`
  if (diffSeconds < 86400 * 30) return `${Math.floor(diffSeconds / 86400)} 天前`
  if (diffSeconds < 86400 * 365) return `${Math.floor(diffSeconds / (86400 * 30))} 个月前`
  return `${Math.floor(diffSeconds / (86400 * 365))} 年前`
}

