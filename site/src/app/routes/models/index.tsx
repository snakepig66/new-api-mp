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
import { formatYuan } from '@/lib/money'
import { Button, Empty, Input, PageHeader, buttonClasses } from '@/ui'
import type { SquareModel, SquareTierPrices } from '@/features/market/api'

type SortKey = 'saving' | 'price' | 'success' | 'latency'

interface ModelsSearch {
  vendor?: string
  q?: string
  sort?: SortKey
  official?: boolean
  free?: boolean
}

const searchSchema = (search: Record<string, unknown>): ModelsSearch => {
  const vendor = typeof search.vendor === 'string' ? search.vendor.trim() : ''
  const q = typeof search.q === 'string' ? search.q : ''
  const sort =
    search.sort === 'price' || search.sort === 'success' || search.sort === 'latency'
      ? search.sort
      : search.sort === 'saving'
        ? 'saving'
        : undefined
  return {
    ...(vendor ? { vendor } : {}),
    ...(q.trim() ? { q } : {}),
    ...(sort ? { sort } : {}),
    ...(search.official === true ? { official: true } : {}),
    ...(search.free === true ? { free: true } : {}),
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

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices.flat
}

function inputMicro(model: SquareModel): number | null {
  const price = currentPrices(model)
  if (!price) return null
  const value = Number(price.input_micro_per_mtok)
  return Number.isFinite(value) ? value : null
}

function savingsOf(
  model: SquareModel,
  officialInput: number | string | undefined,
): number | null {
  const market = inputMicro(model)
  const official = Number(officialInput)
  if (market === null || !Number.isFinite(official) || official <= 0 || market >= official) {
    return null
  }
  return 1 - market / official
}

function FlatPriceNote() {
  return (
    <div className="mb-6 rounded-md border border-line bg-bg-soft px-4 py-3 text-[13px] text-ink-2">
      {copy.pages.models.flatPriceNote}
    </div>
  )
}

function FilterPill({
  active,
  children,
  onClick,
}: {
  active: boolean
  children: React.ReactNode
  onClick: () => void
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={
        active
          ? 'rounded-full bg-brand-soft px-3 py-1.5 text-[13px] font-medium text-brand-ink'
          : 'rounded-full border border-line px-3 py-1.5 text-[13px] text-ink-2 transition-colors hover:border-line-strong hover:text-ink'
      }
    >
      {children}
    </button>
  )
}

export function ModelsPage() {
  const modelsCopy = copy.pages.models
  const navigate = useNavigate()
  const search = Route.useSearch()
  const vendor = search.vendor ?? null
  const query = (search.q ?? '').trim()
  const sort = search.sort ?? 'saving'
  const officialOnly = Boolean(search.official)
  const freeOnly = Boolean(search.free)

  const models = useQuery(squareModelsQuery())
  const vendors = useQuery(vendorsQuery())
  const [searchInput, setSearchInput] = useState(search.q ?? '')

  useEffect(() => {
    setSearchInput(search.q ?? '')
  }, [search.q])

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

  const filtered = useMemo(() => {
    let list = filterPickerModels(rows, vendor ?? 'all', search.q ?? '')
    if (officialOnly) list = list.filter((row) => Boolean(row.official_available))
    if (freeOnly) list = list.filter((row) => inputMicro(row) === 0)
    return list
  }, [rows, vendor, search.q, officialOnly, freeOnly])

  // Detail queries carry the official price and the per-channel reliability.
  const { details, loading: detailsLoading } = useSquareModelDetails(rows)

  const sorted = useMemo(() => {
    const list = [...filtered]
    const officialInput = (model: SquareModel) =>
      details.get(model.model)?.official_prices?.flat?.input_micro_per_mtok
    switch (sort) {
      case 'price':
        return list.sort((a, b) => (inputMicro(a) ?? Infinity) - (inputMicro(b) ?? Infinity))
      case 'success':
        return list.sort(
          (a, b) =>
            (b.metrics?.success_rate_24h_milli ?? -1) - (a.metrics?.success_rate_24h_milli ?? -1),
        )
      case 'latency':
        return list.sort(
          (a, b) =>
            (a.metrics?.latency_ms || Infinity) - (b.metrics?.latency_ms || Infinity),
        )
      case 'saving':
      default:
        return list.sort((a, b) => {
          // Saving needs the official book; models without it rank last.
          const sa = savingsOf(a, officialInput(a)) ?? -1
          const sb = savingsOf(b, officialInput(b)) ?? -1
          if (sa !== sb) return sb - sa
          return (inputMicro(a) ?? Infinity) - (inputMicro(b) ?? Infinity)
        })
    }
  }, [filtered, details, sort])

  const lowestPrice = useMemo(() => {
    let lowest: number | null = null
    for (const model of rows) {
      if (model.listing_count === 0) continue
      const value = inputMicro(model)
      if (value === null) continue
      if (lowest === null || value < lowest) lowest = value
    }
    return lowest
  }, [rows])

  const hasFilters = vendor !== null || query.length > 0 || officialOnly || freeOnly
  const hasSort = sort !== 'saving'

  function patchSearch(patch: Partial<ModelsSearch>) {
    void navigate({
      to: '/models',
      search: (previous) => {
        const next: Record<string, unknown> = { ...previous, ...patch }
        for (const key of Object.keys(next)) {
          if (next[key] === undefined || next[key] === '' || next[key] === false) {
            delete next[key]
          }
        }
        return next as ModelsSearch
      },
      replace: true,
      resetScroll: false,
    })
  }

  function selectVendor(next: string | null) {
    patchSearch({ vendor: next ?? undefined })
  }

  function updateSearch(value: string) {
    setSearchInput(value)
    patchSearch({ q: value.trim() ? value : undefined })
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
                lowestPrice === null ? null : formatYuan(lowestPrice),
              )
        }
        className="border-b-0 pb-0"
      />

      <div className="mt-6">
        <FlatPriceNote />
      </div>

      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0 flex-1">
          <VendorPills
            options={pillOptions}
            active={vendor}
            onSelect={selectVendor}
            allLabel={modelsCopy.allVendors}
            allCount={rows.length}
            ariaLabel={modelsCopy.vendorAria}
          />
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <span className="text-[13px] text-ink-3">{modelsCopy.filters.sort}</span>
            {(
              [
                ['saving', modelsCopy.filters.sortSaving],
                ['price', modelsCopy.filters.sortPrice],
                ['success', modelsCopy.filters.sortSuccess],
                ['latency', modelsCopy.filters.sortLatency],
              ] as const
            ).map(([value, label]) => (
              <FilterPill
                key={value}
                active={sort === value}
                onClick={() => patchSearch({ sort: value === 'saving' ? undefined : value })}
              >
                {label}
              </FilterPill>
            ))}
            <FilterPill active={officialOnly} onClick={() => patchSearch({ official: !officialOnly || undefined })}>
              {modelsCopy.filters.officialOnly}
            </FilterPill>
            <FilterPill active={freeOnly} onClick={() => patchSearch({ free: !freeOnly || undefined })}>
              {modelsCopy.filters.freeOnly}
            </FilterPill>
          </div>
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
                <Link to="/share" className={buttonClasses('primary', 'md')}>
                  {copy.actions.becomeSeller}
                </Link>
              </div>
            }
          />
        ) : sorted.length === 0 ? (
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
            {hasFilters || hasSort ? (
              <p className="mb-3 text-[13px] text-ink-3">
                {modelsCopy.filteredCount(sorted.length, rows.length)}
              </p>
            ) : null}
            <ModelTable
              models={sorted}
              details={details}
              detailsLoading={detailsLoading}
              vendorMeta={vendorMeta}
              showAddToKey
              minWidth={1080}
            />
          </>
        )}
      </div>

      <p className="mt-4 text-[13px] text-ink-3">{modelsCopy.priceNote}</p>
    </div>
  )
}
