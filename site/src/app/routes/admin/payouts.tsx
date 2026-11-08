import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 旧提现审批已迁入 /console/ops/review/payouts。 */
export const Route = createFileRoute('/admin/payouts')({
  component: () => <Navigate to="/console/ops/review/payouts" replace />,
})
