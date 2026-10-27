/**
 * Shared summary bar for the usage-records page: one row of Stat tiles fed by
 * the buyer (usageSummaryQuery) or seller (shareSummaryQuery) totals.
 *
 * The bar is layout only — labels, values and hints are computed by each view
 * so the money/token formatting stays next to the query that produced it.
 * Mobile keeps the tiles legible by wrapping into two columns.
 */

import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { Stat, type StatTone } from '@/ui'

const summaryCopy = copy.pages.records.summary

/** Skeleton fallback while loading: four blank tiles keep the layout stable. */
const EMPTY_SUMMARY_ITEMS: RecordsSummaryItem[] = [
  { label: '', value: '' },
  { label: '', value: '' },
  { label: '', value: '' },
  { label: '', value: '' },
]

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
  // A loading view still needs tiles to skeleton; fall back to four blanks.
  const shown = loading && items.length === 0 ? EMPTY_SUMMARY_ITEMS : items

  return (
    <div
      aria-busy={loading || undefined}
      aria-label={summaryCopy.ariaLabel}
      className={cn(
        'grid grid-cols-2 gap-4 rounded-md border border-line bg-bg p-5 sm:grid-cols-3 lg:grid-cols-6',
        className,
      )}
    >
      {shown.map((item) =>
        loading ? (
          <div key={item.label} className="min-w-0">
            <div className="h-3.5 w-16 animate-pulse rounded-sm bg-bg-soft motion-reduce:animate-none" />
            <div className="mt-2 h-7 w-24 animate-pulse rounded-sm bg-bg-soft motion-reduce:animate-none" />
          </div>
        ) : (
          <Stat
            key={item.label}
            label={item.label}
            value={item.value}
            hint={item.hint}
            tone={item.tone}
          />
        ),
      )}
    </div>
  )
}
