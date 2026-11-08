import { Navigate, createFileRoute } from '@tanstack/react-router'

/**
 * 旧的渠道模型映射页已删除（卖家自选模型），深链接跳转到模型池。
 */
export const Route = createFileRoute('/admin/channel-models')({
  component: () => <Navigate to="/console/ops/models" replace />,
})
