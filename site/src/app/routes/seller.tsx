import { createFileRoute, redirect } from '@tanstack/react-router'

/**
 * Legacy seller shell. The unified console now owns these pages; every old
 * /seller path redirects to its /console equivalent so bookmarks keep working.
 */
export const Route = createFileRoute('/seller')({
  beforeLoad: ({ location }) => {
    const path = location.pathname.replace(/\/+$/, '') || '/seller'
    let href = '/share'

    if (path === '/seller/listings/new') {
      href = '/console/listings/new'
    } else if (path.startsWith('/seller/listings/')) {
      href = `/console/listings/${path.slice('/seller/listings/'.length)}`
    } else if (path === '/seller/listings') {
      href = '/console/listings'
    } else if (path === '/seller/records') {
      href = '/console/records?view=sales'
    } else if (path === '/seller/payouts') {
      href = '/console/earnings'
    } else if (path === '/seller/onboarding') {
      href = '/console/onboarding'
    }

    throw redirect({ href, replace: true })
  },
  component: () => null,
})
