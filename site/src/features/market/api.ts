/**
 * Model square API shapes and query options.
 *
 * Money fields are integer micro-yuan per million tokens. They are typed as
 * number | string because int64 values may arrive as strings; format them
 * with lib/money.ts and never do arithmetic on them.
 */

import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

export type MarketTier = 'peak' | 'offpeak' | 'flat'

export interface SquareTierPrices {
  input_micro_per_mtok: number | string
  output_micro_per_mtok: number | string
  cache_read_micro_per_mtok: number | string
  cache_write_micro_per_mtok: number | string
}

export interface SquareModel {
  model: string
  vendor: string
  tier: MarketTier
  tier_label: string
  prices: Partial<Record<MarketTier, SquareTierPrices>>
  /** RFC 3339 timestamp, or null when the calendar has no switch ahead. */
  next_switch: string | null
  listing_count: number
  generated_at: string
}

export interface SquareModelDetail extends SquareModel {
  multiplier_min_milli: number
  multiplier_max_milli: number
  official_prices: Partial<
    Record<MarketTier, SquareTierPrices & { version: number }>
  >
}

export const squareModelsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'square', 'models'],
    queryFn: () => api.get<SquareModel[]>('/marketplace/square/models'),
    staleTime: 30_000,
  })

export const squareModelQuery = (model: string) =>
  queryOptions({
    queryKey: ['marketplace', 'square', 'model', model],
    queryFn: () =>
      api.get<SquareModelDetail>(
        `/marketplace/square/models/${encodeURIComponent(model)}`,
      ),
    staleTime: 30_000,
  })
