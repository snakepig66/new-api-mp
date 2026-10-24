import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatMultiplier, savingPercent } from '@/lib/multiplier'

export interface MultiplierProps {
  /** Integer milli multiplier from the backend: 300 = 0.30x. */
  milli: number
  showSaving?: boolean
  className?: string
}

/** Listing multiplier with the discount versus official price. */
export function Multiplier({ milli, showSaving = true, className }: MultiplierProps) {
  return (
    <span className={cn('inline-flex items-baseline gap-1.5', className)}>
      <span className="font-numeric tabular-nums text-ink">{formatMultiplier(milli)}x</span>
      {showSaving ? (
        <span className="text-[11px] text-success">{copy.multiplier.savings(savingPercent(milli))}</span>
      ) : null}
    </span>
  )
}
