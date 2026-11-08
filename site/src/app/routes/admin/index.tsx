import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧运营首页已迁入 /console/ops/pricing。 */
export const Route = createFileRoute('/admin/')({
  component: () => <Navigate to="/console/ops/pricing" replace />,
})
