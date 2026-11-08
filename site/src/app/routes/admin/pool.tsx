import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧模型池已迁入 /console/ops/models。 */
export const Route = createFileRoute('/admin/pool')({
  component: () => <Navigate to="/console/ops/models" replace />,
})
