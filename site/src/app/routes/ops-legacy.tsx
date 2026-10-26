import { createFileRoute, redirect } from '@tanstack/react-router'

/** Legacy operator entry; the unified console now hosts every ops page. */
export const Route = createFileRoute('/ops-legacy')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops', replace: true })
  },
  component: () => null,
})
