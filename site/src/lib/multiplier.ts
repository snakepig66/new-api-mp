/**
 * Multiplier display. The backend stores multipliers as integer milli
 * (300 = 0.30x). This is display formatting only — no price arithmetic.
 */

/** 300 -> "0.30", 875 -> "0.875", 1000 -> "1.00". */
export function formatMultiplier(milli: number): string {
  if (!Number.isFinite(milli) || !Number.isInteger(milli)) return '—'
  const negative = milli < 0
  const absolute = Math.abs(milli)
  const whole = Math.floor(absolute / 1000)
  let fraction = String(absolute % 1000).padStart(3, '0').replace(/0+$/, '')
  if (fraction.length < 2) fraction = fraction.padEnd(2, '0')
  return `${negative ? '-' : ''}${whole}.${fraction}`
}

/** 300 -> "70", 875 -> "12.5", 1000 -> "0". Percent saved versus official price. */
export function savingPercent(milli: number): string {
  if (!Number.isFinite(milli) || !Number.isInteger(milli)) return '—'
  const percent = (1000 - milli) / 10
  return percent.toFixed(1).replace(/\.0$/, '')
}
