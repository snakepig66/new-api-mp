/**
 * 表单里的「元」输入与后端微元之间的转换。
 *
 * 金额提交必须走后端整数微元；界面用元输入（日限额、提现）。这里用字符串
 * 解析，不经过浮点数，避免 0.1 + 0.2 一类误差。展示仍只走 formatYuan。
 */

import type { MicroAmount } from '@/lib/money'

const MICRO_PER_YUAN = 1_000_000n

/** "50.5" -> 50_500_000n; malformed / negative -> null. */
export function parseYuanToMicro(text: string): bigint | null {
  const value = text.trim().replace(/,/g, '')
  if (!value) return null
  const match = /^(\d+)(?:\.(\d{0,6}))?$/.exec(value)
  if (!match) return null
  const whole = BigInt(match[1])
  const fraction = (match[2] ?? '').padEnd(6, '0')
  return whole * MICRO_PER_YUAN + BigInt(fraction)
}

/**
 * 123_400_000 -> "123.4"; 50_000_000 -> "50". Used to prefill an input,
 * never to render a price.
 */
export function microToYuanInput(value: MicroAmount): string {
  let micros: bigint
  if (typeof value === 'bigint') {
    micros = value
  } else if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) return ''
    micros = BigInt(value)
  } else {
    if (!/^\d+$/.test(value.trim())) return ''
    micros = BigInt(value.trim())
  }
  if (micros <= 0n) return '0'
  const whole = micros / MICRO_PER_YUAN
  const fraction = (micros % MICRO_PER_YUAN).toString().padStart(6, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : `${whole}`
}
