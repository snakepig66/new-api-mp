import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧挂单审核已迁入 /console/ops/review。 */
export const Route = createFileRoute('/admin/listings')({
  component: () => <Navigate to="/console/ops/review" replace />,
})
