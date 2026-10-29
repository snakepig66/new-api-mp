/**
 * Shared summary bar for the usage-records page: one row of compact Stat tiles fed by
 * the buyer (usageSummaryQuery) or seller (shareSummaryQuery) totals.
 *
 * Space-efficient ribbon layout with minimal vertical height to prioritize table visibility.
 */

import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import type { StatTone } from '@/ui'

const summaryCopy = copy.pages.records.summary

/** Skeleton fallback while loading: five blank tiles fill the lg grid. */
const EMPTY_SUMMARY_ITEMS: RecordsSummaryItem[] = Array.from({ length: 5 }, () => ({
  label: '',
  value: '',
}))

const toneClass: Record<StatTone, string> = {
  default: 'text-ink',
  brand: 'text-brand-ink',
  success: 'text-brand-ink',
  warning: 'text-warn',
  danger: 'text-danger',
}

export interface RecordsSummaryItem {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: StatTone
}

export interface RecordsSummaryBarProps {
  /** 4–6 tiles; the caller decides what the totals mean per view. */
  items: RecordsSummaryItem[]
  /** Show a skeleton row while the summary query loads. */
  loading?: boolean
  className?: string
}

export function RecordsSummaryBar({ items, loading = false, className }: RecordsSummaryBarProps) {
  // A loading view still needs tiles to skeleton; fall back to five blanks.
  const shown = loading && items.length === 0 ? EMPTY_SUMMARY_ITEMS : items

  return (
    <div
      aria-busy={loading || undefined}
      aria-label={summaryCopy.ariaLabel}
      className={cn(
        'grid grid-cols-2 gap-3 glass py-2.5 px-4 sm:grid-cols-3 sm:py-2.5 sm:px-5',
        shown.length <= 5 ? 'lg:grid-cols-5' : 'lg:grid-cols-6',
        'lg:divide-x lg:divide-line/30',
        className,
      )}
    >
      {shown.map((item, index) =>
        loading ? (
          <div key={`summary-skeleton-${index}`} className="min-w-0 lg:first:pl-0 lg:pl-4">
            <div className="h-3 w-14 animate-pulse rounded-sm bg-white/60 motion-reduce:animate-none" />
            <div className="mt-1 h-5 w-20 animate-pulse rounded-sm bg-white/60 motion-reduce:animate-none" />
          </div>
        ) : (
          <div
            key={item.label}
            className="min-w-0 flex flex-col justify-center lg:first:pl-0 lg:pl-4"
          >
            <div className="text-[11px] font-medium leading-4 text-ink-3 truncate">
              {item.label}
            </div>
            <div className="mt-0.5 flex flex-wrap items-baseline gap-x-1.5">
              <span
                className={cn(
                  'text-title font-semibold leading-5 tabular-nums tracking-tight',
                  item.tone && toneClass[item.tone] ? toneClass[item.tone] : 'text-ink',
                )}
              >
                {item.value}
              </span>
              {item.hint ? (
                <span className="text-[11px] leading-4 text-ink-3 font-normal truncate">
                  {item.hint}
                </span>
              ) : null}
            </div>
          </div>
        ),
      )}
    </div>
  )
}
