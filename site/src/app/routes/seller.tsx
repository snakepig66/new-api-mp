import {
  Link,
  Navigate,
  Outlet,
  createFileRoute,
  redirect,
  useRouterState,
} from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { ensureAuth } from '@/lib/auth'
import { cn } from '@/lib/cn'
import { Tag } from '@/ui'

import { meQuery } from '@/features/seller/api'
import { AsyncError, PageSkeleton } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { isSellerActive } from '@/features/seller/status'

export const Route = createFileRoute('/seller')({
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
  component: SellerLayout,
})

const nav = copy.pages.sellerCommon.nav
const shell = copy.consoleShell

const tabs = [
  { to: '/seller', label: nav.overview, exact: true },
  { to: '/seller/listings', label: nav.listings },
  { to: '/seller/records', label: nav.records },
  { to: '/seller/payouts', label: nav.payouts },
  { to: '/seller/onboarding', label: nav.onboarding },
] as const

function NavPills() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  return (
    <nav
      aria-label={copy.pages.seller.title}
      className="flex items-center gap-1.5 overflow-x-auto lg:flex-col lg:items-stretch lg:gap-1 lg:overflow-visible"
    >
      {tabs.map((tab) => {
        const active =
          'exact' in tab && tab.exact
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

function IdentitySwitch() {
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
            aria-current={item.key === 'seller' ? 'page' : undefined}
            className={cn(
              'inline-flex h-9 flex-1 items-center justify-center whitespace-nowrap rounded-full px-3 text-[14px] transition-colors lg:justify-start',
              item.key === 'seller'
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

function SellerShell({
  children,
  active,
  status,
}: {
  children: ReactNode
  active?: boolean
  status?: ReactNode
}) {
  return (
    <div className="min-h-screen bg-bg-soft">
      <div className="mx-auto flex w-full max-w-[1440px] flex-col lg:flex-row">
        <aside className="flex shrink-0 flex-col gap-3 border-b border-line bg-bg px-4 py-4 sm:px-6 lg:sticky lg:top-0 lg:h-screen lg:w-[220px] lg:gap-6 lg:border-b-0 lg:border-r lg:px-5 lg:py-6">
          <div className="hidden lg:flex lg:items-center lg:justify-between lg:gap-2">
            <p className="px-3 text-[12px] font-medium uppercase tracking-[0.12em] text-ink-3">
              {shell.sellerGroup}
            </p>
            {active !== undefined ? (
              <Tag tone={active ? 'brand' : 'neutral'} dot>
                {active
                  ? copy.pages.sellerOnboarding.activeBadge
                  : copy.pages.sellerOnboarding.pendingBadge}
              </Tag>
            ) : null}
          </div>
          <NavPills />
          {status}
          <IdentitySwitch />
        </aside>

        <main className="min-w-0 flex-1 px-5 py-8 sm:px-8 lg:px-10 lg:py-10">
          <div className="mx-auto w-full max-w-[1040px]">{children}</div>
        </main>
      </div>
    </div>
  )
}

function SellerLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const { data, isPending, isError, error, refetch } = useQuery(meQuery())

  if (isPending) {
    return (
      <SellerShell>
        <PageSkeleton />
      </SellerShell>
    )
  }

  if (isError || !data) {
    return (
      <SellerShell>
        <AsyncError
          title={copy.pages.sellerCommon.loadError}
          reason={sellerErrorMessage(error)}
          onRetry={() => void refetch()}
          retryLabel={copy.actions.retry}
        />
      </SellerShell>
    )
  }

  const active = isSellerActive(data.seller)

  // Unfinished onboarding: every console page bounces to the onboarding rail so
  // a pending seller cannot create listings or request a payout.
  if (!active && pathname !== '/seller/onboarding') {
    return <Navigate to="/seller/onboarding" replace />
  }

  return (
    <SellerShell active={active}>
      <Outlet />
    </SellerShell>
  )
}
