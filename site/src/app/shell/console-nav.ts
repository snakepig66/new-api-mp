import { copy } from '@/copy'

import type { ConsoleNavGroup } from './ConsoleSidebar'

/**
 * One grouped navigation for every console role:
 * 1. "我要用": 概览、API Key、使用记录、钱包
 * 2. "我要卖": 我的渠道、收益与提现
 * 3. "更多": 被调明细、模型知识库、帮助中心、账号，管理员可见运营看板、团体工具
 */
export function consoleGroups({
  sellerActive,
  isAdmin,
}: {
  sellerActive: boolean
  isAdmin: boolean
}): ConsoleNavGroup[] {
  const nav = copy.pages.console.nav
  const sellerNav = copy.pages.sellerCommon.nav

  return [
    {
      label: '我要用',
      items: [
        { to: '/console', label: nav.overview, exact: true },
        { to: '/console/keys', label: nav.keys },
        { to: '/console/records', label: nav.records },
        { to: '/console/wallet', label: nav.wallet },
      ],
    },
    {
      label: '我要卖',
      items: [
        { to: '/console/listings', label: sellerNav.listings },
        { to: '/console/earnings', label: sellerNav.payouts },
        ...(sellerActive ? [{ to: '/console/records', search: { view: 'sales' }, label: '被调明细' }] : []),
      ],
    },
    ...(isAdmin
      ? [
          {
            label: '运营',
            items: [
              { to: '/console/ops/metrics', label: '运营看板' },
              { to: '/console/ops/channels', label: '广场渠道' },
              { to: '/console/ops/models', label: '模型池' },
              { to: '/console/ops/plaza', label: '广场上架' },
              { to: '/console/ops/review', label: '发布审核' },
              { to: '/console/ops/users', label: '用户与充值' },
              { to: '/console/ops/tools', label: '团体工具' },
              { to: '/console/ops/settings', label: '系统设置' },
            ],
          },
        ]
      : []),
    {
      label: '更多',
      collapsible: true,
      items: [
        { to: '/console/account', label: nav.account },
        // 面向买家/卖家的入口；管理员不需要。
        ...(isAdmin
          ? []
          : [
              { to: '/models', label: '模型知识库' },
              { to: '/docs', label: '帮助中心' },
            ]),
      ],
    },
  ]
}
