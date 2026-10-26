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

import { api } from '@/lib/api'
import { queryClient } from '@/lib/query-client'

import type { SquareModelDetail } from '@/features/market/api'

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
  models: string[]
  multiplier_milli: number
  concurrency_limit: number
  rpm_limit: number
  daily_cap_micro: number | string
  hours: string
  status: string
  key_fingerprint8: string
  created_at: string
  /** Stored system pause reason (plan quota, patrol, migration) or "manual". */
  pause_reason?: string
  /** Time-boxed pause end (plan quota windows). */
  resume_at?: string
  /** Per-model outcome of the latest verify probe. */
  verification?: SellerVerification | null
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
}

// ---------------------------------------------------------------------------
// 收入统计
// ---------------------------------------------------------------------------

export interface SellerDailyIncome {
  date: string
  income_micro: number | string
}

export interface SellerStats {
  pending_micro: number | string
  available_micro: number | string
  /** Cumulative disbursed withdrawals (paid payouts only). */
  paid_micro: number | string
  month_income_micro: number | string
  platform_fee_total_micro: number | string
  daily: SellerDailyIncome[]
  as_of: string
}

// ---------------------------------------------------------------------------
// 分享记录
// ---------------------------------------------------------------------------

export interface ShareRecord {
  request_id: string
  model: string
  buyer_code: string
  tier: string
  tier_label: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  search_count: number
  fetch_count: number
  tool_debit_micro: number | string
  seller_credit_micro: number | string
  platform_fee_micro: number | string
  status: string
  created_at: string
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
    queryKey: ['marketplace', 'seller', 'listings', 'health'],
    queryFn: () => api.get<SellerListingHealth[]>(`${sellerBase}/listings/health`),
    staleTime: 30_000,
  })

export const sellerListingDetailQuery = (id: number) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'listing', id],
    queryFn: () => api.get<SellerListingDetail>(`${sellerBase}/listings/${id}`),
    staleTime: 15_000,
    refetchInterval: (query) =>
      query.state.data?.status === 'verifying' ? VERIFY_POLL_INTERVAL_MS : false,
  })

export const sellerStatsQuery = () =>
  queryOptions({
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

export const shareRecordsQuery = (page: number, pageSize = 20) =>
  queryOptions({
    queryKey: ['marketplace', 'seller', 'shares', page, pageSize],
    queryFn: () =>
      api.get<PageEnvelope<ShareRecord>>('/marketplace/records/shares', {
        query: { page, page_size: pageSize },
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
