import { createFileRoute, redirect } from '@tanstack/react-router'

/**
 * Payment providers return the browser to `<ServerAddress>/usage-logs` after
 * the gateway page. That path belongs to the upstream console, which this
 * site replaces, so send the buyer to the wallet where the credited balance is
 * visible. A failed or still-pending payment is also handled there.
 */
export const Route = createFileRoute('/usage-logs')({
  beforeLoad: () => {
    throw redirect({ to: '/console/wallet', search: { page: 1 }, replace: true })
  },
})
