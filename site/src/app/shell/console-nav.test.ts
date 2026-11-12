import { describe, expect, it } from 'vitest'

import { consoleGroups } from './console-nav'

function labels(groups: ReturnType<typeof consoleGroups>): string[] {
  return groups.flatMap((group) => group.items.map((item) => item.label))
}

describe('console navigation', () => {
  it('groups navigation by function, with 概览 on its own', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: false })
    expect(groups.map((group) => group.label)).toEqual([
      undefined,
      '接入与用量',
      '渠道',
      '资金',
      undefined,
    ])
    expect(groups[0]?.items.map((item) => item.label)).toEqual(['概览'])
    expect(groups.at(-1)?.items.map((item) => item.label)).toEqual(['账号设置'])
    expect(labels(groups)).toContain('API 密钥')
    expect(labels(groups)).toContain('使用记录')
    expect(labels(groups)).toContain('钱包')
    expect(labels(groups)).toContain('我的渠道')
    expect(labels(groups)).toContain('收益与提现')
    expect(labels(groups)).toContain('账号设置')
    expect(labels(groups)).not.toContain('模型知识库')
    expect(labels(groups)).not.toContain('帮助中心')
    expect(labels(groups)).not.toContain('运营看板')
    expect(labels(groups)).not.toContain('团体工具')
  })

  it('keeps wallet and earnings in the same 资金 group', () => {
    const groups = consoleGroups({ sellerActive: true, isAdmin: false })
    const funds = groups.find((group) => group.label === '资金')
    expect(funds?.items.map((item) => item.label)).toEqual(['钱包', '收益与提现'])
  })

  it('shows onboarding and 被调用记录 next to 我的渠道 for active sellers', () => {
    const groups = consoleGroups({ sellerActive: true, isAdmin: false })
    const channels = groups.find((group) => group.label === '渠道')
    const records = channels?.items.find((item) => item.label === '被调用记录')
    expect(channels?.items.map((item) => item.label)).toEqual([
      '开通分享',
      '我的渠道',
      '被调用记录',
    ])
    expect(records?.search).toEqual({ view: 'sales' })
    expect(records?.iconKey).toBe('/console/records#sales')
  })

  it('marks the onboarding entry until agreement and payout account are done', () => {
    const pending = consoleGroups({
      sellerActive: false,
      isAdmin: false,
      onboardingPending: true,
    })
    const ready = consoleGroups({
      sellerActive: true,
      isAdmin: false,
      onboardingPending: false,
    })
    const itemOf = (groups: ReturnType<typeof consoleGroups>) =>
      groups
        .find((group) => group.label === '渠道')
        ?.items.find((item) => item.label === '开通分享')

    expect(itemOf(pending)?.badge).toBe('待完善')
    expect(itemOf(ready)?.badge).toBeUndefined()
  })

  it('hides 被调用记录 for users who have not opened a channel', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: false })
    expect(labels(groups)).not.toContain('被调用记录')
  })

  it('gives admins a collapsible 运营 group and hides buyer-facing help links', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: true })
    expect(groups.map((group) => group.label)).toEqual([
      undefined,
      '接入与用量',
      '渠道',
      '资金',
      '运营',
      undefined,
    ])
    expect(labels(groups)).not.toContain('模型知识库')
    expect(labels(groups)).not.toContain('帮助中心')
    expect(labels(groups)).toContain('账号设置')
    const ops = groups.find((group) => group.label === '运营')
    expect(ops?.collapsible).toBe(true)
    expect(ops?.items.map((item) => item.label)).toEqual(
      expect.arrayContaining([
        '运营看板',
        '团体工具',
        '广场渠道',
        '模型池',
        '广场上架',
        '发布审核',
        '用户与充值',
        '系统设置',
      ]),
    )
  })
})
