import { cn } from '@/lib/cn'
import { formatMultiplier } from '@/lib/multiplier'

export interface MultiplierProps {
  /** Integer milli multiplier from the backend: 300 = 0.30x. */
  milli: number
  showSaving?: boolean
  className?: string
}

/** Listing multiplier without savings badge. */
export function Multiplier({ milli, className }: MultiplierProps) {
  return (
    <span className={cn('inline-flex items-center gap-1.5', className)}>
      <span className="tabular-nums text-ink">{formatMultiplier(milli)}x</span>
    </span>
  )
}
