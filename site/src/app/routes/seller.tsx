import {
  Link,
  Navigate,
  Outlet,
  createFileRoute,
  redirect,
  useRouterState,
} from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'

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

const tabs = [
  { to: '/seller', label: nav.overview, exact: true },
  { to: '/seller/listings', label: nav.listings },
  { to: '/seller/records', label: nav.records },
  { to: '/seller/payouts', label: nav.payouts },
  { to: '/seller/onboarding', label: nav.onboarding },
] as const

function SellerTab({
  to,
  label,
  exact,
}: {
  to: (typeof tabs)[number]['to']
  label: string
  exact?: boolean
}) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const active = exact ? pathname === to : pathname === to || pathname.startsWith(`${to}/`)

  return (
    <Link
      to={to}
      className={cn(
        'relative whitespace-nowrap px-3 py-2.5 text-[12.5px] transition-colors',
        active ? 'font-medium text-ink' : 'text-muted hover:text-ink',
      )}
    >
      {label}
      <span
        aria-hidden="true"
        className={cn(
          'absolute inset-x-2 bottom-0 h-[2px] rounded-full',
          active ? 'bg-brand-dark' : 'bg-transparent',
        )}
      />
    </Link>
  )
}

function SellerLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const { data, isPending, isError, error, refetch } = useQuery(meQuery())

  if (isPending) {
    return (
      <div className="mx-auto max-w-[1180px] px-5 py-8">
        <PageSkeleton />
      </div>
    )
  }

  if (isError || !data) {
    return (
      <div className="mx-auto max-w-[1180px] px-5 py-8">
        <AsyncError
          title={copy.pages.sellerCommon.loadError}
          reason={sellerErrorMessage(error)}
          onRetry={() => void refetch()}
          retryLabel={copy.actions.retry}
        />
      </div>
    )
  }

  const active = isSellerActive(data.seller)

  // Unfinished onboarding: every console page bounces to the onboarding rail so
  // a pending seller cannot create listings or request a payout.
  if (!active && pathname !== '/seller/onboarding') {
    return <Navigate to="/seller/onboarding" replace />
  }

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-8">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-[12px] text-muted">
          <span>{copy.pages.seller.title}</span>
          <span className="h-3 w-px bg-line-strong" aria-hidden="true" />
          <span>{copy.pages.seller.lead}</span>
        </div>
        {active ? (
          <Tag tone="success" dot>
            {copy.pages.sellerOnboarding.activeBadge}
          </Tag>
        ) : (
          <Tag tone="warning" dot>
            {copy.pages.sellerOnboarding.pendingBadge}
          </Tag>
        )}
      </div>

      <nav
        aria-label={copy.pages.seller.title}
        className="mt-3 -mb-px flex items-center gap-0.5 overflow-x-auto border-b border-line"
      >
        {tabs.map((tab) => (
          <SellerTab key={tab.to} {...tab} />
        ))}
      </nav>

      <div className="pt-7">
        <Outlet />
      </div>
    </div>
  )
}
