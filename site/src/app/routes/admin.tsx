import { Link, Outlet, createFileRoute, redirect, useRouterState } from '@tanstack/react-router'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { ROLE_ADMIN_USER, ensureAuth, getSession } from '@/lib/auth'

export const Route = createFileRoute('/admin')({
  beforeLoad: async ({ location }) => {
    const authenticated = await ensureAuth()
    if (!authenticated) {
      throw redirect({
        to: '/login',
        search: { redirect: location.href },
        replace: true,
      })
    }
    const role = getSession()?.user?.role ?? 0
    if (role < ROLE_ADMIN_USER) {
      throw redirect({ to: '/console', replace: true })
    }
  },
  component: AdminLayout,
})

const nav = copy.pages.adminCommon.nav
const shell = copy.consoleShell

const tabs = [
  { to: '/admin/prices', label: nav.prices },
  { to: '/admin/per-call-prices', label: nav.perCallPrices },
  { to: '/admin/pool', label: nav.pool },
  { to: '/admin/holidays', label: nav.holidays },
  { to: '/admin/listings', label: nav.listings },
  { to: '/admin/payouts', label: nav.payouts },
  { to: '/admin/tools', label: nav.tools },
] as const

function NavPills() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  return (
    <nav
      aria-label={nav.aria}
      className="flex items-center gap-1.5 overflow-x-auto lg:flex-col lg:items-stretch lg:gap-1 lg:overflow-visible"
    >
      {tabs.map((tab) => {
        const active = pathname === tab.to || pathname.startsWith(`${tab.to}/`)
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
  )
}

function AdminLayout() {
  return (
    <div className="min-h-screen bg-bg-soft">
      <div className="mx-auto flex w-full max-w-[1440px] flex-col lg:flex-row">
        <aside className="flex shrink-0 flex-col gap-3 border-b border-line bg-bg px-4 py-4 sm:px-6 lg:sticky lg:top-0 lg:h-screen lg:w-[220px] lg:gap-6 lg:border-b-0 lg:border-r lg:px-5 lg:py-6">
          <div className="hidden lg:block">
            <p className="px-3 text-[12px] font-medium uppercase tracking-[0.12em] text-ink-3">
              {shell.adminGroup}
            </p>
          </div>
          <NavPills />
          <div className="mt-3 border-t border-line pt-3 lg:mt-auto lg:pt-4">
            <Link
              to="/console"
              className="inline-flex h-9 items-center rounded-full px-3 text-[14px] text-ink-2 transition-colors hover:bg-bg-soft hover:text-ink"
            >
              {shell.backToConsole}
            </Link>
            <p className="mt-2 hidden px-3 text-[13px] leading-5 text-ink-3 lg:block">
              {copy.pages.admin.legacy}
            </p>
          </div>
        </aside>

        <main className="min-w-0 flex-1 px-5 py-8 sm:px-8 lg:px-10 lg:py-10">
          <div className="mx-auto w-full max-w-[1120px]">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
