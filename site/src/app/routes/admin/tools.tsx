import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧冲正与反查已迁入 /console/ops/tools/reversal。 */
export const Route = createFileRoute('/admin/tools')({
  component: () => <Navigate to="/console/ops/tools/reversal" replace />,
})
