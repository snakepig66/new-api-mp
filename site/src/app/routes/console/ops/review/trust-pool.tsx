import { createFileRoute } from '@tanstack/react-router'
import { TrustPoolPage } from '../trust-pool'

export const Route = createFileRoute('/console/ops/review/trust-pool')({
  component: TrustPoolPage,
})
