import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import { filterPickerModels } from '@/features/market/ModelPicker'
import { ModelTable, originVendorOf } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { VendorPills } from '@/features/market/VendorPills'
import { toVendorLookup, vendorLogo, vendorOptions } from '@/features/market/vendors'
import { vendorsQuery } from '@/features/seller/api'
import { formatMultiplier } from '@/lib/multiplier'
import { formatYuan } from '@/lib/money'
import { Button, Empty, Input, PageHeader, buttonClasses } from '@/ui'
import type { SquareModel } from '@/features/market/api'

interface ModelsSearch {
  vendor?: string
  q?: string
}

const searchSchema = (search: Record<string, unknown>): ModelsSearch => {
  const vendor = typeof search.vendor === 'string' ? search.vendor.trim() : ''
  const q = typeof search.q === 'string' ? search.q : ''
  return {
    ...(vendor ? { vendor } : {}),
    ...(q.trim() ? { q } : {}),
  }
}

export const Route = createFileRoute('/models/')({
  validateSearch: searchSchema,
  component: ModelsPage,
})

/**
 * The square is contractually one row per standard model. Older payloads may
 * still repeat a model per channel, so collapse defensively and keep the row
 * with the most live supply.
 */
function uniqueByModel(rows: SquareModel[]): SquareModel[] {
  const byModel = new Map<string, SquareModel>()
  for (const row of rows) {
    const current = byModel.get(row.model)
    if (!current || row.listing_count > current.listing_count) byModel.set(row.model, row)
  }
  return Array.from(byModel.values())
}

/** Cheapest input price across every tier the model publishes. */
function lowestInputMicro(model: SquareModel): number | null {
  let lowest: number | null = null
  for (const price of Object.values(model.prices)) {
    if (!price) continue
    const value = Number(price.input_micro_per_mtok)
    if (!Number.isFinite(value) || value < 0) continue
    if (lowest === null || value < lowest) lowest = value
  }
  return lowest
}

export function ModelsPage() {
  const modelsCopy = copy.pages.models
  const navigate = useNavigate()
  const { vendor: vendorParam, q: queryParam } = Route.useSearch()
  const vendor = vendorParam ?? null
  const query = (queryParam ?? '').trim()

  const models = useQuery(squareModelsQuery())
  const vendors = useQuery(vendorsQuery())
  const [searchInput, setSearchInput] = useState(queryParam ?? '')

  useEffect(() => {
    setSearchInput(queryParam ?? '')
  }, [queryParam])

  const rows = useMemo(() => uniqueByModel(models.data ?? []), [models.data])
  const vendorMeta = useMemo(() => toVendorLookup(vendors.data ?? []), [vendors.data])

  const vendorCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const row of rows) {
      const origin = originVendorOf(row)
      counts.set(origin, (counts.get(origin) ?? 0) + 1)
    }
    return counts
  }, [rows])

  const pillOptions = useMemo(
    () =>
      vendorOptions(rows.map((row) => originVendorOf(row)), vendorMeta).map((option) => ({
        ...option,
        logoUrl: vendorLogo(option.value, vendorMeta),
        count: vendorCounts.get(option.value) ?? 0,
      })),
    [rows, vendorMeta, vendorCounts],
  )

  const filtered = useMemo(
    () => filterPickerModels(rows, vendor ?? 'all', queryParam ?? ''),
    [rows, vendor, queryParam],
  )

  const vendorModels = useMemo(() => {
    if (!vendor) return []
    return rows
      .filter((row) => originVendorOf(row) === vendor)
      .sort((a, b) => a.model.localeCompare(b.model))
  }, [rows, vendor])

  // Detail queries carry 官方价 and 最低倍率, which the list endpoint omits.
  // Fetch for the whole catalog so the header's lowest multiplier is stable
  // while a vendor pill or search narrows the table.
  const { details, loading: detailsLoading } = useSquareModelDetails(rows)

  const lowestMilli = useMemo(() => {
    let lowest: number | null = null
    for (const detail of details.values()) {
      // Official-only models have no multiplier; they must not drag the
      // headline "最低倍率" down to zero.
      if (detail.multiplier_min_milli <= 0 || detail.listing_count === 0) continue
      if (lowest === null || detail.multiplier_min_milli < lowest) {
        lowest = detail.multiplier_min_milli
      }
    }
    return lowest
  }, [details])

  const hasFilters = vendor !== null || query.length > 0

  function selectVendor(next: string | null) {
    void navigate({
      to: '/models',
      search: (previous) => ({ ...previous, vendor: next ?? undefined }),
      resetScroll: false,
    })
  }

  function updateSearch(value: string) {
    setSearchInput(value)
    void navigate({
      to: '/models',
      search: (previous) => ({
        ...previous,
        q: value.trim() ? value : undefined,
      }),
      replace: true,
      resetScroll: false,
    })
  }

  function clearFilters() {
    setSearchInput('')
    void navigate({ to: '/models', search: {}, resetScroll: false })
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

      <div className="mt-8 flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0 flex-1">
          <VendorPills
            options={pillOptions}
            active={vendor}
            onSelect={selectVendor}
            allLabel={modelsCopy.allVendors}
            allCount={rows.length}
            ariaLabel={modelsCopy.vendorAria}
          />
          {vendor ? (
            <div
              data-testid="vendor-model-chips"
              className="-mx-1 mt-2 flex min-w-0 items-stretch gap-2 overflow-x-auto px-1 pb-1 [scrollbar-width:thin]"
            >
              {vendorModels.map((model) => {
                const input = lowestInputMicro(model)
                return (
                  <Link
                    key={model.model}
                    to="/models/$id"
                    params={{ id: model.model }}
                    className="shrink-0 rounded-md border border-line bg-bg px-3 py-2 transition-colors hover:border-line-strong hover:bg-bg-soft"
                  >
                    <span className="block whitespace-nowrap text-[13px] font-medium text-ink">
                      {model.model}
                    </span>
                    <span className="mt-0.5 block whitespace-nowrap text-[12px] tabular-nums text-ink-3">
                      {modelsCopy.modelRow.priceLabel}{' '}
                      {input === null
                        ? copy.common.none
                        : `${formatYuan(input)}${modelsCopy.modelRow.perMillion}`}
                    </span>
                  </Link>
                )
              })}
            </div>
          ) : null}
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
            value={searchInput}
            onChange={(event) => updateSearch(event.target.value)}
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
            <ModelTable
              models={filtered}
              details={details}
              detailsLoading={detailsLoading}
              vendorMeta={vendorMeta}
              showAddToKey
              minWidth={1160}
            />
          </>
        )}
      </div>

      <p className="mt-4 text-[13px] text-ink-3">{modelsCopy.priceNote}</p>
    </div>
  )
}
