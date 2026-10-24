/**
 * 改倍率的 10 分钟冷却。
 *
 * 列表/详情接口目前不回传 multiplier_updated_at，因此客户端在成功改价或收到
 * 409 时把「冷却截止时间」记在 localStorage，用于展示倒计时和禁用提交。
 * 服务端仍是最终裁判：即使本地没有记录，409 也会被翻译成中文提示。
 */

export const MULTIPLIER_COOLDOWN_MS = 10 * 60 * 1000

const STORAGE_PREFIX = 'mp:seller:multiplier-cooldown:'

function storage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    return null
  }
}

function keyOf(listingId: number): string {
  return `${STORAGE_PREFIX}${listingId}`
}

export function readCooldownUntil(listingId: number): number {
  const store = storage()
  if (!store) return 0
  const raw = store.getItem(keyOf(listingId))
  if (!raw) return 0
  const until = Number(raw)
  return Number.isFinite(until) && until > 0 ? until : 0
}

export function markMultiplierChanged(listingId: number, now = Date.now()): number {
  const until = now + MULTIPLIER_COOLDOWN_MS
  storage()?.setItem(keyOf(listingId), String(until))
  return until
}

export function remainingSeconds(until: number, now = Date.now()): number {
  if (!Number.isFinite(until) || until <= now) return 0
  return Math.ceil((until - now) / 1000)
}

/** Clear the local timer (used by tests and after a fresh window). */
export function clearCooldown(listingId: number): void {
  storage()?.removeItem(keyOf(listingId))
}
