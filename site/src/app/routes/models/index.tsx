import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useMemo, useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery, useSquareModelDetails } from '@/features/market/api'
import {
  CAPABILITY_KEYS,
  matchesCapabilities,
  type CapabilityKey,
} from '@/features/market/capabilities'
import { ModelTable } from '@/features/market/model-table'
import { QueryError } from '@/features/market/query-error'
import { cn } from '@/lib/cn'
import { Button, Empty, PageHeader, Tag, buttonClasses } from '@/ui'

export const Route = createFileRoute('/models/')({
  component: ModelsPage,
})

function FilterCheck({
  checked,
  onChange,
  label,
  count,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  count?: number
}) {
  return (
    <label className="flex cursor-pointer items-center justify-between gap-2 py-0.5 text-[12.5px] text-ink">
      <span className="flex min-w-0 items-center gap-2">
        <input
          type="checkbox"
          checked={checked}
          onChange={(event) => onChange(event.target.checked)}
          className="h-3.5 w-3.5 shrink-0 accent-brand-dark"
        />
        <span className="truncate">{label}</span>
      </span>
      {typeof count === 'number' ? (
        <span className="font-numeric text-[11px] text-muted">{count}</span>
      ) : null}
    </label>
  )
}

function FilterGroup({
  title,
  hint,
  children,
}: {
  title?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className="border-t border-line pt-3 first:border-t-0 first:pt-0">
      {title ? <h2 className="text-[11.5px] font-medium text-ink">{title}</h2> : null}
      {hint ? <p className="mt-0.5 text-[10.5px] leading-4 text-muted">{hint}</p> : null}
      <div className="mt-2">{children}</div>
    </div>
  )
}

export function ModelsPage() {
  const modelsCopy = copy.pages.models
  const models = useQuery(squareModelsQuery())
  const [vendors, setVendors] = useState<string[]>([])
  const [capabilities, setCapabilities] = useState<CapabilityKey[]>([])
  const [inStockOnly, setInStockOnly] = useState(false)

  const rows = models.data ?? []
  const vendorOptions = useMemo(
    () => [...new Set(rows.map((row) => row.vendor))].sort((a, b) => a.localeCompare(b)),
    [rows],
  )

  const filtered = useMemo(() => {
    return rows.filter((row) => {
      if (inStockOnly && row.listing_count <= 0) return false
      if (vendors.length > 0 && !vendors.includes(row.vendor)) return false
      return matchesCapabilities(row.model, capabilities)
    })
  }, [rows, vendors, capabilities, inStockOnly])

  // Detail queries fill 官方价 and 最低倍率, which the list endpoint omits.
  const { details, loading: detailsLoading } = useSquareModelDetails(filtered)

  const hasFilters = vendors.length > 0 || capabilities.length > 0 || inStockOnly

  function toggle<T>(list: T[], value: T): T[] {
    return list.includes(value) ? list.filter((item) => item !== value) : [...list, value]
  }

  function clearFilters() {
    setVendors([])
    setCapabilities([])
    setInStockOnly(false)
  }

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-10">
      <PageHeader
        title={modelsCopy.title}
        lead={modelsCopy.lead}
        actions={
          models.data ? (
            <Tag tone="brand">{modelsCopy.count(models.data.length)}</Tag>
          ) : null
        }
      />

      <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-muted">
        <span>{modelsCopy.priceUnit}</span>
        <span>{modelsCopy.priceNote}</span>
      </div>

      <div className="mt-6 grid gap-8 lg:grid-cols-[200px_minmax(0,1fr)] lg:items-start">
        <aside className="border border-line bg-surface px-4 py-4 lg:sticky lg:top-20">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-[12.5px] font-medium text-ink">{modelsCopy.filters.title}</h2>
            {hasFilters ? (
              <button
                type="button"
                onClick={clearFilters}
                className="text-[11.5px] text-brand-dark hover:underline"
              >
                {modelsCopy.filters.clear}
              </button>
            ) : null}
          </div>

          <div className="mt-3 space-y-4">
            <FilterGroup title={modelsCopy.filters.vendor} hint={modelsCopy.filters.vendorHint}>
              {vendorOptions.length === 0 ? (
                <p className="text-[11.5px] text-muted">{copy.common.none}</p>
              ) : (
                vendorOptions.map((vendor) => (
                  <FilterCheck
                    key={vendor}
                    label={vendor}
                    count={rows.filter((row) => row.vendor === vendor).length}
                    checked={vendors.includes(vendor)}
                    onChange={() => setVendors((current) => toggle(current, vendor))}
                  />
                ))
              )}
            </FilterGroup>

            <FilterGroup
              title={modelsCopy.filters.capability}
              hint={modelsCopy.filters.capabilityHint}
            >
              {CAPABILITY_KEYS.map((key) => (
                <FilterCheck
                  key={key}
                  label={modelsCopy.capabilities[key]}
                  checked={capabilities.includes(key)}
                  onChange={() => setCapabilities((current) => toggle(current, key))}
                />
              ))}
            </FilterGroup>

            <FilterGroup hint={modelsCopy.filters.inStockHint}>
              <FilterCheck
                label={modelsCopy.filters.inStock}
                checked={inStockOnly}
                onChange={setInStockOnly}
              />
            </FilterGroup>
          </div>
        </aside>

        <div className="min-w-0">
          <div className="mb-2 flex items-center justify-between gap-3">
            <p className={cn('text-[11.5px] text-muted', models.isPending && 'invisible')}>
              {models.data
                ? hasFilters
                  ? modelsCopy.filteredCount(filtered.length, models.data.length)
                  : modelsCopy.count(models.data.length)
                : null}
            </p>
          </div>

          {models.isError ? (
            <QueryError
              title={modelsCopy.error.title}
              reason={modelsCopy.error.reason}
              onRetry={() => void models.refetch()}
              retrying={models.isFetching}
            />
          ) : models.isPending ? (
            <ModelTable models={[]} loading skeletonRows={8} minWidth={980} />
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
            <ModelTable
              models={filtered}
              details={details}
              detailsLoading={detailsLoading}
              minWidth={980}
            />
          )}
        </div>
      </div>
    </div>
  )
}
