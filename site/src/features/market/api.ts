/**
 * Model square API shapes and query options.
 *
 * Money fields are integer micro-yuan per million tokens. They are typed as
 * number | string because int64 values may arrive as strings; format them
 * with lib/money.ts and never do arithmetic on them.
 */

import { queryOptions, useQueries } from '@tanstack/react-query'

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

/**
 * The list endpoint only carries buyer prices and the listing count. The
 * official price book and the multiplier band live on the detail endpoint, so
 * tables that must show 官方价/最低倍率 enrich their rows with one detail query
 * per model. These queries share the cache with the detail page (same key), and
 * the catalog is small in v1. Fold this back into the list endpoint once the
 * backend exposes the two fields there.
 */
export function useSquareModelDetails(models: SquareModel[] | undefined) {
  const results = useQueries({
    queries: (models ?? []).map((model) => squareModelQuery(model.model)),
  })
  const details = new Map<string, SquareModelDetail>()
  let loading = false
  for (const result of results) {
    if (result.data) details.set(result.data.model, result.data)
    else if (result.isLoading) loading = true
  }
  return { details, loading }
}

/** Public homepage strip (cached server-side for 60s). */
export interface PublicStats {
  models_on_sale: number
  min_multiplier_milli: number
  today_requests: number
  platform_models: number
  generated_at: string
}

export const publicStatsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'stats', 'public'],
    queryFn: () => api.get<PublicStats>('/marketplace/stats/public'),
    staleTime: 60_000,
  })

/**
 * Upstream token list, used only to resolve which Key to add a model to.
 * `GET /api/token/` answers in the upstream PageInfo envelope.
 */
export interface BuyerToken {
  id: number
  name: string
  status: number
  key?: string
}

export interface BuyerTokenPage {
  page: number
  page_size: number
  total: number
  items: BuyerToken[]
}

export const buyerTokensQuery = () =>
  queryOptions({
    queryKey: ['tokens', 'list'],
    queryFn: () => api.get<BuyerTokenPage>('/token/', { query: { p: 1, page_size: 100 } }),
    staleTime: 30_000,
  })

/** POST /api/marketplace/key/models — 401/400/404 use the OpenAI error shape. */
export function addModelToKey(tokenId: number, model: string): Promise<{ models: unknown[] }> {
  return api.post<{ models: unknown[] }>('/marketplace/key/models', {
    token_id: tokenId,
    model,
  })
}
