import { createFileRoute, redirect } from '@tanstack/react-router'

/** 运营首页落到价目表：最常用的入口。 */
export const Route = createFileRoute('/console/ops/')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/pricing', replace: true })
  },
})
