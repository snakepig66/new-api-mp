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

const tabs = [
  { to: '/admin/prices', label: nav.prices },
  { to: '/admin/per-call-prices', label: nav.perCallPrices },
  { to: '/admin/holidays', label: nav.holidays },
  { to: '/admin/listings', label: nav.listings },
  { to: '/admin/payouts', label: nav.payouts },
  { to: '/admin/tools', label: nav.tools },
] as const

function AdminTab({ to, label }: { to: (typeof tabs)[number]['to']; label: string }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const active = pathname === to || pathname.startsWith(`${to}/`)

  return (
    <Link
      to={to}
      aria-current={active ? 'page' : undefined}
      className={cn(
        '-mb-px shrink-0 whitespace-nowrap border-b-2 px-3 py-2 text-[13px] transition-colors',
        'lg:mb-0 lg:border-b-0 lg:border-l-2 lg:py-1.5',
        active
          ? 'border-brand-dark font-medium text-ink lg:bg-wash'
          : 'border-transparent text-muted hover:border-line-strong hover:text-ink',
      )}
    >
      {label}
    </Link>
  )
}

function AdminLayout() {
  return (
    <div className="mx-auto max-w-[1180px] px-5 py-8">
      <div className="flex flex-col gap-5 lg:flex-row lg:gap-9">
        <aside className="shrink-0 lg:w-[168px]">
          <h2 className="mb-2 hidden text-[14px] font-semibold tracking-tight text-ink lg:block">
            {nav.title}
          </h2>
          <nav
            aria-label={nav.aria}
            className="-mx-1 flex items-center gap-0.5 overflow-x-auto border-b border-line lg:mx-0 lg:flex-col lg:items-stretch lg:gap-0 lg:overflow-visible lg:border-b-0"
          >
            {tabs.map((tab) => (
              <AdminTab key={tab.to} {...tab} />
            ))}
          </nav>
          <p className="mt-4 hidden border-t border-line pt-3 font-mono text-[10.5px] leading-5 text-muted/80 lg:block">
            {copy.pages.admin.legacy}
          </p>
        </aside>

        <div className="min-w-0 flex-1">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
