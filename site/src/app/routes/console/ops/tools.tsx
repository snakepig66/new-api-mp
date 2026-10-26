import { Outlet, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { OpsTabNav, type OpsTabLink } from '@/features/admin/components'

export const Route = createFileRoute('/console/ops/tools')({
  component: ToolsLayout,
})

const nav = copy.pages.adminCommon.nav

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/tools', label: nav.holidays },
  { to: '/console/ops/tools/reversal', label: nav.tools },
]

function ToolsLayout() {
  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel={nav.aria} />
      <Outlet />
    </>
  )
}
