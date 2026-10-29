import { Outlet, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { OpsTabNav, type OpsTabLink } from '@/features/admin/components'

export const Route = createFileRoute('/console/ops/pricing')({
  component: PricingLayout,
})

const nav = copy.pages.adminCommon.nav

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/models', label: '模型与定价' },
  { to: '/console/ops/pricing/per-call', label: nav.perCallPrices },
]

function PricingLayout() {
  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel={nav.aria} />
      <Outlet />
    </>
  )
}
