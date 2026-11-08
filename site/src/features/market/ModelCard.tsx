import { Link } from '@tanstack/react-router'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import type { SquareModel, SquareModelDetail, SquareTierPrices } from './api'
import { AddToKeyMenu } from './AddToKeyMenu'
import {
  CapabilityBadges,
  CopyModelButton,
  LatencyThroughput,
  ModelLogoMark,
  PriceRows,
  SlaBar,
} from './ModelCardParts'
import { modelLogo } from './model-logo'
import { vendorLabel, vendorLogo, type VendorLookup } from './vendors'

export { formatCompactYuan } from './ModelCardParts'

export interface ModelCardProps {
  model: SquareModel
  detail?: SquareModelDetail
  vendorMeta?: VendorLookup
  showAddToKey?: boolean
  className?: string
}

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices.flat
}

function originVendorOf(model: SquareModel): string {
  return model.origin_vendor || model.vendor
}

export function ModelCard({
  model,
  vendorMeta,
  showAddToKey = false,
  className,
}: ModelCardProps) {
  const origin = originVendorOf(model)
  const row = currentPrices(model)
  const logo = modelLogo(model.model) || vendorLogo(origin, vendorMeta)

  const isOnSale = model.listing_count > 0 && model.buyer_price_source !== 'official'

  return (
    <div
      className={cn(
        'group relative flex flex-col justify-between rounded-2xl border border-line bg-surface p-5 transition-all',
        'hover:border-brand/40 hover:shadow-md hover:-translate-y-0.5',
        className,
      )}
    >
      <div>
        {/* Header: Logo, Model Name, Copy Icon, Capabilities */}
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2.5">
            <ModelLogoMark model={model.model} logo={logo} />
            <Link
              to="/models/$id"
              params={{ id: model.model }}
              className="truncate text-[15px] font-semibold text-ink transition-colors group-hover:text-brand-ink"
              title={model.model}
            >
              {model.model}
            </Link>
            <CopyModelButton model={model.model} />
          </div>
          <CapabilityBadges model={model.model} capabilities={model.capabilities} />
        </div>

        {/* Subtitle: Vendor */}
        <p className="mt-1 text-caption text-ink-3">
          {vendorLabel(origin, vendorMeta)}
        </p>

        {/* Pricing Rows */}
        <div className="mt-4">
          {!isOnSale ? (
            <p className="text-meta text-ink-3">暂未开售</p>
          ) : row ? (
            <PriceRows prices={row} />
          ) : (
            <p className="text-meta text-ink-3">{copy.common.none}</p>
          )}
        </div>
      </div>

      {/* Card Bottom: SLA / Metrics & Footer */}
      <div className="mt-5 pt-3 border-t border-line/60 space-y-2">
        <SlaBar model={model.model} metrics={model.metrics} />

        {/* Latency & Throughput footer */}
        <div className="flex items-center justify-between text-caption text-ink-3">
          <LatencyThroughput
            metrics={model.metrics}
            contextLength={model.context_length}
            channelCount={model.channel_count}
          />

          {showAddToKey && (
            <div onClick={(e) => e.stopPropagation()} className="shrink-0 ml-2">
              <AddToKeyMenu model={model.model} hasSupply={model.listing_count > 0} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
