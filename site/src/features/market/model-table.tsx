import { Link } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  Multiplier,
  PeakBadge,
  Price,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
} from '@/ui'

import type { MarketTier, SquareModel, SquareModelDetail, SquareTierPrices } from './api'

export interface ModelTableProps {
  models: SquareModel[]
  /** Detail enrichment for 官方价 / 最低倍率 (the list endpoint lacks both). */
  details?: Map<string, SquareModelDetail>
  detailsLoading?: boolean
  loading?: boolean
  skeletonRows?: number
  minWidth?: number
}

function currentPrices(model: SquareModel): SquareTierPrices | undefined {
  return model.prices[model.tier] ?? model.prices.peak ?? model.prices.offpeak
}

function otherTierOf(tier: MarketTier): 'peak' | 'offpeak' | null {
  if (tier === 'peak') return 'offpeak'
  if (tier === 'offpeak') return 'peak'
  return null
}

function skeletonCell(width = 'w-16') {
  return <div className={`h-3.5 animate-pulse rounded-[2px] bg-line ${width}`} />
}

/** Two price rows (input on top, output below) for one tier pair. */
function PricePair({
  current,
  other,
  tier,
  otherTier,
}: {
  current: SquareTierPrices
  other?: SquareTierPrices
  tier: MarketTier
  otherTier: 'peak' | 'offpeak' | null
}) {
  return (
    <div className="leading-tight">
      <Price
        value={current.input_micro_per_mtok}
        tier={tier}
        other={
          other && otherTier
            ? { value: other.input_micro_per_mtok, tier: otherTier }
            : null
        }
        unit={null}
        size="sm"
      />
      <div className="mt-0.5 text-[11px] text-muted">
        {copy.pages.models.outputPrefix}{' '}
        <span className="font-numeric tabular-nums text-ink">
          {formatYuan(current.output_micro_per_mtok)}
        </span>
        {other && otherTier ? (
          <span className="font-numeric tabular-nums text-muted/80">
            {' '}
            / {formatYuan(other.output_micro_per_mtok)}
          </span>
        ) : null}
      </div>
    </div>
  )
}

/**
 * The dense model board, shared by the homepage top-8 and the full square.
 *
 * Columns 可用率 and 延迟 P50 from the design brief are deliberately absent:
 * the square API exposes neither field (see marketplace/api_square.go), and
 * this table does not invent numbers. Add them back when the API carries
 * availability/latency aggregates.
 */
export function ModelTable({
  models,
  details,
  detailsLoading = false,
  loading = false,
  skeletonRows = 6,
  minWidth = 900,
}: ModelTableProps) {
  const columns = copy.pages.models.columns

  return (
    <Table minWidth={minWidth}>
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{columns.model}</TableHeaderCell>
          <TableHeaderCell>{columns.vendor}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.official}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.multiplier}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.buyerPrice}</TableHeaderCell>
          <TableHeaderCell numeric>{columns.sellers}</TableHeaderCell>
          <TableHeaderCell>{columns.tier}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {loading
          ? Array.from({ length: skeletonRows }, (_, index) => (
              <TableRow key={`skeleton-${index}`} aria-hidden="true">
                <TableCell>{skeletonCell('w-40')}</TableCell>
                <TableCell>{skeletonCell('w-16')}</TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-20')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-16')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-20')}</div>
                </TableCell>
                <TableCell numeric>
                  <div className="flex justify-end">{skeletonCell('w-8')}</div>
                </TableCell>
                <TableCell>{skeletonCell('w-20')}</TableCell>
              </TableRow>
            ))
          : models.map((model) => {
              const row = currentPrices(model)
              const otherTier = otherTierOf(model.tier)
              const other = otherTier ? model.prices[otherTier] : undefined
              const detail = details?.get(model.model)
              const official = detail?.official_prices[model.tier]
              const officialOther = otherTier ? detail?.official_prices[otherTier] : undefined

              return (
                <TableRow key={`${model.model}:${model.vendor}`} interactive>
                  <TableCell>
                    <Link
                      to="/models/$id"
                      params={{ id: model.model }}
                      className="font-medium text-ink transition-colors hover:text-brand-dark"
                    >
                      {model.model}
                    </Link>
                  </TableCell>
                  <TableCell className="text-muted">{model.vendor}</TableCell>
                  <TableCell numeric>
                    {official ? (
                      <PricePair
                        current={official}
                        other={officialOther}
                        tier={model.tier}
                        otherTier={otherTier}
                      />
                    ) : detailsLoading ? (
                      <div className="flex justify-end">{skeletonCell('w-20')}</div>
                    ) : (
                      <span className="text-muted">{copy.common.none}</span>
                    )}
                  </TableCell>
                  <TableCell numeric>
                    {detail ? (
                      <Multiplier milli={detail.multiplier_min_milli} />
                    ) : detailsLoading ? (
                      <div className="flex justify-end">{skeletonCell('w-16')}</div>
                    ) : (
                      <span className="text-muted">{copy.common.none}</span>
                    )}
                  </TableCell>
                  <TableCell numeric>
                    {row ? (
                      <PricePair
                        current={row}
                        other={other}
                        tier={model.tier}
                        otherTier={otherTier}
                      />
                    ) : (
                      <span className="text-muted">{copy.common.none}</span>
                    )}
                  </TableCell>
                  <TableCell numeric>
                    {model.listing_count}
                    <span className="ml-0.5 text-[11px] text-muted">{copy.pages.models.sellerUnit}</span>
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
