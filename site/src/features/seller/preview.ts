/**
 * 挂单向导的价格预览。
 *
 * 这里只做「预估展示」：买家价 = ceil(官方输入价 × 倍率 / 1000)，到手价 =
 * 买家价 − 平台费（7%，四舍五入到分）。最终扣费与结算完全以后端 Quote /
 * SplitPlatformFee 为准。为保证与后端一致，全部使用整数 / BigInt 运算，
 * 不引入浮点误差。
 *
 * 这是全站唯一允许对 *_micro 做乘法的地方（预览），调用方只渲染结果。
 */

import type { MicroAmount } from '@/lib/money'

export interface PricePreview {
  /** Buyer debit in micro-yuan, rounded up like the backend Quote. */
  buyerMicro: bigint
  /** Platform fee: round-half-up of buyer × 7%. */
  feeMicro: bigint
  /** Seller credit: buyer − fee. */
  sellerMicro: bigint
  multiplierMilli: number
}

const MULTIPLIER_DEN = 1000n
const FEE_PERCENT = 7n

function toBigInt(value: MicroAmount): bigint | null {
  if (typeof value === 'bigint') return value < 0n ? null : value
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || !Number.isInteger(value) || !Number.isSafeInteger(value)) {
      return null
    }
    return value < 0 ? null : BigInt(value)
  }
  const text = value.trim()
  if (!/^\d+$/.test(text)) return null
  try {
    return BigInt(text)
  } catch {
    return null
  }
}

export function previewMultiplier(
  officialMicroPerMTok: MicroAmount,
  multiplierMilli: number,
): PricePreview | null {
  if (!Number.isInteger(multiplierMilli) || multiplierMilli < 1 || multiplierMilli > 1000) {
    return null
  }
  const official = toBigInt(officialMicroPerMTok)
  if (official === null) return null

  const scale = BigInt(multiplierMilli)
  const numerator = official * scale
  const buyerMicro =
    numerator === 0n ? 0n : (numerator + MULTIPLIER_DEN - 1n) / MULTIPLIER_DEN
  const feeMicro = (buyerMicro * FEE_PERCENT + 50n) / 100n
  const sellerMicro = buyerMicro - feeMicro

  return { buyerMicro, feeMicro, sellerMicro, multiplierMilli }
}

export type MultiplierComparison = 'higher' | 'lower' | 'same'

/** Compare the chosen multiplier with the market's lowest for the same model. */
export function compareMultiplier(
  chosenMilli: number,
  marketLowestMilli: number | null | undefined,
): MultiplierComparison | null {
  if (!Number.isInteger(chosenMilli) || marketLowestMilli === null || marketLowestMilli === undefined) {
    return null
  }
  if (!Number.isInteger(marketLowestMilli) || marketLowestMilli <= 0) return null
  if (chosenMilli === marketLowestMilli) return 'same'
  return chosenMilli > marketLowestMilli ? 'higher' : 'lower'
}
