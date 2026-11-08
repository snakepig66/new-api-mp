import { copy } from '@/copy'

import type { ConsoleNavGroup } from './ConsoleSidebar'

/**
 * One grouped navigation for every console role, grouped by what the entry
 * does rather than by buyer/seller identity:
 * 0. 概览置顶，不带分组标题
 * 1. 接入与用量: 密钥与调用记录
 * 2. 渠道: 分享额度侧（被调用记录仅开通后出现）
 * 3. 资金: 钱包与收益放在同一处，不再按身份切开
 * 4. 账号设置置底，同样不带分组标题
 * 管理员额外看到运营组。
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
      // 概览单独置顶，不需要分组标题。
      items: [{ to: '/console', label: nav.overview, exact: true }],
    },
    {
      label: '接入与用量',
      items: [
        { to: '/console/keys', label: nav.keys },
        { to: '/console/records', label: nav.records },
      ],
    },
    {
      label: '渠道',
      items: [
        { to: '/console/listings', label: sellerNav.listings },
        ...(sellerActive
          ? [
              {
                to: '/console/records',
                search: { view: 'sales' },
                iconKey: '/console/records#sales',
                label: '被调用记录',
              },
            ]
          : []),
      ],
    },
    {
      label: '资金',
      items: [
        { to: '/console/wallet', label: nav.wallet },
        { to: '/console/earnings', label: sellerNav.payouts },
      ],
    },
    ...(isAdmin
      ? [
          {
            label: '运营',
            collapsible: true,
            items: [
              { to: '/console/ops/metrics', label: '运营看板' },
              { to: '/console/ops/logs', label: '调用记录' },
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
      // 账号设置单独置底；文档/模型市场已在全站顶栏，不在控制台重复。
      items: [{ to: '/console/account', label: nav.account }],
    },
  ]
}
