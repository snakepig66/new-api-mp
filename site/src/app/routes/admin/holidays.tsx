import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧节假日日历已迁入 /console/ops/tools。 */
export const Route = createFileRoute('/admin/holidays')({
  component: () => <Navigate to="/console/ops/tools" replace />,
})
