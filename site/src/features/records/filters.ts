/**
 * Shared filter types and date-range helpers for marketplace usage/share records.
 *
 * The backend reads start/end as unix seconds and the rest as plain strings;
 * buildRecordQuery drops empty/undefined values so absent filters are not sent.
 */

/** Non-pagination filters shared by the list and summary endpoints. */
export interface RecordFilters {
  /** Unix seconds; inclusive lower bound on created_at. */
  start?: number
  /** Unix seconds; upper bound on created_at. */
  end?: number
  model?: string
  /** Buyer: filter by the calling token id. */
  token_id?: number
  /** Seller: filter by listing (channel) id. */
  listing_id?: number
  /** Buyer: token-group snapshot; Seller: channel-group name or id. */
  group?: string
  stream?: boolean
  status?: string
}

/** Full list-query params: the shared filters plus pagination. */
export interface RecordFilterParams extends RecordFilters {
  page?: number
  page_size?: number
}

/** Summary endpoints take the same filters as the list, without pagination. */
export type SummaryFilterParams = RecordFilters

export type DatePreset = 'today' | '7d' | '30d'

/**
 * Convert a preset to [start, end] in unix seconds. `end` is always now; `today`
 * starts at local midnight, the rolling windows subtract 7/30 days from now.
 */
export function presetToRange(preset: DatePreset): [number, number] {
  const now = new Date()
  const end = Math.floor(now.getTime() / 1000)
  let start = end
  switch (preset) {
    case 'today': {
      const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate())
      start = Math.floor(midnight.getTime() / 1000)
      break
    }
    case '7d':
      start = end - 7 * 24 * 60 * 60
      break
    case '30d':
      start = end - 30 * 24 * 60 * 60
      break
  }
  return [start, end]
}

/**
 * Build the query-string object for a records request, omitting undefined,
 * empty-string, and non-positive numeric filters so they are not sent.
 */
export function buildRecordQuery(
  params: RecordFilterParams,
): Record<string, string | number | boolean> {
  const q: Record<string, string | number | boolean> = {}
  if (params.start !== undefined) q.start = params.start
  if (params.end !== undefined) q.end = params.end
  if (params.model) q.model = params.model
  if (params.token_id && params.token_id > 0) q.token_id = params.token_id
  if (params.listing_id && params.listing_id > 0) q.listing_id = params.listing_id
  if (params.group) q.group = params.group
  if (params.stream !== undefined) q.stream = params.stream
  if (params.status) q.status = params.status
  if (params.page && params.page > 0) q.page = params.page
  if (params.page_size && params.page_size > 0) q.page_size = params.page_size
  return q
}
