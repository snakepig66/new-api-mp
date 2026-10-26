import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'

import { ROLE_ADMIN_USER, getSession } from '@/lib/auth'

/**
 * 运营后台统一入口：只有管理员及以上角色可以进入。
 * 未登录由父级 /console 布局跳转登录；这里只处理角色不足的情况。
 */
export const Route = createFileRoute('/console/ops')({
  beforeLoad: () => {
    const role = getSession()?.user?.role ?? 0
    if (role < ROLE_ADMIN_USER) {
      throw redirect({ to: '/console', replace: true })
    }
  },
  component: OpsLayout,
})

function OpsLayout() {
  return <Outlet />
}
