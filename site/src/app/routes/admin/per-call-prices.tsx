import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧按次计费已迁入 /console/ops/pricing/per-call。 */
export const Route = createFileRoute('/admin/per-call-prices')({
  component: () => <Navigate to="/console/ops/pricing/per-call" replace />,
})
