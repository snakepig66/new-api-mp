import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧按次计费已迁入 /console/ops/pricing/per-call。 */
export const Route = createFileRoute('/admin/per-call-prices')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/pricing/per-call', replace: true })
  },
})
