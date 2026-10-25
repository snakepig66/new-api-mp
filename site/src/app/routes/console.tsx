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
  { to: '/console/account' as const, label: copy.pages.console.nav.account, exact: false },
]

const shell = copy.consoleShell

function NavPills() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  return (
    <nav
      aria-label={copy.pages.console.nav.aria}
      className="flex items-center gap-1.5 overflow-x-auto lg:flex-col lg:items-stretch lg:gap-1 lg:overflow-visible"
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
  )
}

export function IdentitySwitch({ active }: { active: 'buyer' | 'seller' }) {
  const items = [
    { key: 'buyer' as const, to: '/console' as const, label: shell.buyer },
    { key: 'seller' as const, to: '/seller' as const, label: shell.seller },
  ]
  return (
    <div className="mt-3 border-t border-line pt-3 lg:mt-auto lg:pt-4">
      <p className="hidden px-2 text-[12px] font-medium uppercase tracking-[0.12em] text-ink-3 lg:block">
        {shell.identityTitle}
      </p>
      <div className="flex gap-1.5 lg:mt-2 lg:flex-col">
        {items.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            aria-current={active === item.key ? 'page' : undefined}
            className={cn(
              'inline-flex h-9 flex-1 items-center justify-center whitespace-nowrap rounded-full px-3 text-[14px] transition-colors lg:justify-start',
              active === item.key
                ? 'bg-brand-soft font-medium text-brand-ink'
                : 'text-ink-2 hover:bg-bg-soft hover:text-ink',
            )}
          >
            {item.label}
          </Link>
        ))}
      </div>
    </div>
  )
}

function ConsoleLayout() {
  return (
    <div className="min-h-screen bg-bg-soft">
      <div className="mx-auto flex w-full max-w-[1440px] flex-col lg:flex-row">
        <aside className="flex shrink-0 flex-col gap-3 border-b border-line bg-bg px-4 py-4 sm:px-6 lg:sticky lg:top-0 lg:h-screen lg:w-[220px] lg:gap-6 lg:border-b-0 lg:border-r lg:px-5 lg:py-6">
          <div className="hidden lg:block">
            <p className="px-3 text-[12px] font-medium uppercase tracking-[0.12em] text-ink-3">
              {shell.buyerGroup}
            </p>
          </div>
          <NavPills />
          <IdentitySwitch active="buyer" />
        </aside>

        <main className="min-w-0 flex-1 px-5 py-8 sm:px-8 lg:px-10 lg:py-10">
          <div className="mx-auto w-full max-w-[1040px]">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
