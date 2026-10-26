import { Outlet, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { OpsTabNav, type OpsTabLink } from '@/features/admin/components'

export const Route = createFileRoute('/console/ops/review')({
  component: ReviewLayout,
})

const nav = copy.pages.adminCommon.nav

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/review', label: nav.listings },
  { to: '/console/ops/review/payouts', label: nav.payouts },
]

function ReviewLayout() {
  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel={nav.aria} />
      <Outlet />
    </>
  )
}
