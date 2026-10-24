import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'

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

function AdminLayout() {
  return (
    <div className="mx-auto max-w-[1180px] px-5 py-8">
      <Outlet />
    </div>
  )
}
