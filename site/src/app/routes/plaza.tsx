import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { plazaModelsQuery, type PlazaModel } from '@/features/market/api'
import {
  CapabilityBadges,
  CopyModelButton,
  LatencyThroughput,
  ModelLogoMark,
  PriceRows,
  SlaBar,
} from '@/features/market/ModelCardParts'
import { modelLogo } from '@/features/market/model-logo'
import { vendorLabel, vendorLogo } from '@/features/market/vendors'
import { cn } from '@/lib/cn'
import { Empty, Input, PageHeader } from '@/ui'

export const Route = createFileRoute('/plaza')({
  component: PlazaPage,
})

export function PlazaPage() {
  const plazaQuery = useQuery(plazaModelsQuery())
  const [search, setSearch] = useState('')
  const [selectedVendor, setSelectedVendor] = useState('all')

  const models = plazaQuery.data ?? []

  const vendors = useMemo(() => {
    const set = new Set<string>()
    for (const m of models) {
      const v = m.origin_vendor || m.vendor
      if (v) set.add(v)
    }
    return Array.from(set).sort()
  }, [models])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return models.filter((m) => {
      const vendor = (m.origin_vendor || m.vendor || '').toLowerCase()
      if (selectedVendor !== 'all' && vendor !== selectedVendor.toLowerCase()) {
        return false
      }
      if (q) {
        const nameMatch = m.model.toLowerCase().includes(q)
        const vendorMatch = vendor.includes(q)
        const aliasMatch = m.aliases?.some((a) => a.toLowerCase().includes(q)) ?? false
        if (!nameMatch && !vendorMatch && !aliasMatch) return false
      }
      return true
    })
  }, [models, search, selectedVendor])

  return (
    <div className="container mx-auto max-w-6xl px-4 py-8">
      <PageHeader
        title={copy.pages.plaza.title}
        lead={copy.pages.plaza.lead}
      />

      {/* Filters */}
      <div className="mt-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="max-w-md flex-1">
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={copy.pages.plaza.searchPlaceholder}
            aria-label={copy.pages.plaza.searchAria}
          />
        </div>

        <div className="flex flex-wrap items-center gap-1.5 overflow-x-auto pb-1">
          <button
            type="button"
            onClick={() => setSelectedVendor('all')}
            className={cn(
              'rounded-full px-3 py-1 text-meta font-medium transition-colors cursor-pointer',
              selectedVendor === 'all'
                ? 'bg-brand text-white'
                : 'bg-surface text-ink-2 hover:bg-surface-hover hover:text-ink border border-line',
            )}
          >
            {copy.pages.plaza.allVendors}
          </button>
          {vendors.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setSelectedVendor(v)}
              className={cn(
                'rounded-full px-3 py-1 text-meta font-medium capitalize transition-colors cursor-pointer',
                selectedVendor === v
                  ? 'bg-brand text-white'
                  : 'bg-surface text-ink-2 hover:bg-surface-hover hover:text-ink border border-line',
              )}
            >
              {v}
            </button>
          ))}
        </div>
      </div>

      {/* Content */}
      {plazaQuery.isLoading ? (
        <div className="mt-8 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div
              key={i}
              className="h-48 rounded-xl border border-line bg-surface/50 animate-pulse"
            />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <div className="mt-12">
          <Empty
            title={copy.pages.plaza.empty}
            reason="没有找到匹配的模型，请尝试其他关键词或厂商筛选。"
          />
        </div>
      ) : (
        <div className="mt-8 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filtered.map((model) => (
            <PlazaModelCard key={model.model} item={model} />
          ))}
        </div>
      )}
    </div>
  )
}

/** Discount percentage of the plaza price over the base price, or null. */
function discountPercent(item: PlazaModel): number | null {
  const base = Number(item.base_prices?.input_micro_per_mtok ?? 0)
  const price = Number(item.prices?.input_micro_per_mtok ?? 0)
  if (!(base > 0) || !(price > 0) || price >= base) return null
  return Math.round((1 - price / base) * 100)
}

export function PlazaModelCard({ item }: { item: PlazaModel }) {
  const origin = item.origin_vendor || item.vendor || ''
  const logo = modelLogo(item.model) || vendorLogo(origin)
  const discount = discountPercent(item)

  return (
    <div className="group relative flex flex-col justify-between rounded-2xl border border-line bg-surface p-5 transition-all hover:border-brand/40 hover:shadow-md hover:-translate-y-0.5">
      <div>
        {/* Header: Logo, Model Name, Copy Icon, Capabilities */}
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2.5">
            <ModelLogoMark model={item.model} logo={logo} />
            <span
              className="truncate text-[15px] font-semibold text-ink transition-colors group-hover:text-brand-ink"
              title={item.model}
            >
              {item.model}
            </span>
            <CopyModelButton model={item.model} />
          </div>
          <CapabilityBadges model={item.model} capabilities={item.capabilities} />
        </div>

        {/* Subtitle: Vendor */}
        <p className="mt-1 text-caption text-ink-3">{vendorLabel(origin)}</p>

        {/* Pricing Rows */}
        <div className="mt-4">
          {item.prices ? (
            <>
              <PriceRows prices={item.prices} basePrices={discount ? item.base_prices : undefined} />
              {discount ? (
                <div className="mt-2">
                  <span className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
                    省 {discount}%
                  </span>
                </div>
              ) : null}
            </>
          ) : (
            <p className="text-meta text-ink-3">{copy.common.none}</p>
          )}
        </div>
      </div>

      {/* Card Bottom: SLA / Metrics & Footer */}
      <div className="mt-5 pt-3 border-t border-line/60 space-y-2">
        <SlaBar model={item.model} metrics={item.metrics} />

        <div className="flex items-center justify-between text-caption text-ink-3">
          <LatencyThroughput
            metrics={item.metrics}
            contextLength={item.context_length}
          />
        </div>
      </div>
    </div>
  )
}
