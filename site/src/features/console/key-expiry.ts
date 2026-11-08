import { isTokenExpired } from './labels'
import type { ConsoleKey } from './types'

/** A key expiring inside this window gets the console banner. */
export const EXPIRY_WARNING_SECONDS = 3 * 24 * 60 * 60

/**
 * The soonest non-expired key expiring within the next three days, if any.
 * Shared by the console overview and the key list so both show one banner.
 */
export function soonestExpiringKey(
  keys: ConsoleKey[],
  nowSeconds: number = Math.floor(Date.now() / 1000),
): ConsoleKey | null {
  let soonest: ConsoleKey | null = null
  for (const item of keys) {
    const { token } = item
    if (token.expired_time <= 0 || isTokenExpired(token, nowSeconds)) continue
    if (token.expired_time > nowSeconds + EXPIRY_WARNING_SECONDS) continue
    if (!soonest || token.expired_time < soonest.token.expired_time) soonest = item
  }
  return soonest
}
