import { Navigate, createFileRoute } from '@tanstack/react-router'

/** 运营首页落到价目表：最常用的入口。 */
export const Route = createFileRoute('/console/ops/')({
  component: () => <Navigate to="/console/ops/pricing" replace />,
})
