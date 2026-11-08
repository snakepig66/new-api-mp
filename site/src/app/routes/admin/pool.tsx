import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧模型池已迁入 /console/ops/models。 */
export const Route = createFileRoute('/admin/pool')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/models', replace: true })
  },
})
