/**
 * Display-only money formatting.
 *
 * The backend always sends money as integer micro-yuan (1 元 = 1_000_000 微元).
 * This module exists so no feature ever does arithmetic on those integers:
 * format with formatYuan() and render the string.
 *
 * Accepted inputs:
 *  - number: safe integers only (larger JSON numbers lose precision)
 *  - bigint: preferred when the backend sends int64 as a string
 *  - string: a decimal integer, e.g. "1234567"
 *
 * Invalid input renders as an em dash rather than throwing, so a bad field
 * never breaks a whole table.
 */

export type MicroAmount = number | string | bigint

const MICRO_PER_YUAN = 1_000_000n
const INTEGER_RE = /^[+-]?\d+$/

function toMicros(value: MicroAmount): bigint | null {
  if (typeof value === 'bigint') return value
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || !Number.isInteger(value) || !Number.isSafeInteger(value)) {
      return null
    }
    return BigInt(value)
  }
  const text = value.trim()
  if (!INTEGER_RE.test(text)) return null
  try {
    return BigInt(text)
  } catch {
    return null
  }
}

/**
 * Format integer micro-yuan as a display string: 420000 -> "¥0.42".
 *
 * Rules: six fraction digits are kept at most, trailing zeros are trimmed but
 * at least two decimals remain; the integer part is grouped with commas;
 * exact zero renders "¥0"; invalid input renders "—".
 *
 * Display only — never parse the result back or do arithmetic with it.
 */
export function formatYuan(value: MicroAmount): string {
  const micros = toMicros(value)
  if (micros === null) return '—'
  if (micros === 0n) return '¥0'

  const negative = micros < 0n
  const absolute = negative ? -micros : micros
  const yuan = absolute / MICRO_PER_YUAN
  const remainder = absolute % MICRO_PER_YUAN

  let fraction = remainder.toString().padStart(6, '0').replace(/0+$/, '')
  if (fraction.length === 0) fraction = '00'
  if (fraction.length === 1) fraction += '0'

  const grouped = yuan.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${negative ? '-' : ''}¥${grouped}.${fraction}`
}
