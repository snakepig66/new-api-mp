import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { cn } from '@/lib/cn'
import { Button, Empty, Input } from '@/ui'

import { squareModelsQuery, type SquareModel, type SquareTierPrices } from './api'
import { originVendorOf } from './model-table'
import { toVendorLookup, vendorLabel, vendorOptions } from './vendors'
import { vendorsQuery } from '@/features/seller/api'

const pickerCopy = copy.pages.models.picker

export type ModelSelection = Record<string, string>

export interface ModelPickerProps {
  /** model id -> typed per-model price cap ('' = no cap). */
  selection: ModelSelection
  onChange: (next: ModelSelection) => void
  disabled?: boolean
  idPrefix?: string
  className?: string
  /** Optional overrides for tests and callers that already hold the data. */
  models?: SquareModel[]
  vendors?: { id: string; display_name: string; tier: string; kind?: string }[]
  loading?: boolean
  error?: unknown
  onRetry?: () => void
}

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices[model.tier] ?? model.prices.peak ?? model.prices.offpeak
}

/** Filter by origin vendor and by standard name/alias only (never by channel). */
export function filterPickerModels(
  models: SquareModel[],
  vendor: string,
  search: string,
): SquareModel[] {
  const needle = search.trim().toLowerCase()
  return models.filter((model) => {
    if (vendor !== 'all' && originVendorOf(model) !== vendor) return false
    if (!needle) return true
    if (model.model.toLowerCase().includes(needle)) return true
    return (model.aliases ?? []).some((alias) => alias.toLowerCase().includes(needle))
  })
}

export interface VendorGroup {
  vendor: string
  label: string
  models: SquareModel[]
}

/** Group filtered models by their original vendor; groups and rows are stable. */
export function groupPickerModels(
  models: SquareModel[],
  labels: (vendor: string) => string,
): VendorGroup[] {
  const byVendor = new Map<string, SquareModel[]>()
  for (const model of models) {
    const vendor = originVendorOf(model)
    const list = byVendor.get(vendor) ?? []
    list.push(model)
    byVendor.set(vendor, list)
  }
  return [...byVendor.entries()]
    .map(([vendor, rows]) => ({
      vendor,
      label: labels(vendor),
      models: rows.sort((a, b) => a.model.localeCompare(b.model)),
    }))
    .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
}

/** Add or remove every model of one group while preserving other selections. */
export function toggleGroupSelection(
  selection: ModelSelection,
  group: VendorGroup,
): ModelSelection {
  const allSelected = group.models.every((model) => model.model in selection)
  const next = { ...selection }
  if (allSelected) {
    for (const model of group.models) delete next[model.model]
  } else {
    for (const model of group.models) {
      if (!(model.model in next)) next[model.model] = ''
    }
  }
  return next
}

export function ModelPicker({
  selection,
  onChange,
  disabled = false,
  idPrefix = 'model-picker',
  className,
  models,
  vendors,
  loading,
  error,
  onRetry,
}: ModelPickerProps) {
  const squareQuery = useQuery({ ...squareModelsQuery(), enabled: models === undefined })
  const vendorsQueryResult = useQuery({ ...vendorsQuery(), enabled: vendors === undefined })
  const [vendor, setVendor] = useState('all')
  const [search, setSearch] = useState('')

  const allModels = models ?? squareQuery.data ?? []
  const vendorList = vendors ?? vendorsQueryResult.data ?? []
  const lookup = useMemo(() => toVendorLookup(vendorList), [vendorList])
  const labelFor = (slug: string) => vendorLabel(slug, lookup)

  const originVendors = useMemo(
    () => vendorOptions(new Set(allModels.map((model) => originVendorOf(model))), lookup),
    [allModels, lookup],
  )
  const filtered = useMemo(
    () => filterPickerModels(allModels, vendor, search),
    [allModels, vendor, search],
  )
  const groups = useMemo(() => groupPickerModels(filtered, labelFor), [filtered, lookup])

  const isPending = loading ?? (models === undefined && squareQuery.isPending)
  const loadError = error ?? (models === undefined ? squareQuery.error : undefined)
  const retry = onRetry ?? (() => void squareQuery.refetch())
  const selectedCount = Object.keys(selection).length

  function toggle(model: SquareModel) {
    const next = { ...selection }
    if (model.model in next) delete next[model.model]
    else next[model.model] = ''
    onChange(next)
  }

  function setCap(model: string, value: string) {
    onChange({ ...selection, [model]: value })
  }

  function selectAllFiltered() {
    const next = { ...selection }
    for (const model of filtered) {
      if (!(model.model in next)) next[model.model] = ''
    }
    onChange(next)
  }

  function clearFilters() {
    setVendor('all')
    setSearch('')
  }

  return (
    <div className={cn('min-w-0', className)}>
      <div
        className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:thin]"
        role="tablist"
        aria-label={copy.pages.models.allVendors}
      >
        <VendorPill
          active={vendor === 'all'}
          onClick={() => setVendor('all')}
          label={pickerCopy.allVendors}
        />
        {originVendors.map((option) => (
          <VendorPill
            key={option.value}
            active={vendor === option.value}
            onClick={() => setVendor(option.value)}
            label={option.label}
          />
        ))}
      </div>

      <div className="mt-2">
        <Input
          aria-label={pickerCopy.searchAria}
          placeholder={pickerCopy.searchPlaceholder}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
      </div>

      <div className="mt-2 max-h-[320px] overflow-y-auto rounded-md border border-line">
        {isPending ? (
          <div className="space-y-2 p-3" aria-label={pickerCopy.loading}>
            <div className="h-5 animate-pulse rounded-sm bg-line" />
            <div className="h-5 animate-pulse rounded-sm bg-line" />
            <div className="h-5 animate-pulse rounded-sm bg-line" />
          </div>
        ) : loadError ? (
          <div className="p-4 text-[13px] text-ink-2">
            <p>{pickerCopy.error}</p>
            <Button variant="secondary" size="sm" className="mt-3" onClick={retry}>
              {pickerCopy.retry}
            </Button>
          </div>
        ) : allModels.length === 0 ? (
          <Empty className="border-0 py-8" title={pickerCopy.emptyAll} reason="" />
        ) : filtered.length === 0 ? (
          <Empty
            className="border-0 py-8"
            title={pickerCopy.emptyFiltered}
            reason=""
            action={
              <Button variant="secondary" size="sm" onClick={clearFilters}>
                {pickerCopy.clearFilter}
              </Button>
            }
          />
        ) : (
          <div>
            {groups.map((group) => {
              const groupSelected = group.models.filter((model) => model.model in selection).length
              const allSelected = groupSelected === group.models.length
              return (
                <section key={group.vendor}>
                  <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-line bg-bg/95 px-3 py-2 backdrop-blur supports-[backdrop-filter]:bg-bg/90">
                    <TriStateCheckbox
                      checked={allSelected}
                      indeterminate={groupSelected > 0 && !allSelected}
                      onChange={() => onChange(toggleGroupSelection(selection, group))}
                      label={`${group.label} ${pickerCopy.groupCount(groupSelected, group.models.length)}`}
                    />
                    <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">
                      {group.label}
                    </span>
                    <span className="shrink-0 tabular-nums text-[13px] text-ink-3">
                      {pickerCopy.groupCount(groupSelected, group.models.length)}
                    </span>
                  </div>
                  <ul className="divide-y divide-line">
                    {group.models.map((model) => {
                      const checked = model.model in selection
                      const prices = currentPrices(model)
                      return (
                        <li
                          key={model.model}
                          className={cn(
                            'flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2 transition-colors',
                            checked ? 'bg-brand-soft/40' : 'bg-bg hover:bg-bg-soft/60',
                          )}
                        >
                          <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2.5">
                            <input
                              type="checkbox"
                              className="h-3.5 w-3.5 shrink-0 accent-brand"
                              checked={checked}
                              disabled={disabled}
                              onChange={() => toggle(model)}
                            />
                            <span className="min-w-0">
                              <span className="block truncate text-[13px] text-ink">
                                {model.model}
                              </span>
                              {prices ? (
                                <span className="block text-[12px] tabular-nums text-ink-3">
                                  {pickerCopy.priceLabel} {formatYuan(prices.input_micro_per_mtok)}
                                  /M
                                </span>
                              ) : null}
                            </span>
                          </label>
                          <label className="flex shrink-0 items-center gap-1.5 text-[12px] text-ink-3">
                            <span>{pickerCopy.capLabel}</span>
                            <input
                              id={`${idPrefix}-cap-${model.model}`}
                              type="text"
                              inputMode="decimal"
                              disabled={!checked || disabled}
                              aria-label={`${model.model} ${pickerCopy.capLabel}`}
                              placeholder={pickerCopy.capPlaceholder}
                              value={selection[model.model] ?? ''}
                              onChange={(event) => setCap(model.model, event.target.value)}
                              className="h-8 w-20 rounded-sm border border-transparent bg-bg-soft px-2 tabular-nums text-[13px] text-ink disabled:bg-bg-soft disabled:text-ink-3"
                            />
                          </label>
                        </li>
                      )
                    })}
                  </ul>
                </section>
              )
            })}
          </div>
        )}
      </div>

      <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[13px] text-ink-3">
        <span className="tabular-nums">{pickerCopy.selectedCount(selectedCount)}</span>
        <div className="flex items-center gap-3">
          <button
            type="button"
            className="text-[13px] text-ink-2 underline-offset-2 hover:text-ink hover:underline disabled:text-ink-3"
            disabled={filtered.length === 0 || disabled}
            onClick={selectAllFiltered}
          >
            {pickerCopy.selectAllFiltered}
          </button>
          <button
            type="button"
            className="text-[13px] text-ink-2 underline-offset-2 hover:text-ink hover:underline disabled:text-ink-3"
            disabled={selectedCount === 0 || disabled}
            onClick={() => onChange({})}
          >
            {pickerCopy.clear}
          </button>
        </div>
      </div>
    </div>
  )
}

function VendorPill({
  active,
  onClick,
  label,
}: {
  active: boolean
  onClick: () => void
  label: string
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'shrink-0 rounded-full border px-3 py-1.5 text-[13px] transition-colors',
        active
          ? 'border-brand bg-brand-soft text-brand-ink'
          : 'border-line bg-bg text-ink-2 hover:border-line-strong hover:text-ink',
      )}
    >
      {label}
    </button>
  )
}

function TriStateCheckbox({
  checked,
  indeterminate,
  onChange,
  label,
}: {
  checked: boolean
  indeterminate: boolean
  onChange: () => void
  label: string
}) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate
  }, [indeterminate])
  return (
    <input
      ref={ref}
      type="checkbox"
      aria-label={label}
      className="h-3.5 w-3.5 shrink-0 accent-brand"
      checked={checked}
      onChange={onChange}
    />
  )
}
