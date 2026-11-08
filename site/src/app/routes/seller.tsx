import { Navigate, createFileRoute, useRouterState } from '@tanstack/react-router'

/**
 * Legacy seller shell. The unified console now owns these pages; every old
 * /seller path redirects to its /console equivalent so bookmarks keep working.
 * The redirect runs from the component (post-mount) instead of beforeLoad:
 * a redirect thrown during the initial route load is occasionally swallowed
 * by the router (P2-1 blank page), while a mount-time navigation is not.
 */
export const Route = createFileRoute('/seller')({
  component: SellerLegacyRedirect,
})

function SellerLegacyRedirect() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const path = pathname.replace(/\/+$/, '') || '/seller'
  let to = '/console'
  let search: Record<string, unknown> | undefined
  if (path === '/seller/listings/new') {
    to = '/console/listings/new'
  } else if (path.startsWith('/seller/listings/')) {
    to = `/console/listings/${path.slice('/seller/listings/'.length)}`
  } else if (path === '/seller/listings') {
    to = '/console/listings'
  } else if (path === '/seller/records') {
    to = '/console/records'
    search = { view: 'sales' }
  } else if (path === '/seller/payouts') {
    to = '/console/earnings'
  } else if (path === '/seller/onboarding') {
    to = '/console/onboarding'
  }
  return <Navigate to={to} search={search} replace />
}
