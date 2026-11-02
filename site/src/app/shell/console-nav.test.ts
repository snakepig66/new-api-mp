import { describe, expect, it } from 'vitest'

import { consoleGroups } from './console-nav'

function labels(groups: ReturnType<typeof consoleGroups>): string[] {
  return groups.flatMap((group) => group.items.map((item) => item.label))
}

describe('console navigation', () => {
  it('groups navigation into 我要用, 我要卖 and 更多', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: false })
    expect(groups.map((group) => group.label)).toEqual(['我要用', '我要卖', '更多'])
    expect(labels(groups)).toContain('概览')
    expect(labels(groups)).toContain('API Key')
    expect(labels(groups)).toContain('使用记录')
    expect(labels(groups)).toContain('钱包')
    expect(labels(groups)).toContain('我的渠道')
    expect(labels(groups)).toContain('收益与提现')
    expect(labels(groups)).toContain('模型知识库')
    expect(labels(groups)).toContain('帮助中心')
    expect(labels(groups)).toContain('账号设置')
    expect(labels(groups)).not.toContain('运营看板')
    expect(labels(groups)).not.toContain('团体工具')
  })

  it('shows 被调明细 in 更多 for active sellers', () => {
    const groups = consoleGroups({ sellerActive: true, isAdmin: false })
    expect(labels(groups)).toContain('被调明细')
  })

  it('adds operator items in 更多 only for admins', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: true })
    expect(labels(groups)).toEqual(
      expect.arrayContaining([
        '运营看板',
        '团体工具',
        '模型目录',
        '发布审核',
      ]),
    )
  })
})
