import { copy } from '@/copy'

import type { ConsoleNavGroup } from './ConsoleSidebar'

/**
 * One grouped navigation for every console role. Seller and operator groups
 * are absent rather than disabled, so a buyer never sees a dead entry.
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
  const shell = copy.consoleShell

  return [
    {
      label: shell.groups.usage,
      items: [
        { to: '/console', label: nav.overview, exact: true },
        { to: '/console/keys', label: nav.keys },
        { to: '/console/records', label: nav.records },
      ],
    },
    {
      label: shell.groups.funds,
      items: [{ to: '/console/wallet', label: nav.wallet }],
    },
    ...(sellerActive
      ? [
          {
            label: shell.groups.share,
            items: [
              { to: '/console/listings', label: sellerNav.listings },
              { to: '/console/earnings', label: sellerNav.payouts },
            ],
          },
        ]
      : []),
    {
      label: shell.groups.account,
      items: [{ to: '/console/account', label: nav.account }],
    },
    ...(isAdmin
      ? [
          {
            label: shell.groups.ops,
            items: [
              { to: '/console/ops/pricing', label: shell.ops.pricing },
              { to: '/console/ops/models', label: shell.ops.models },
              { to: '/console/ops/trust-pool', label: shell.ops.trustPool },
              { to: '/console/ops/review', label: shell.ops.review },
              { to: '/console/ops/tools', label: shell.ops.tools },
              { to: '/console/ops/trace', label: shell.ops.trace },
              { to: '/console/ops/metrics', label: shell.ops.metrics },
              { to: '/console/ops/risk', label: shell.ops.risk },
            ],
          },
        ]
      : []),
  ]
}
