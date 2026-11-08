import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧节假日日历已迁入 /console/ops/tools。 */
export const Route = createFileRoute('/admin/holidays')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/tools', replace: true })
  },
})
