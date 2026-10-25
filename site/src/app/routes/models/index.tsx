import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import { ModelTable } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { vendorOptions } from '@/features/market/vendors'
import { cn } from '@/lib/cn'
import { formatMultiplier } from '@/lib/multiplier'
import { Button, Empty, Input, PageHeader, buttonClasses } from '@/ui'

export const Route = createFileRoute('/models/')({
  component: ModelsPage,
})

function Pill({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        'inline-flex h-9 shrink-0 items-center rounded-full px-4 text-[14px] transition-colors',
        active
          ? 'bg-cta font-medium text-white hover:bg-cta-hover'
          : 'bg-bg-soft text-ink-2 hover:bg-line/70 hover:text-ink',
      )}
    >
      {children}
    </button>
  )
}

export function ModelsPage() {
  const modelsCopy = copy.pages.models
  const models = useQuery(squareModelsQuery())
  const [vendor, setVendor] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  const rows = models.data ?? []
  const pills = useMemo(() => vendorOptions(rows.map((row) => row.vendor)), [rows])
  const query = search.trim().toLowerCase()

  const filtered = useMemo(() => {
    return rows.filter((row) => {
      if (vendor && row.vendor !== vendor) return false
      if (query && !row.model.toLowerCase().includes(query)) return false
      return true
    })
  }, [rows, vendor, query])

  // Detail queries carry 官方价 and 最低倍率, which the list endpoint omits.
  // Fetch for the whole catalog so the header's lowest multiplier is stable
  // while a vendor pill or search narrows the table.
  const { details, loading: detailsLoading } = useSquareModelDetails(rows)

  const lowestMilli = useMemo(() => {
    let lowest: number | null = null
    for (const detail of details.values()) {
      if (lowest === null || detail.multiplier_min_milli < lowest) {
        lowest = detail.multiplier_min_milli
      }
    }
    return lowest
  }, [details])

  const hasFilters = vendor !== null || query.length > 0

  function clearFilters() {
    setVendor(null)
    setSearch('')
  }

  return (
    <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
      <PageHeader
        title={modelsCopy.title}
        lead={
          detailsLoading
            ? modelsCopy.count(rows.length)
            : modelsCopy.leadSummary(
                rows.length,
                lowestMilli === null ? null : formatMultiplier(lowestMilli),
              )
        }
        className="border-b-0 pb-0"
      />

      <div className="mt-8 flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="-mx-1 flex items-center gap-2 overflow-x-auto px-1 pb-1">
          <Pill active={vendor === null} onClick={() => setVendor(null)}>
            {modelsCopy.allVendors}
          </Pill>
          {pills.map((pill) => (
            <Pill
              key={pill.value}
              active={vendor === pill.value}
              onClick={() => setVendor(vendor === pill.value ? null : pill.value)}
            >
              {pill.label}
            </Pill>
          ))}
        </div>

        <div className="relative w-full shrink-0 lg:w-[260px]">
          <svg
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
            className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-ink-3"
          >
            <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.5" />
            <path d="m10.5 10.5 3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
          <Input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={modelsCopy.searchPlaceholder}
            aria-label={modelsCopy.searchAria}
            className="h-10 rounded-full pl-10 text-[14px]"
          />
        </div>
      </div>

      <div className="mt-6">
        {models.isError ? (
          <QueryError
            title={modelsCopy.error.title}
            reason={modelsCopy.error.reason}
            onRetry={() => void models.refetch()}
            retrying={models.isFetching}
          />
        ) : models.isPending ? (
          <ModelTable models={[]} loading skeletonRows={8} />
        ) : rows.length === 0 ? (
          <Empty
            title={modelsCopy.empty.title}
            reason={modelsCopy.empty.reason}
            next={modelsCopy.empty.next}
            action={
              <div className="flex flex-wrap gap-2">
                <Link to="/docs" className={buttonClasses('secondary', 'md')}>
                  {copy.actions.viewDocs}
                </Link>
                <Link to="/seller" className={buttonClasses('primary', 'md')}>
                  {copy.actions.becomeSeller}
                </Link>
              </div>
            }
          />
        ) : filtered.length === 0 ? (
          <Empty
            title={modelsCopy.filteredEmpty.title}
            reason={modelsCopy.filteredEmpty.reason}
            next={modelsCopy.filteredEmpty.next}
            action={
              <Button variant="secondary" size="md" onClick={clearFilters}>
                {modelsCopy.filters.clear}
              </Button>
            }
          />
        ) : (
          <>
            {hasFilters ? (
              <p className="mb-3 text-[13px] text-ink-3">
                {modelsCopy.filteredCount(filtered.length, rows.length)}
              </p>
            ) : null}
            <ModelTable models={filtered} details={details} detailsLoading={detailsLoading} />
          </>
        )}
      </div>

      <p className="mt-4 text-[13px] text-ink-3">{modelsCopy.priceNote}</p>
    </div>
  )
}
