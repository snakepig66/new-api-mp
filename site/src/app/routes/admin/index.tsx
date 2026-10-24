import { createFileRoute, redirect } from '@tanstack/react-router'

/** 后台首页落到价目表：运营最常用的入口。 */
export const Route = createFileRoute('/admin/')({
  beforeLoad: () => {
    throw redirect({ to: '/admin/prices', replace: true })
  },
})
