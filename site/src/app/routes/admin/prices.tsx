import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧价目表已迁入 /console/ops/pricing。 */
export const Route = createFileRoute('/admin/prices')({
  component: () => <Navigate to="/console/ops/pricing" replace />,
})
