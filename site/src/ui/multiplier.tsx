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
    <span className={cn('inline-flex items-center gap-1.5', className)}>
      <span className="tabular-nums text-ink">{formatMultiplier(milli)}x</span>
      {showSaving ? (
        <span className="inline-flex h-[22px] items-center rounded-full bg-brand-soft px-2 text-[12px] leading-none text-brand-ink">
          {copy.multiplier.savings(savingPercent(milli))}
        </span>
      ) : null}
    </span>
  )
}
