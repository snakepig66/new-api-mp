import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatYuan, type MicroAmount } from '@/lib/money'

export type PriceSize = 'sm' | 'md' | 'lg'

export interface PriceProps {
  /** Price, in integer micro-yuan per million tokens. */
  value: MicroAmount
  /** Defaults to "百万 tokens"; pass null to omit the unit. */
  unit?: string | null
  align?: 'left' | 'right'
  size?: PriceSize
  className?: string
}

const sizeClass: Record<PriceSize, string> = {
  sm: 'text-[13px]',
  md: 'text-[15px]',
  lg: 'text-[22px] font-semibold text-brand-ink',
}

/** Price per million tokens. Figures are sans + tabular, never mono. */
export function Price({
  value,
  unit = copy.units.perMillionTokens,
  align = 'right',
  size = 'md',
  className,
}: PriceProps) {
  return (
    <div
      className={cn(
        'leading-tight',
        align === 'right' ? 'text-right' : 'text-left',
        className,
      )}
    >
      <div
        className={cn(
          'flex items-baseline gap-1.5',
          align === 'right' ? 'justify-end' : 'justify-start',
        )}
      >
        <span className={cn('tabular-nums text-ink', sizeClass[size])}>
          {formatYuan(value)}
        </span>
        {unit ? <span className="text-[13px] text-ink-3">/ {unit}</span> : null}
      </div>
    </div>
  )
}
