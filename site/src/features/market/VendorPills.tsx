import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export interface VendorPillOption {
  value: string
  label: string
  logoUrl?: string
  count: number
}

export interface VendorPillProps {
  label: string
  logoUrl?: string
  count: number
  active: boolean
  onClick: () => void
}

/**
 * One vendor card-pill: logo (or a monogram fallback), short brand name and
 * the number of standard models that vendor currently offers.
 */
export function VendorPill({ label, logoUrl, count, active, onClick }: VendorPillProps) {
  const [logoFailed, setLogoFailed] = useState(false)

  useEffect(() => {
    setLogoFailed(false)
  }, [logoUrl])

  const showLogo = Boolean(logoUrl) && !logoFailed
  const initial = label.trim().charAt(0).toUpperCase() || '?'

  return (
    <button
      type="button"
      aria-pressed={active}
      aria-label={`${label} ${copy.pages.models.vendorCount(count)}`}
      onClick={onClick}
      className={cn(
        'inline-flex shrink-0 items-center gap-2 whitespace-nowrap rounded-full border px-2.5 py-1.5 text-meta transition-colors',
        active
          ? 'border-brand bg-brand-soft text-brand-ink'
          : 'border-white/70 bg-white/60 text-ink-2 hover:border-line-strong hover:text-ink',
      )}
    >
      <span
        className={cn(
          'grid h-6 w-6 shrink-0 place-items-center overflow-hidden rounded-full text-caption font-semibold',
          active ? 'bg-white/85 text-brand-ink' : 'bg-white/50 text-ink-2',
        )}
        aria-hidden="true"
      >
        {showLogo ? (
          <img
            src={logoUrl}
            alt=""
            width={24}
            height={24}
            className="h-6 w-6 object-cover"
            onError={() => setLogoFailed(true)}
          />
        ) : (
          initial
        )}
      </span>
      <span className="font-medium">{label}</span>
      <span
        className={cn(
          'rounded-full px-1.5 py-0.5 text-caption tabular-nums',
          active ? 'bg-white/85 text-brand-ink' : 'bg-white/50 text-ink-3',
        )}
      >
        {copy.pages.models.vendorCount(count)}
      </span>
    </button>
  )
}

export interface VendorPillsProps {
  options: VendorPillOption[]
  active: string | null
  onSelect: (vendor: string | null) => void
  allLabel: string
  allCount: number
  ariaLabel: string
}

/** Horizontally scrollable vendor row; the first pill clears the filter. */
export function VendorPills({
  options,
  active,
  onSelect,
  allLabel,
  allCount,
  ariaLabel,
}: VendorPillsProps) {
  return (
    <div
      data-testid="vendor-pills"
      role="group"
      aria-label={ariaLabel}
      className="-mx-1 flex min-w-0 items-center gap-2 overflow-x-auto px-1 pb-1 [scrollbar-width:thin]"
    >
      <VendorPill
        label={allLabel}
        count={allCount}
        active={active === null}
        onClick={() => onSelect(null)}
      />
      {options.map((option) => (
        <VendorPill
          key={option.value}
          label={option.label}
          logoUrl={option.logoUrl}
          count={option.count}
          active={active === option.value}
          onClick={() => onSelect(active === option.value ? null : option.value)}
        />
      ))}
    </div>
  )
}
