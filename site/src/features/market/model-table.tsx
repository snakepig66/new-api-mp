import { Link } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatMultiplier } from '@/lib/multiplier'
import {
  PeakBadge,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
} from '@/ui'

import type { SquareModel, SquareModelDetail, SquareTierPrices } from './api'
import { isOfficialVendor, vendorLabel, type VendorLookup } from './vendors'

export interface ModelTableProps {
  models: SquareModel[]
  /** Detail enrichment for 官方价 / 最低倍率 (the list endpoint lacks both). */
  details?: Map<string, SquareModelDetail>
  detailsLoading?: boolean
  /** Vendor catalogue metadata; enables display names and the official tag. */
  vendorMeta?: VendorLookup
  loading?: boolean
  skeletonRows?: number
  minWidth?: number
}

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices[model.tier] ?? model.prices.peak ?? model.prices.offpeak
}

/** The standard model's 原厂 slug, with a fallback for older payloads. */
export function originVendorOf(model: SquareModel): string {
  return model.origin_vendor || model.vendor
}

function skeletonCell(width = 'w-16') {
  return <div className={`h-3.5 animate-pulse rounded-sm bg-line ${width}`} />
}

function Missing() {
  return <span className="text-ink-3">{copy.common.none}</span>
}

/** Buyer input/output pair, stacked so one row stays 56px tall. */
function BuyerPair({ prices }: { prices?: SquareTierPrices }) {
  if (!prices) return <Missing />
  return (
    <div className="leading-tight tabular-nums">
      <div>{formatYuan(prices.input_micro_per_mtok)}</div>
      <div className="mt-0.5 text-[13px] text-ink-3">
        {copy.pages.models.outputPrefix} {formatYuan(prices.output_micro_per_mtok)}
      </div>
    </div>
  )
}

/**
 * The model board shared by the homepage (top 6) and the full square.
 *
 * One row is one standard model: upstream channels are aggregated into the
 * channel count. When no listing is live the row advertises the official
 * price and says so with a 官方价 tag.
 */
export function ModelTable({
  models,
  details,
  detailsLoading = false,
  vendorMeta,
  loading = false,
  skeletonRows = 6,
  minWidth = 1040,
}: ModelTableProps) {
  const columns = copy.pages.models.columns

  return (
    <Table minWidth={minWidth}>
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{columns.model}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.input}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.output}</TableHeaderCell>
          <TableHeaderCell>{columns.multiplier}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.buyerPrice}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.channels}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.sellers}</TableHeaderCell>
          <TableHeaderCell>{columns.tier}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {loading
          ? Array.from({ length: skeletonRows }, (_, index) => (
              <TableRow
                key={`skeleton-${index}`}
                className="[&>td]:h-14 [&>td]:py-0"
                aria-hidden="true"
              >
                <TableCell>
                  <div className="space-y-1.5">
                    {skeletonCell('w-40')}
                    {skeletonCell('w-16')}
                  </div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell>{skeletonCell('w-12')}</TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-8')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-8')}</div>
                </TableCell>
                <TableCell>{skeletonCell('w-20')}</TableCell>
              </TableRow>
            ))
          : models.map((model) => {
              const origin = originVendorOf(model)
              const row = currentPrices(model)
              const detail = details?.get(model.model)
              const official = detail?.official_prices[model.tier]
              const officialPrice =
                model.buyer_price_source === 'official' || model.listing_count === 0
              const hasMarketSupply = model.listing_count > 0

              return (
                <TableRow
                  key={model.model}
                  interactive
                  className="relative [&>td]:h-14 [&>td]:py-0"
                >
                  <TableCell>
                    <Link
                      to="/models/$id"
                      params={{ id: model.model }}
                      className="block min-w-0 after:absolute after:inset-0 after:content-['']"
                    >
                      <span className="block truncate text-[15px] font-medium text-ink transition-colors hover:text-brand-ink">
                        {model.model}
                      </span>
                      <span className="flex min-w-0 items-center gap-1.5">
                        <span className="truncate text-[13px] text-ink-3">
                          {vendorLabel(origin, vendorMeta)}
                        </span>
                        {isOfficialVendor(origin, vendorMeta) ? (
                          <Tag tone="brand">{copy.pages.models.officialChannel}</Tag>
                        ) : null}
                      </span>
                    </Link>
                  </TableCell>
                  <TableCell numeric>
                    {official ? (
                      <span className="tabular-nums">
                        {formatYuan(official.input_micro_per_mtok)}
                      </span>
                    ) : detailsLoading ? (
                      <div className="flex justify-end">{skeletonCell('w-16')}</div>
                    ) : (
                      <Missing />
                    )}
                  </TableCell>
                  <TableCell numeric>
                    {official ? (
                      <span className="tabular-nums">
                        {formatYuan(official.output_micro_per_mtok)}
                      </span>
                    ) : detailsLoading ? (
                      <div className="flex justify-end">{skeletonCell('w-16')}</div>
                    ) : (
                      <Missing />
                    )}
                  </TableCell>
                  <TableCell>
                    {hasMarketSupply && detail ? (
                      <span className="font-medium tabular-nums text-brand-ink">
                        {formatMultiplier(detail.multiplier_min_milli)}x
                      </span>
                    ) : hasMarketSupply && detailsLoading ? (
                      skeletonCell('w-12')
                    ) : (
                      <Missing />
                    )}
                  </TableCell>
                  <TableCell numeric>
                    {row ? (
                      <div className="leading-tight">
                        <BuyerPair prices={row} />
                        {officialPrice ? (
                          <Tag tone="brand" className="mt-1">
                            {copy.pages.models.officialPrice}
                          </Tag>
                        ) : null}
                      </div>
                    ) : (
                      <Missing />
                    )}
                  </TableCell>
                  <TableCell numeric>
                    <span className="tabular-nums">{model.channel_count}</span>
                  </TableCell>
                  <TableCell numeric>
                    <span className="tabular-nums">{model.listing_count}</span>
                    <span className="ml-0.5 text-[13px] text-ink-3">
                      {copy.pages.models.sellerUnit}
                    </span>
                  </TableCell>
                  <TableCell>
                    {model.tier === 'flat' ? (
                      <span title={copy.pages.models.flatHint}>
                        <Tag>{copy.pages.models.flat}</Tag>
                      </span>
                    ) : (
                      <PeakBadge tier={model.tier} nextSwitchAt={model.next_switch} />
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
      </TableBody>
    </Table>
  )
}
