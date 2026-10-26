import { createFileRoute, redirect } from '@tanstack/react-router'

/** 旧运营首页已迁入 /console/ops/pricing。 */
export const Route = createFileRoute('/admin/')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/pricing', replace: true })
  },
})
