/**
 * Console-only input parsing and display helpers.
 *
 * `lib/money.ts` is the single formatter for backend money; this file only
 * converts a human-typed yuan string into the integer 微元 the API expects
 * (form input, never display arithmetic). Multiplier caps are integer
 * thousandths (850 = 0.85x) and are not money.
 */

const MICRO_PER_YUAN = 1_000_000

/** "12.50" -> 12500000 微元; invalid or unsafe input -> null. */
export function parseYuanToMicro(raw: string): number | null {
  const text = raw.trim()
  if (!text) return null
  const match = /^(\d{1,12})(?:\.(\d{1,6}))?$/.exec(text)
  if (!match) return null
  const fraction = (match[2] ?? '').padEnd(6, '0')
  const micros = Number(match[1]) * MICRO_PER_YUAN + Number(fraction)
  if (!Number.isSafeInteger(micros)) return null
  return micros
}

/** 12500000 微元 -> "12.5" for an input field. */
export function microToYuanInput(micro: number): string {
  if (!Number.isFinite(micro) || !Number.isInteger(micro) || micro <= 0) return ''
  const whole = Math.floor(micro / MICRO_PER_YUAN)
  const fraction = String(micro % MICRO_PER_YUAN)
    .padStart(6, '0')
    .replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : String(whole)
}

/**
 * "0.85" -> 850 milli. Blank or "0" means no cap (0).
 * The accepted range is 0.001x through 1.000x; anything else is invalid.
 */
export function parseMultiplierToMilli(raw: string): number | null {
  const text = raw.trim()
  if (!text) return 0
  const match = /^(\d)(?:\.(\d{1,3}))?$/.exec(text)
  if (!match) return null
  const whole = Number(match[1])
  if (whole > 1) return null
  const fraction = (match[2] ?? '').padEnd(3, '0')
  const milli = whole * 1000 + Number(fraction)
  if (milli < 0 || milli > 1000) return null
  return milli
}

/** 850 -> "0.85"; 0 -> "" (an empty input means no cap). */
export function milliToMultiplierInput(milli: number): string {
  if (!Number.isFinite(milli) || !Number.isInteger(milli) || milli <= 0) return ''
  return (milli / 1000).toFixed(3).replace(/0+$/, '').replace(/\.$/, '')
}

export interface ParsedSellerIds {
  ids: number[]
  invalid: string[]
}

/**
 * Parse a list of 分享者编号 typed as "10023, 10024" (commas, full-width
 * commas, semicolons or whitespace). Duplicates collapse; signed ids and
 * unsafe integers are reported as invalid.
 */
export function parseSellerIds(raw: string): ParsedSellerIds {
  const ids: number[] = []
  const invalid: string[] = []
  const seen = new Set<number>()
  for (const token of raw.split(/[\s,，;；、]+/)) {
    if (!token) continue
    if (!/^\d{1,15}$/.test(token)) {
      invalid.push(token)
      continue
    }
    const value = Number(token)
    if (!Number.isSafeInteger(value) || value <= 0) {
      invalid.push(token)
      continue
    }
    if (seen.has(value)) continue
    seen.add(value)
    ids.push(value)
  }
  return { ids, invalid }
}

/** Group an integer with commas; invalid input renders an em dash. */
export function formatCount(value: number): string {
  if (!Number.isFinite(value) || !Number.isInteger(value)) return '—'
  return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

/** Pretty-print a JSON string for the retry-chain viewer. */
export function prettyJson(raw: string | null | undefined): string {
  const text = (raw ?? '').trim()
  if (!text) return ''
  try {
    return JSON.stringify(JSON.parse(text) as unknown, null, 2)
  } catch {
    return text
  }
}

/** Unix seconds -> the value datetime-local expects (browser local time). */
export function unixToDateTimeInput(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return ''
  const date = new Date(seconds * 1000)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** datetime-local value -> Unix seconds; blank or invalid -> null. */
export function dateTimeInputToUnix(raw: string): number | null {
  const text = raw.trim()
  if (!text) return null
  const millis = new Date(text).getTime()
  if (!Number.isFinite(millis)) return null
  return Math.floor(millis / 1000)
}
