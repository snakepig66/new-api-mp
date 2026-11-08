import { createFileRoute } from '@tanstack/react-router'
import { RiskPage } from '../risk'

export const Route = createFileRoute('/console/ops/tools/risk')({
  component: RiskPage,
})
