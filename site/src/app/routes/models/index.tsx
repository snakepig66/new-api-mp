import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import { ModelCard } from '@/features/market/ModelCard'
import { filterPickerModels } from '@/features/market/ModelPicker'
import { ModelTable, originVendorOf } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { VendorPills } from '@/features/market/VendorPills'
import { toVendorLookup, vendorLogo, vendorOptions } from '@/features/market/vendors'
import { vendorsQuery } from '@/features/seller/api'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { Button, Empty, Input, PageHeader, buttonClasses } from '@/ui'
import type { SquareModel, SquareTierPrices } from '@/features/market/api'

type SortKey = 'price' | 'success' | 'latency'
type ViewMode = 'cards' | 'table'

interface ModelsSearch {
  vendor?: string
  q?: string
  sort?: SortKey
  official?: boolean
  free?: boolean
  view?: ViewMode
}

const searchSchema = (search: Record<string, unknown>): ModelsSearch => {
  const vendor = typeof search.vendor === 'string' ? search.vendor.trim() : ''
  const q = typeof search.q === 'string' ? search.q : ''
  const sort =
    search.sort === 'price' || search.sort === 'success' || search.sort === 'latency'
      ? search.sort
      : undefined
  const view = search.view === 'table' || search.view === 'cards' ? search.view : undefined
  return {
    ...(vendor ? { vendor } : {}),
    ...(q.trim() ? { q } : {}),
    ...(sort ? { sort } : {}),
    ...(search.official === true ? { official: true } : {}),
    ...(search.free === true ? { free: true } : {}),
    ...(view ? { view } : {}),
  }
}

export const Route = createFileRoute('/models/')({
  validateSearch: searchSchema,
  component: ModelsPage,
})

/**
 * The square is contractually one row per standard model. Models with no
 * channels or listings are hidden from the square.
 */
function uniqueByModel(rows: SquareModel[]): SquareModel[] {
  const byModel = new Map<string, SquareModel>()
  for (const row of rows) {
    if ((row.channel_count ?? 0) <= 0 || (row.listing_count ?? 0) <= 0) continue
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

function FlatPriceNote() {
  return (
    <div className="mb-6 glass-inset px-4 py-3 text-meta text-ink-2">
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
          ? 'rounded-full bg-brand-soft px-3 py-1.5 text-meta font-medium text-brand-ink'
          : 'rounded-full border border-line px-3 py-1.5 text-meta text-ink-2 transition-colors hover:border-line-strong hover:text-ink'
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
  const sort = search.sort ?? 'price'
  const officialOnly = Boolean(search.official)
  const freeOnly = Boolean(search.free)
  const view = search.view ?? 'cards'

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
    switch (sort) {
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
      case 'price':
      default:
        return list.sort((a, b) => (inputMicro(a) ?? Infinity) - (inputMicro(b) ?? Infinity))
    }
  }, [filtered, sort])

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
  const hasSort = sort !== 'price'

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
            <span className="text-meta text-ink-3">{modelsCopy.filters.sort}</span>
            {(
              [
                ['price', modelsCopy.filters.sortPrice],
                ['success', modelsCopy.filters.sortSuccess],
                ['latency', modelsCopy.filters.sortLatency],
              ] as const
            ).map(([value, label]) => (
              <FilterPill
                key={value}
                active={sort === value}
                onClick={() => patchSearch({ sort: value === 'price' ? undefined : value })}
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

        <div className="flex w-full flex-col gap-3 shrink-0 sm:flex-row sm:items-center lg:w-auto">
          <div className="flex items-center rounded-full border border-line bg-surface p-1">
            <button
              type="button"
              onClick={() => patchSearch({ view: undefined })}
              className={cn(
                'flex items-center gap-1.5 rounded-full px-3 py-1 text-meta font-medium transition-colors',
                view === 'cards'
                  ? 'bg-brand-soft text-brand-ink'
                  : 'text-ink-3 hover:text-ink',
              )}
              title="卡片视图"
              aria-label="卡片视图"
              aria-pressed={view === 'cards'}
            >
              <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <rect width="7" height="7" x="3" y="3" rx="1" />
                <rect width="7" height="7" x="14" y="14" rx="1" />
                <rect width="7" height="7" x="14" y="3" rx="1" />
                <rect width="7" height="7" x="3" y="14" rx="1" />
              </svg>
              <span>卡片</span>
            </button>
            <button
              type="button"
              onClick={() => patchSearch({ view: 'table' })}
              className={cn(
                'flex items-center gap-1.5 rounded-full px-3 py-1 text-meta font-medium transition-colors',
                view === 'table'
                  ? 'bg-brand-soft text-brand-ink'
                  : 'text-ink-3 hover:text-ink',
              )}
              title="表格视图"
              aria-label="表格视图"
              aria-pressed={view === 'table'}
            >
              <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <line x1="3" x2="21" y1="6" y2="6" />
                <line x1="3" x2="21" y1="12" y2="12" />
                <line x1="3" x2="21" y1="18" y2="18" />
              </svg>
              <span>表格</span>
            </button>
          </div>

          <div className="relative w-full sm:w-[240px]">
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
              className="h-10 rounded-full pl-10 text-body"
            />
          </div>
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
          view === 'cards' ? (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {Array.from({ length: 8 }, (_, idx) => (
                <div
                  key={idx}
                  className="flex flex-col justify-between rounded-2xl border border-line bg-surface p-5 h-64 animate-pulse"
                  aria-hidden="true"
                >
                  <div className="space-y-3">
                    <div className="flex items-center gap-2.5">
                      <div className="h-5.5 w-5.5 rounded bg-line" />
                      <div className="h-4 w-32 rounded bg-line" />
                    </div>
                    <div className="h-3 w-16 rounded bg-line" />
                    <div className="mt-4 space-y-2">
                      <div className="h-4 w-44 rounded bg-line" />
                      <div className="h-4 w-40 rounded bg-line" />
                      <div className="h-4 w-32 rounded bg-line" />
                    </div>
                  </div>
                  <div className="h-3.5 w-full rounded bg-line" />
                </div>
              ))}
            </div>
          ) : (
            <ModelTable models={[]} loading skeletonRows={8} />
          )
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
              <p className="mb-3 text-meta text-ink-3">
                {modelsCopy.filteredCount(sorted.length, rows.length)}
              </p>
            ) : null}
            {view === 'cards' ? (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {sorted.map((model) => (
                  <ModelCard
                    key={model.model}
                    model={model}
                    detail={details.get(model.model)}
                    vendorMeta={vendorMeta}
                    showAddToKey
                  />
                ))}
              </div>
            ) : (
              <ModelTable
                models={sorted}
                details={details}
                detailsLoading={detailsLoading}
                vendorMeta={vendorMeta}
                showAddToKey
                minWidth={1080}
              />
            )}
          </>
        )}
      </div>

      <p className="mt-4 text-meta text-ink-3">{modelsCopy.priceNote}</p>
    </div>
  )
}
