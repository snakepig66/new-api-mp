import { createFileRoute, redirect } from '@tanstack/react-router'

/**
 * Pricing is now unified into /console/ops/models.
 * This redirect ensures legacy bookmarks seamlessly land on the unified page.
 */
export const Route = createFileRoute('/console/ops/pricing/')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/models', replace: true })
  },
  component: () => null,
})
