import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧冲正与反查已迁入 /console/ops/tools/reversal。 */
export const Route = createFileRoute('/admin/tools')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/tools/reversal', replace: true })
  },
})
