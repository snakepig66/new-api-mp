import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatYuan, type MicroAmount } from '@/lib/money'

export type PriceTier = 'peak' | 'offpeak' | 'flat'
export type PriceSize = 'sm' | 'md' | 'lg'

export interface PriceProps {
  /** Current tier's price, in integer micro-yuan per million tokens. */
  value: MicroAmount
  /** Which tier `value` belongs to; flat tiers show no tier label. */
  tier?: PriceTier
  /** The other tier's price, rendered smaller and grey. */
  other?: { value: MicroAmount; tier: PriceTier } | null
  /** Defaults to "百万 tokens"; pass null to omit the unit. */
  unit?: string | null
  align?: 'left' | 'right'
  size?: PriceSize
  className?: string
}

const sizeClass: Record<PriceSize, string> = {
  sm: 'text-[13px]',
  md: 'text-[15px]',
  lg: 'text-[22px] font-medium',
}

const tierDot: Record<PriceTier, string> = {
  peak: 'bg-warning',
  offpeak: 'bg-success',
  flat: 'bg-line-strong',
}

function tierLabel(tier: PriceTier): string {
  return copy.tiers[tier]
}

/**
 * Price per million tokens. The current tier is the loud one; the other tier
 * sits on a quiet second line.
 */
export function Price({
  value,
  tier = 'flat',
  other = null,
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
        {tier !== 'flat' ? (
          <span className="inline-flex items-center gap-1 text-[11px] text-muted">
            <span
              className={cn('h-1.5 w-1.5 rounded-full', tierDot[tier])}
              aria-hidden="true"
            />
            {tierLabel(tier)}
          </span>
        ) : null}
        <span className={cn('font-numeric tabular-nums text-ink', sizeClass[size])}>
          {formatYuan(value)}
        </span>
        {unit ? <span className="text-[11px] text-muted">/ {unit}</span> : null}
      </div>
      {other ? (
        <div className="mt-0.5 text-[11px] text-muted/80">
          {copy.price.otherTier(tierLabel(other.tier))}{' '}
          <span className="font-numeric tabular-nums">{formatYuan(other.value)}</span>
        </div>
      ) : null}
    </div>
  )
}
