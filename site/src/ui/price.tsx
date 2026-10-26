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
  /** Hide the inline tier label when the caller renders a separate peak badge. */
  showTier?: boolean
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
  lg: 'text-[22px] font-semibold',
}

const tierDot: Record<PriceTier, string> = {
  peak: 'bg-warn',
  offpeak: 'bg-ink-3',
  flat: 'bg-line-strong',
}

function tierLabel(tier: PriceTier): string {
  return copy.tiers[tier]
}

/**
 * Price per million tokens. The current tier is the loud one; the other tier
 * sits on a quiet second line. Figures are sans + tabular, never mono.
 */
export function Price({
  value,
  tier = 'flat',
  showTier = true,
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
        {tier !== 'flat' && showTier ? (
          <span className="inline-flex items-center gap-1 text-[13px] text-ink-3">
            <span
              className={cn('h-1.5 w-1.5 rounded-full', tierDot[tier])}
              aria-hidden="true"
            />
            {tierLabel(tier)}
          </span>
        ) : null}
        <span className={cn('tabular-nums text-ink', sizeClass[size])}>
          {formatYuan(value)}
        </span>
        {unit ? <span className="text-[13px] text-ink-3">/ {unit}</span> : null}
      </div>
      {other ? (
        <div className="mt-0.5 text-[13px] text-ink-3">
          {copy.price.otherTier(tierLabel(other.tier))}{' '}
          <span className="tabular-nums">{formatYuan(other.value)}</span>
        </div>
      ) : null}
    </div>
  )
}
