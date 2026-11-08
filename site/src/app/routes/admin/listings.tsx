import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧挂单审核已迁入 /console/ops/review。 */
export const Route = createFileRoute('/admin/listings')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/review', replace: true })
  },
})
