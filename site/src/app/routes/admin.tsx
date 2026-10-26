import { Outlet, createFileRoute } from '@tanstack/react-router'

/**
 * 旧运营后台的占位布局。
 * 各子路由（以及 index）分别跳转到 /console/ops 下的对应页面，
 * 这里不再做角色校验：新的 /console/ops 布局会统一校验管理员权限。
 */
export const Route = createFileRoute('/admin')({
  component: AdminLegacyLayout,
})

function AdminLegacyLayout() {
  return <Outlet />
}
