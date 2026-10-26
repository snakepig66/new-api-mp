import { Link, Outlet, createFileRoute, useRouterState } from '@tanstack/react-router'

import { copy } from '@/copy'
import { SellerOnly } from '@/features/seller/SellerOnly'
import { cn } from '@/lib/cn'

export const Route = createFileRoute('/console/earnings')({
  component: EarningsLayout,
})

const page = copy.pages.consoleEarnings

const tabs = [
  { to: '/console/earnings', label: page.tabs.overview, exact: true },
  { to: '/console/earnings/payouts', label: page.tabs.payouts, exact: false },
] as const

function EarningsLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })

  return (
    <SellerOnly>
      <nav
        aria-label={page.title}
        className="mb-6 flex items-center gap-1.5 overflow-x-auto"
      >
        {tabs.map((tab) => {
          const active = tab.exact
            ? pathname === tab.to || pathname === `${tab.to}/`
            : pathname === tab.to || pathname.startsWith(`${tab.to}/`)
          return (
            <Link
              key={tab.to}
              to={tab.to}
              aria-current={active ? 'page' : undefined}
              className={cn(
                'inline-flex h-9 shrink-0 items-center whitespace-nowrap rounded-full px-3.5 text-[14px] transition-colors',
                active
                  ? 'bg-brand-soft font-medium text-brand-ink'
                  : 'text-ink-2 hover:bg-bg-soft hover:text-ink',
              )}
            >
              {tab.label}
            </Link>
          )
        })}
      </nav>
      <Outlet />
    </SellerOnly>
  )
}
