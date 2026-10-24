import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'

import { ensureAuth } from '@/lib/auth'

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

function SellerLayout() {
  return (
    <div className="mx-auto max-w-[1180px] px-5 py-8">
      <Outlet />
    </div>
  )
}
