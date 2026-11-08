import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧价目表已迁入 /console/ops/pricing。 */
export const Route = createFileRoute('/admin/prices')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/pricing', replace: true })
  },
})
