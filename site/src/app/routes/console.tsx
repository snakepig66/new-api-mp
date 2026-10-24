import { Link, Outlet, createFileRoute, redirect, useRouterState } from '@tanstack/react-router'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { ensureAuth } from '@/lib/auth'

export const Route = createFileRoute('/console')({
  beforeLoad: async ({ location }) => {
    const authenticated = await ensureAuth()
    if (!authenticated) {
      throw redirect({
        to: '/login',
        search: { redirect: location.href },
        replace: true,
      })
    }
  },
  component: ConsoleLayout,
})

const tabs = [
  { to: '/console' as const, label: copy.pages.console.nav.overview, exact: true },
  { to: '/console/keys' as const, label: copy.pages.console.nav.keys, exact: false },
  { to: '/console/records' as const, label: copy.pages.console.nav.records, exact: false },
  { to: '/console/wallet' as const, label: copy.pages.console.nav.wallet, exact: false },
]

function ConsoleTabs() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  return (
    <nav
      className="-mx-1 mb-7 flex items-center gap-1 overflow-x-auto border-b border-line"
      aria-label={copy.pages.console.nav.aria}
    >
      {tabs.map((tab) => {
        const active = tab.exact
          ? pathname === tab.to || pathname === `${tab.to}/`
          : pathname === tab.to || pathname.startsWith(`${tab.to}/`)
        return (
          <Link
            key={tab.to}
            to={tab.to}
            className={cn(
              '-mb-px shrink-0 border-b-2 px-3 py-2 text-[13px] transition-colors',
              active
                ? 'border-brand-dark font-medium text-ink'
                : 'border-transparent text-muted hover:border-line-strong hover:text-ink',
            )}
          >
            {tab.label}
          </Link>
        )
      })}
    </nav>
  )
}

function ConsoleLayout() {
  return (
    <div className="mx-auto max-w-[1180px] px-5 py-8">
      <ConsoleTabs />
      <Outlet />
    </div>
  )
}
