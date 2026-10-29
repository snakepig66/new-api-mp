/**
 * 分享者控制台 API 形状与查询。
 *
 * 金额字段一律是后端整数微元（1 元 = 1_000_000 微元），类型写成
 * number | string，因为 int64 可能以字符串下发。展示统一走 lib/money.ts 的
 * formatYuan；前端不把它们换算成浮点数。
 *
 * 路由前缀是 /api/marketplace，统一由 lib/api.ts 补上 /api。
 */

import { queryOptions } from '@tanstack/react-query'

import { api, ApiError } from '@/lib/api'
import { queryClient } from '@/lib/query-client'

import type { SquareModelDetail } from '@/features/market/api'
import type { RecordFilterParams, SummaryFilterParams } from '@/features/records/filters'
import { buildRecordQuery } from '@/features/records/filters'

// ---------------------------------------------------------------------------
// 身份与开通
// ---------------------------------------------------------------------------

export type SellerNextStep = 'agreement' | 'real_name' | 'payout_account' | 'done'

export interface SellerProgress {
  agreement_signed: boolean
  real_name_verified: boolean
  payout_account_bound: boolean
  next_step: SellerNextStep
  agreement_version: string
  payout_channel?: 'wechat' | 'alipay' | string
  payout_name?: string
  payout_account_masked?: string
  payout_has_qr_code?: boolean
}

export interface SellerIdentity {
  exists: boolean
  status: string
  progress: SellerProgress
}

export interface MeResponse {
  user_id: number
  seller: SellerIdentity
}

export interface OnboardingInput {
  agreement_version?: 'v1'
  real_name?: string
  id_card_masked?: string
  alipay_account?: string
  payout_channel?: 'wechat' | 'alipay'
  payout_name?: string
  payout_account?: string
  payout_qr_code?: string
}

export interface OnboardingResponse {
  seller: SellerIdentity
}

// ---------------------------------------------------------------------------
// 挂单
// ---------------------------------------------------------------------------

/** One selected model's real callability result from the latest verify probe. */
export interface VerifyModelResult {
  model: string
  upstream_model: string
  ok: boolean
  prompt_cache_support: boolean
  latency_ms: number
  fail_reason?: string
}

/** Latest verify probe summary attached to a listing row. */
export interface SellerVerification {
  ok: boolean
  fail_reason: string
  prompt_cache_support: boolean
  latency_ms: number
  model_results: VerifyModelResult[]
  checked_at: string
}

export interface SellerListing {
  id: number
  vendor: string
  /** Seller-defined display name; absent on payloads from older backends. */
  label?: string
  models: string[]
  /** Aggregate model counts / effective multiplier band from the listing rows. */
  model_count?: number
  enabled_model_count?: number
  auto_disabled_model_count?: number
  multiplier_min_milli?: number
  multiplier_max_milli?: number
  multiplier_milli: number
  concurrency_limit: number
  rpm_limit: number
  daily_cap_micro: number | string
  hours: string
  status: string
  key_fingerprint8: string
  created_at: string
  /** P4 group id; 0 for a listing created before groups existed. */
  group_id?: number
  /** P4 key weight inside a weighted_round_robin group (0 = default 100). */
  weight?: number
  /** Stored system pause reason (plan quota, patrol, migration) or "manual". */
  pause_reason?: string
  /** Time-boxed pause end (plan quota windows). */
  resume_at?: string
  /** Per-model outcome of the latest verify probe. */
  verification?: SellerVerification | null
}

/** P4: one key (listing) inside a group. */
export interface SellerGroupSibling {
  id: number
  status: string
  models: string[]
  multiplier_milli: number
  weight: number
  key_fingerprint8: string
}

/** P4: one seller's multi-key channel group. */
export interface SellerGroup {
  id: number
  vendor_slug: string
  key_strategy: 'weighted_round_robin' | 'primary_backup'
  affinity_enabled: boolean
  channel_concurrency_limit: number
  channel_rpm_limit: number
  channel_daily_cap_micro: number | string
  channel_cumulative_cap_micro: number | string
  key_count: number
  listings: SellerGroupSibling[]
}

/** P4: one listing's per-model multiplier row. */
export interface SellerModelMultiplier {
  model: string
  multiplier_milli: number
  enabled: boolean
  /** Staged price increase (applies at pending_effective_at). */
  pending_multiplier_milli: number
  pending_effective_at?: string
  auto_disabled: boolean
  disabled_reason: string
}

export interface UpdateSellerGroupInput {
  key_strategy: 'weighted_round_robin' | 'primary_backup'
  affinity_enabled: boolean
  channel_concurrency_limit: number
  channel_rpm_limit: number
  channel_daily_cap_micro: number
  channel_cumulative_cap_micro: number
}

export interface SellerProbe {
  id: number
  type: string
  ok: boolean
  latency_ms: number
  detail: unknown
  created_at: string
}

export interface SellerListingDetail extends SellerListing {
  probes: SellerProbe[]
  /** P4 group block with sibling keys; absent when the listing is ungrouped. */
  group?: SellerGroup | null
  /** P4 per-model multiplier table (fallback is multiplier_milli). */
  model_multipliers?: SellerModelMultiplier[]
}

/** Newest patrol/verify probe summarised on a health card. */
export interface SellerHealthProbe {
  type: string
  ok: boolean
  latency_ms: number
  fail_reason: string
  model_results: VerifyModelResult[]
  created_at: string
}

/**
 * One row of GET /marketplace/seller/listings/health. Today is the
 * Asia/Shanghai calendar day; today_requests = 0 means the card has no sample.
 */
export interface SellerListingHealth {
  listing_id: number
  latest_probe: SellerHealthProbe | null
  today_requests: number
  /** 0..1000; 980 = 98%, 900 = 90%. */
  today_success_rate_milli: number
  today_income_micro: number | string
  /**
   * Rolling success-rate windows. A window with no samples is -1 (the console
   * renders "—"), while 0 is a real all-failed window. Optional so older
   * payloads/fixtures still type-check.
   */
  requests_1h?: number
  success_rate_1h_milli?: number
  requests_24h?: number
  success_rate_24h_milli?: number
  requests_7d?: number
  success_rate_7d_milli?: number
}

/** One price book's per-million-token prices, in 微元 (integer). */
export interface SellerModelPrices {
  input_micro_per_mtok: number
  output_micro_per_mtok: number
  cache_read_micro_per_mtok: number
  cache_write_micro_per_mtok: number
}

/**
 * One row of GET /marketplace/seller/listings/:id/models — the console's
 * 模型与价格 table. Buyer prices come from the same Quote path the debit uses.
 * The price maps only carry the single `flat` key now; pricing no longer has
 * peak/off-peak tiers.
 */
export interface SellerModelRow {
  model: string
  upstream_model: string
  enabled: boolean
  /** '' verified/live, 'pending' verifying, 'failed' verification failed. */
  verify_state?: '' | 'pending' | 'failed'
  auto_disabled: boolean
  disabled_reason: string
  disabled_detail: string
  disabled_upstream_status: number
  disabled_upstream_message: string
  disabled_at?: string
  multiplier_milli: number
  effective_multiplier_milli: number
  pending_multiplier_milli: number
  pending_effective_at?: string
  coefficient_milli: number
  origin_vendor: string
  multiplier_min_milli: number
  multiplier_max_milli: number
  official_prices: { flat?: SellerModelPrices }
  buyer_prices: { flat?: SellerModelPrices }
}

export interface SellerListingModelsResponse {
  items: SellerModelRow[]
  platform_fee_percent: number
  multiplier_min_milli: number
  multiplier_max_milli: number
}

/** Per-channel-kind multiplier floor/ceiling from GET /seller/multiplier-bounds. */
export interface MultiplierKindBounds {
  min_milli: number
  max_milli: number
}

export interface MultiplierBounds {
  absolute_min_milli: number
  absolute_max_milli: number
  kinds: Record<SellerChannelKind, MultiplierKindBounds>
}

/** One row returned by the single/batch model-state endpoints. */
export interface SellerModelChangeRow {
  model: string
  enabled: boolean
  multiplier_milli: number
  pending_multiplier_milli: number
  pending_effective_at?: string
  auto_disabled: boolean
  disabled_reason: string
}

/** PUT /seller/listings/:id/models returns {items: [...]}. */
export interface SellerModelChangeResult {
  items: SellerModelChangeRow[]
}

/** POST /seller/listings/:id/models/restore result. */
export interface SellerModelsRestoreResult {
  restored: number
  /** Models the probe still rejects (same row shape as a batch rejection). */
  still_failing: ModelChangeError[]
  checked_at: string
}

/** One rejected model change from an all-or-nothing batch (400/409). */
export interface ModelChangeError {
  listing_id: number
  model: string
  code: string
  message: string
  earliest_change_at?: string
}

// ---------------------------------------------------------------------------
// 上游请求头（自定义渠道）
// ---------------------------------------------------------------------------

/** Only the masked spelling of a header value is ever sent to the console. */
export interface SellerHeaderEntry {
  name: string
  value_masked: string
}

/** A header policy candidate that is still being verified. */
export interface SellerPendingHeaders {
  version: number
  submitted_at?: string
  headers: SellerHeaderEntry[]
}

/** GET /seller/listings/:id/headers. */
export interface SellerHeadersView {
  custom_channel: boolean
  headers: SellerHeaderEntry[]
  pending?: SellerPendingHeaders
  can_edit: boolean
}

/** PUT /seller/listings/:id/headers with a non-empty object. */
export interface SellerHeadersSubmitResult {
  pending: boolean
  unchanged: boolean
  version: number
  headers: SellerHeaderEntry[]
}

export interface CreateListingInput {
  vendor_id: string
  api_key: string
  models: string[]
  multiplier_milli: number
  concurrency: number
  rpm: number
  daily_cap_micro: number
  hours: string
  /** Required (true) for plan channels; the seller confirms the plan terms. */
  plan_terms_accepted?: boolean
  /** Seller-defined upstream URL; opens a private third-party channel. */
  custom_base_url?: string
  /** P4 group settings, used when this create request creates the group. */
  key_strategy?: 'weighted_round_robin' | 'primary_backup'
  affinity_enabled?: boolean
  /** P4 key weight inside a weighted_round_robin group. */
  weight?: number
  /** Per-model upstream name and multiplier (overrides multiplier_milli). */
  model_configs?: SellerModelConfig[]
}

export interface CreateListingResponse {
  id: number
  status: string
}

export interface ProbeInput {
  vendor_id: string
  api_key: string
  models: string[]
  /** Seller-defined upstream URL; probes it instead of vendor_id. */
  custom_base_url?: string
}

export type TrustState = 'trusted' | 'watch' | 'blocked' | 'unlisted'

/** Trust-pool recognition attached to a probe result (P2). */
export interface TrustPoolInfo {
  matched: boolean
  trust: TrustState
  kind: 'official' | 'third_party' | 'plan' | ''
  vendor_name: string
  endpoints: string[]
  /** e.g. 「智谱 · 官方 · OpenAI + Anthropic · 免审核」. */
  badge_text: string
}

export interface ProbeResult {
  ok: boolean
  models: string[]
  /** Channel-qualified standard models this key can call (pool contract). */
  verified_models?: string[]
  /**
   * Price coefficient per standard model (1000 = official price). The buyer
   * price is official × coefficient × multiplier; the backend Quote applies
   * the same two factors.
   */
  coefficients?: Record<string, number>
  missing_models: string[]
  latency_ms: number
  prompt_cache: boolean
  balance_micro: number | string | null
  key_valid: boolean
  fail_reason: string
  /** Certification-pool recognition; present on new backends. */
  trust_pool?: TrustPoolInfo | null
  /** Every model name the upstream key exposes, with its suggested pool match. */
  upstream_models?: UpstreamModelOption[]
  /** The platform model pool (priced standard models). */
  pool_models?: PoolModelOption[]
}

/** Official flat price of a pool model, micro-yuan per million tokens. */
export interface PoolModelPrice {
  input_micro_per_mtok: number
  output_micro_per_mtok: number
  cache_read_micro_per_mtok: number
  cache_write_micro_per_mtok: number
}

/** One upstream `/models` entry and the pool model it maps to. */
export interface UpstreamModelOption {
  upstream: string
  suggested_model: string | null
  priced: boolean
}

/** One priced model in the platform pool. */
export interface PoolModelOption {
  model: string
  vendor: string
  display_name?: string
  context_length?: number
  price: PoolModelPrice
}

/** A seller-chosen model: standard id, upstream name, multiplier. */
export interface SellerModelConfig {
  model: string
  upstream_model: string
  multiplier_milli: number
}

export interface ListingUpstreamModelsResult {
  key_valid: boolean
  fail_reason: string
  upstream_models: UpstreamModelOption[]
  pool_models: PoolModelOption[]
  coefficients?: Record<string, number>
}

// ---------------------------------------------------------------------------
// 收入统计
// ---------------------------------------------------------------------------

export interface SellerDailyIncome {
  date: string
  income_micro: number | string
}

// ---------------------------------------------------------------------------
// 概览（GET /seller/overview，也合并进 GET /seller/stats）
// ---------------------------------------------------------------------------

export interface SellerOverviewCards {
  calls: number
  tokens: number
  income_micro: number | string
  pending_micro: number | string
}

/** Chart window: today plus the previous 29 days, inclusive. */
export interface SellerOverviewRange {
  days: number
  start: string
  end: string
}

export interface SellerOverviewModelRow {
  model: string
  calls: number
  tokens: number
  income_micro: number | string
}

export interface SellerOverviewDay {
  date: string
  tokens: number
  income_micro: number | string
}

/** One model's dense daily series; the remainder of the top 8 is "other". */
export interface SellerOverviewModelSeries {
  model: string
  days: SellerOverviewDay[]
}

export interface SellerOverviewChannel {
  listing_id: number
  label: string
  calls: number
  tokens: number
  income_micro: number | string
}

export interface SellerOverview {
  cards: SellerOverviewCards
  range: SellerOverviewRange
  by_model: SellerOverviewModelRow[]
  daily_by_model: SellerOverviewModelSeries[]
  by_channel: SellerOverviewChannel[]
  as_of: string
}

export interface SellerStats {
  pending_micro: number | string
  available_micro: number | string
  /** Cumulative disbursed withdrawals (paid payouts only). */
  paid_micro: number | string
  month_income_micro: number | string
  platform_fee_total_micro: number | string
  /** Month-to-date calls/tokens plus the 30-day breakdowns (same as overview). */
  month_calls: number
  month_tokens: number
  range: SellerOverviewRange
  by_model: SellerOverviewModelRow[]
  daily_by_model: SellerOverviewModelSeries[]
  by_channel: SellerOverviewChannel[]
  daily: SellerDailyIncome[]
  as_of: string
}

// ---------------------------------------------------------------------------
// 分享记录
// ---------------------------------------------------------------------------

export interface ShareRecord {
  request_id: string
  model: string
  /** 匿名代号 (§3.1), 8 hex digits — never the real buyer user id. */
  buyer_code: string
  tier: string
  tier_label: string
  /** 渠道分组 (mp_listing_groups 的 vendor slug). */
  channel_group: string
  /** 渠道 id. */
  listing_id: number
  /** key 指纹后 4 位; empty when unavailable. */
  key_fingerprint4: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  search_count: number
  fetch_count: number
  tool_debit_micro: number | string
  /** 卖家收入. */
  seller_credit_micro: number | string
  /** 买家实付. */
  buyer_paid_micro: number | string
  /** 平台费. */
  platform_fee_micro: number | string
  /** 首字耗时 ms; 0 = 未记录. */
  first_token_ms: number
  /** 总耗时 ms. */
  latency_ms: number
  is_stream: boolean
  /** 买方协议 (openai-chat / anthropic-messages / …). */
  endpoint: string
  /** 协议转换. */
  converted: boolean
  /** 上游模型 id; empty or absent when the response carried no model. */
  upstream_model?: string
  usage_estimated: boolean
  status: string
  created_at: string
}

/** One sold request plus the retry chain (seller detail view). */
export interface ShareRecordDetail extends ShareRecord {
  retry_chain: string
}

/** GET /api/marketplace/records/shares/summary — seller aggregate stats (卖方汇总). */
export interface ShareSummary {
  request_count: number
  success_count: number
  /** -1 = no sample. */
  success_rate_milli: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  /** 收益合计. */
  seller_credit_micro: number | string
  /** 首字平均 ms; 0 = 无数据. */
  avg_first_token_ms: number
  /** 总耗时平均 ms. */
  avg_latency_ms: number
}

export interface PageEnvelope<T> {
  page: number
  page_size: number
  total: number
  items: T[]
}

// ---------------------------------------------------------------------------
// 提现
// ---------------------------------------------------------------------------

export interface Payout {
  id: number
  seller_id: number
  amount_micro: number | string
  status: string
  idempotency_key: string
  reviewer_id: number
  reviewed_at: string | null
  created_at: string
  updated_at: string
}

/**
 * Payout rows are marshalled straight from the Go struct, which has no JSON
 * tags (unlike listings/records), so the wire keys are Go field names
 * ("AmountMicro", "CreatedAt", …). Normalize both spellings once here.
 */
export function normalizePayout(raw: unknown): Payout {
  const row = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  const value = (snake: string, go: string): unknown => row[snake] ?? row[go]
  return {
    id: Number(value('id', 'ID') ?? 0),
    seller_id: Number(value('seller_id', 'SellerID') ?? 0),
    amount_micro: (value('amount_micro', 'AmountMicro') ?? 0) as number | string,
    status: String(value('status', 'Status') ?? ''),
    idempotency_key: String(value('idempotency_key', 'IdempotencyKey') ?? ''),
    reviewer_id: Number(value('reviewer_id', 'ReviewerID') ?? 0),
    reviewed_at: (value('reviewed_at', 'ReviewedAt') ?? null) as string | null,
    created_at: String(value('created_at', 'CreatedAt') ?? ''),
    updated_at: String(value('updated_at', 'UpdatedAt') ?? ''),
  }
}

export interface SellerBalance {
  available_micro: number | string
  pending_micro: number | string
  /** Cumulative disbursed withdrawals (paid payouts only). */
  paid_micro: number | string
  payouts: Payout[]
}

export interface PayoutInput {
  amount_micro: number
  idempotency_key: string
}

// ---------------------------------------------------------------------------
// 官方渠道白名单
// ---------------------------------------------------------------------------

export type SellerChannelKind = 'official' | 'plan' | 'third_party'

export interface SellerVendor {
  id: string
  display_name: string
  tier: string
  /** Pool kind; defaults to tier for older payloads. */
  kind?: SellerChannelKind
  /** Root-relative brand mark; empty for vendors without a shipped SVG. */
  logo_url?: string
  protocols: { openai: string; anthropic: string }
  models: string[]
  passthrough_body: boolean
  /** Plan quota window: {mode, period_seconds, reset_timezone, weekday, hour, minute}. */
  quota_window?: SellerQuotaWindow
  /** Operator-configured plan terms link. */
  terms_url?: string
}

/** Plan quota-window shape exposed by GET /marketplace/vendors. */
export interface SellerQuotaWindow {
  mode?: 'rolling' | 'weekly' | string
  period_seconds?: number
  reset_timezone?: string
  weekday?: number
  hour?: number
  minute?: number
}

/** Normalize the channel kind, falling back to the legacy `tier` field. */
export function sellerChannelKind(vendor: SellerVendor): SellerChannelKind {
  if (vendor.kind === 'official' || vendor.kind === 'plan' || vendor.kind === 'third_party') {
    return vendor.kind
  }
  if (vendor.tier === 'official' || vendor.tier === 'plan' || vendor.tier === 'third_party') {
    return vendor.tier
  }
  return 'third_party'
}

export const vendorsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'vendors'],
    queryFn: () => api.get<SellerVendor[]>('/marketplace/vendors'),
    staleTime: 60_000,
  })

/** Seller-scoped catalogue: public channels plus the seller's own custom upstreams. */
export const sellerVendorsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'vendors'],
    queryFn: () => api.get<SellerVendor[]>('/marketplace/seller/vendors'),
    staleTime: 60_000,
  })

/** One trusted upstream address the wizard can prefill (P2). */
export interface SellerCommonAddress {
  host: string
  vendor_name: string
  kind: 'official' | 'third_party' | 'plan' | ''
  endpoints: string[]
}

export const sellerCommonAddressesQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'common-addresses'],
    queryFn: () => api.get<SellerCommonAddress[]>('/marketplace/seller/common-addresses'),
    staleTime: 5 * 60_000,
  })

/**
 * Display name for a vendor slug. Prefers the vendors list already in the
 * query cache; callers that already fetched it can pass it directly. Falls
 * back to the slug when the vendor is unknown or the list has not loaded.
 */
export function vendorName(id: string, vendors?: readonly SellerVendor[]): string {
  const list =
    vendors ?? queryClient.getQueryData<SellerVendor[]>(vendorsQuery().queryKey)
  return list?.find((vendor) => vendor.id === id)?.display_name ?? id
}

// ---------------------------------------------------------------------------
// Query options
// ---------------------------------------------------------------------------

const sellerBase = '/marketplace/seller'

/** Auto-refresh cadence while any listing is verifying (verification <= 60s). */
export const VERIFY_POLL_INTERVAL_MS = 3000

/**
 * Poll only while a verification is running. The backend caps verification at
 * 60s, so this stops on its own once every row left `verifying`.
 */
export function verifyingRefetchInterval(
  rows: readonly { status: string }[] | undefined,
): number | false {
  return rows?.some((row) => row.status === 'verifying')
    ? VERIFY_POLL_INTERVAL_MS
    : false
}

export interface MarketplaceStatus {
  enabled: boolean
  version: string
  /** Backend PlatformFeePercent: the only source for the 7% split. */
  platform_fee_percent: number
}

export const marketplaceStatusQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'status'],
    queryFn: () => api.get<MarketplaceStatus>('/marketplace/status'),
    staleTime: 5 * 60_000,
  })

export const meQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'me'],
    queryFn: () => api.get<MeResponse>('/marketplace/me'),
    staleTime: 15_000,
  })

export const sellerListingsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'listings'],
    queryFn: () => api.get<SellerListing[]>(`${sellerBase}/listings`),
    staleTime: 15_000,
    refetchInterval: (query) => verifyingRefetchInterval(query.state.data),
  })

export const sellerListingsHealthQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'listings', 'health'],    queryFn: () => api.get<SellerListingHealth[]>(`${sellerBase}/listings/health`),
    staleTime: 30_000,
    // The card is a live reading (today's rate/income + last probe); without a
    // poll an open tab keeps showing the values it loaded hours ago.
    refetchInterval: 60_000,
  })

export const sellerListingDetailQuery = (id: number) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'listing', id],
    queryFn: () => api.get<SellerListingDetail>(`${sellerBase}/listings/${id}`),
    staleTime: 15_000,
    refetchInterval: (query) =>
      query.state.data?.status === 'verifying' ? VERIFY_POLL_INTERVAL_MS : false,
  })

/** Every model of one listing with enabled state, official and buyer prices. */
export const sellerListingModelsQuery = (id: number) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'listing', id, 'models'],
    queryFn: () => api.get<SellerListingModelsResponse>(`${sellerBase}/listings/${id}/models`),
    staleTime: 15_000,
  })

/** Per-channel-kind multiplier floors/ceilings used by the console inputs. */
export const sellerMultiplierBoundsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'multiplier-bounds'],
    queryFn: () => api.get<MultiplierBounds>(`${sellerBase}/multiplier-bounds`),
    staleTime: 5 * 60_000,
  })

/** Masked upstream header policy (plus any pending candidate) of one listing. */
export const sellerListingHeadersQuery = (id: number) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'listing', id, 'headers'],
    queryFn: () => api.get<SellerHeadersView>(`${sellerBase}/listings/${id}/headers`),
    staleTime: 15_000,
  })

/** Month-to-date cards + 30-day per-model/per-channel charts. */
export const sellerOverviewQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'overview'],
    queryFn: () => api.get<SellerOverview>(`${sellerBase}/overview`),
    staleTime: 30_000,
  })

/** P4 groups of the current seller (one per vendor), with their sibling keys. */
export const sellerGroupsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'groups'],
    queryFn: () => api.get<SellerGroup[]>(`${sellerBase}/groups`),
    staleTime: 15_000,
  })

export const sellerStatsQuery = () =>  queryOptions({
    queryKey: ['marketplace', 'seller', 'stats'],
    queryFn: () => api.get<SellerStats>(`${sellerBase}/stats`),
    staleTime: 30_000,
  })

export const sellerBalanceQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'balance'],
    queryFn: async (): Promise<SellerBalance> => {
      const raw = await api.get<{
        available_micro: number | string
        pending_micro: number | string
        paid_micro?: number | string
        payouts: unknown[]
      }>(`${sellerBase}/balance`)
      return {
        available_micro: raw.available_micro,
        pending_micro: raw.pending_micro,
        paid_micro: raw.paid_micro ?? 0,
        payouts: (raw.payouts ?? []).map(normalizePayout),
      }
    },
    staleTime: 15_000,
  })

export const shareRecordsQuery = (
  page: number,
  pageSize = 20,
  filters: Omit<RecordFilterParams, 'page' | 'page_size'> = {},
) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'shares', page, pageSize, filters],
    queryFn: () =>
      api.get<PageEnvelope<ShareRecord>>('/marketplace/records/shares', {
        query: buildRecordQuery({ page, page_size: pageSize, ...filters }),
      }),
    staleTime: 30_000,
  })

/** GET /marketplace/records/shares/:request_id — one sold request with the retry chain. */
export const shareRecordQuery = (requestId: string) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'share', requestId],
    queryFn: () =>
      api.get<ShareRecordDetail>(
        `/marketplace/records/shares/${encodeURIComponent(requestId)}`,
      ),
    staleTime: 60_000,
  })

/** GET /marketplace/records/shares/summary — seller totals under the same filters. */
export const shareSummaryQuery = (filters: SummaryFilterParams = {}) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'shares-summary', filters],
    queryFn: () =>
      api.get<ShareSummary>('/marketplace/records/shares/summary', {
        query: buildRecordQuery(filters),
      }),
    staleTime: 30_000,
  })

export const sellerPayoutsQuery = (page: number, pageSize = 10) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'payouts', page, pageSize],
    queryFn: async (): Promise<PageEnvelope<Payout>> => {
      const raw = await api.get<PageEnvelope<unknown>>(`${sellerBase}/payouts`, {
        query: { page, page_size: pageSize },
      })
      return { ...raw, items: (raw.items ?? []).map(normalizePayout) }
    },
    staleTime: 15_000,
  })

// ---------------------------------------------------------------------------
// Mutations (plain functions; pages wrap them in useMutation)
// ---------------------------------------------------------------------------

/** PUT /seller/listings/:id body; omitted fields are left unchanged. */
export interface UpdateListingSettingsInput {
  label?: string
  concurrency_limit?: number
  rpm_limit?: number
  daily_cap_micro?: number
  hours?: string
}

/** POST /seller/listings/batch-models body (one change across listings). */
export interface BatchListingModelsInput {
  listing_ids: number[]
  models?: string[]
  multiplier_milli?: number
  enabled?: boolean
}

/** PUT /seller/listings/:id/models body (one change across models). */
export interface SetListingModelsStateInput {
  models?: string[]
  multiplier_milli?: number
  enabled?: boolean
}

/** PUT /seller/listings/:id/models/:model body. */
export interface SetListingModelStateInput {
  multiplier_milli?: number
  enabled?: boolean
}

export function postOnboarding(input: OnboardingInput): Promise<OnboardingResponse> {
  return api.post<OnboardingResponse>(`${sellerBase}/onboarding`, input)
}

export function createListing(input: CreateListingInput): Promise<CreateListingResponse> {
  return api.post<CreateListingResponse>(`${sellerBase}/listings`, input)
}

export function probeListing(input: ProbeInput): Promise<ProbeResult> {
  return api.post<ProbeResult>(`${sellerBase}/listings/probe`, input)
}

export function updateListingMultiplier(
  id: number,
  multiplierMilli: number,
): Promise<{ id: number; multiplier_milli: number }> {
  return api.post(`${sellerBase}/listings/${id}/multiplier`, {
    multiplier_milli: multiplierMilli,
  })
}

export function replaceListingKey(id: number, apiKey: string): Promise<{ id: number; status: string }> {
  return api.post(`${sellerBase}/listings/${id}/key`, { api_key: apiKey })
}

/** P4: set one model's multiplier on a listing (applies immediately). */
export function setListingModelMultiplier(
  id: number,
  model: string,
  multiplierMilli: number,
): Promise<{ listing_id: number; model: string; multiplier_milli: number }> {
  return api.put(
    `${sellerBase}/listings/${id}/models/${encodeURIComponent(model)}/multiplier`,
    { multiplier_milli: multiplierMilli },
  )
}

/** P4: update a group's routing strategy, affinity switch and channel limits. */
export function updateListingGroup(
  id: number,
  input: UpdateSellerGroupInput,
): Promise<SellerGroup> {
  return api.put<SellerGroup>(`${sellerBase}/groups/${id}`, input)
}

/** Re-run verification for a draft (failed) listing. */
export function verifyListing(id: number): Promise<{ id: number; status: string }> {
  return api.post(`${sellerBase}/listings/${id}/verify`)
}

export function pauseListing(id: number): Promise<{ id: number; status: string }> {
  return api.post(`${sellerBase}/listings/${id}/pause`)
}

export function resumeListing(id: number): Promise<{ id: number; status: string }> {
  return api.post(`${sellerBase}/listings/${id}/resume`)
}

/** Soft-delete a listing; idempotent, so a repeated call returns the same status. */
export function deleteListing(id: number): Promise<{ id: number; status: string }> {
  return api.delete(`${sellerBase}/listings/${id}`)
}

/** Update display name, limits and business hours; omitted fields stay unchanged. */
export function updateListingSettings(
  id: number,
  input: UpdateListingSettingsInput,
): Promise<SellerListing> {
  return api.put<SellerListing>(`${sellerBase}/listings/${id}`, input)
}

/** Apply one change across several listings in a single transaction. */
export function batchListingModels(
  input: BatchListingModelsInput,
): Promise<{ ok: boolean }> {
  return api.post(`${sellerBase}/listings/batch-models`, input)
}

/** Enable/disable or reprice several models of one listing (all-or-nothing). */
export function setListingModelsState(
  id: number,
  input: SetListingModelsStateInput,
): Promise<SellerModelChangeResult> {
  return api.put<SellerModelChangeResult>(`${sellerBase}/listings/${id}/models`, input)
}

/** Enable/disable or reprice one model; a decrease applies now, an increase is staged. */
export function setListingModelState(
  id: number,
  model: string,
  input: SetListingModelStateInput,
): Promise<SellerModelChangeRow> {
  return api.put<SellerModelChangeRow>(
    `${sellerBase}/listings/${id}/models/${encodeURIComponent(model)}`,
    input,
  )
}

/** List the upstream models the listing's key exposes plus the model pool. */
export function fetchListingUpstreamModels(id: number): Promise<ListingUpstreamModelsResult> {
  return api.post<ListingUpstreamModelsResult>(`${sellerBase}/listings/${id}/models/upstream`)
}

/** Add models to a listing; each is verified by a real call before it gets traffic. */
export function addListingModels(
  id: number,
  models: SellerModelConfig[],
): Promise<{ added: string[]; verifying: string[] }> {
  return api.post(`${sellerBase}/listings/${id}/models`, { models })
}

/** Remove a model; new requests stop at once, in-flight requests finish. */
export function removeListingModel(id: number, model: string): Promise<{ removed: string }> {
  return api.delete(`${sellerBase}/listings/${id}/models/${encodeURIComponent(model)}`)
}

/** Probe every auto-disabled model and re-enable the ones that pass. */
export function restoreListingModels(id: number): Promise<SellerModelsRestoreResult> {
  return api.post<SellerModelsRestoreResult>(`${sellerBase}/listings/${id}/models/restore`)
}

/**
 * Replace the upstream header policy. A non-empty object is staged and
 * verified before it goes live; an empty object clears the live policy, in
 * which case the backend answers `{cleared: true, headers}` instead of the
 * submit result.
 */
export function submitListingHeaders(
  id: number,
  headers: Record<string, string>,
): Promise<SellerHeadersSubmitResult> {
  return api.put<SellerHeadersSubmitResult>(`${sellerBase}/listings/${id}/headers`, { headers })
}

/**
 * Extract the per-row reasons from an all-or-nothing model-change rejection
 * (400/409 with `{code: 'MODEL_CHANGE_REJECTED', items: [...]}`). Returns an
 * empty array for any other error, so callers can branch on the length without
 * first checking the code.
 */
export function modelChangeErrors(err: unknown): ModelChangeError[] {
  if (!(err instanceof ApiError)) return []
  const details = err.details
  const payload =
    details && details.code === 'MODEL_CHANGE_REJECTED'
      ? details
      : details?.data && typeof details.data === 'object'
        ? (details.data as Record<string, unknown>)
        : undefined
  if (err.code !== 'MODEL_CHANGE_REJECTED' && payload?.code !== 'MODEL_CHANGE_REJECTED') {
    return []
  }
  const items = payload?.items
  if (!Array.isArray(items)) return []
  return items.filter(
    (item): item is ModelChangeError => Boolean(item) && typeof item === 'object',
  )
}

export function requestPayout(input: PayoutInput): Promise<{
  id: number
  status: string
  amount_micro: number | string
}> {
  return api.post(`${sellerBase}/payouts`, input)
}

/** Lowest multiplier of live/canary supply for one model, from the square. */
export function squareModelForSeller(model: string) {
  return queryOptions({
    queryKey: ['marketplace', 'square', 'model', model],
    queryFn: () =>
      api.get<SquareModelDetail>(`/marketplace/square/models/${encodeURIComponent(model)}`),
    staleTime: 30_000,
    retry: 0,
  })
}
