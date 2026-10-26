import { describe, expect, it } from 'vitest'

import { copy } from './copy'

/**
 * Buyer/seller copy must not leak operator vocabulary. Admin copy is allowed
 * to keep 挂单 / 系数 / 微元 because those pages are internal.
 */
const FORBIDDEN = [
  '挂单',
  '系数',
  '微元',
  '倍率区间',
  '聚合数据',
  '不编造',
  '尚未接入',
  '接口错误',
  '格式转换',
  '运营工具',
]

const buyerBlocks = [
  copy.brand,
  copy.nav,
  copy.actions,
  copy.common,
  copy.tiers,
  copy.units,
  copy.price,
  copy.peak,
  copy.identity,
  copy.consoleShell,
  copy.pages.home,
  copy.pages.models,
  copy.pages.modelDetail,
  copy.pages.docs,
  copy.pages.login,
  copy.pages.forgotPassword,
  copy.pages.resetPassword,
  copy.pages.register,
  copy.pages.console,
  copy.pages.consoleShared,
  copy.pages.consoleKeys,
  copy.pages.consoleKeyDetail,
  copy.pages.consoleRecords,
  copy.pages.consoleAccount,
  copy.pages.consoleWallet,
]

const sellerBlocks = [
  copy.pages.seller,
  copy.pages.sellerCommon,
  copy.pages.sellerVerification,
  copy.pages.sellerConsole,
  copy.pages.sellerOnboarding,
  copy.pages.sellerListings,
  copy.pages.sellerListingDetail,
  copy.pages.sellerListingNew,
  copy.pages.sellerRecords,
  copy.pages.sellerPayouts,
]

function flatten(value: unknown, out: string[] = []): string[] {
  if (typeof value === 'string') {
    out.push(value)
    return out
  }
  if (typeof value === 'function') {
    out.push(value.toString())
    return out
  }
  if (Array.isArray(value)) {
    for (const item of value) flatten(item, out)
    return out
  }
  if (value && typeof value === 'object') {
    for (const item of Object.values(value)) flatten(item, out)
  }
  return out
}

describe('user-facing copy terminology', () => {
  for (const [group, blocks] of [
    ['buyer', buyerBlocks],
    ['seller', sellerBlocks],
  ] as const) {
    it(`${group} copy has no operator-only terms`, () => {
      const text = flatten(blocks).join('\n')
      const hits = FORBIDDEN.filter((term) => text.includes(term))
      expect(hits).toEqual([])
    })
  }

  it('top navigation uses 分享额度 instead of 成为分享者', () => {
    expect(copy.nav.seller).toBe('分享额度')
    expect(copy.actions.becomeSeller).toBe('了解分享额度')
  })
})
