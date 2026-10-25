/**
 * Console API shapes for the buyer workspace.
 *
 * Money values are integer 微元 and must only be rendered through
 * lib/money.ts. Token counts are plain integers and are safe to display
 * directly. Timestamps arrive as RFC 3339 strings from Go's time.Time.
 */

export interface PageInfo<T> {
  page: number
  page_size: number
  total: number
  items: T[]
}

/** Upstream /api/token row (the key itself is masked by the backend). */
export interface TokenRecord {
  id: number
  user_id: number
  key: string
  status: number
  name: string
  created_time: number
  accessed_time: number
  expired_time: number
  remain_quota: number
  unlimited_quota: boolean
  model_limits_enabled: boolean
  model_limits: string
  allow_ips: string | null
  used_quota: number
  group: string
  cross_group_retry: boolean
  allow_user_quota_query?: boolean
  auto_groups?: string[]
}

export const TOKEN_STATUS_ENABLED = 1
export const TOKEN_STATUS_DISABLED = 2
export const TOKEN_STATUS_EXPIRED = 3
export const TOKEN_STATUS_EXHAUSTED = 4

export type Strategy = 'cheapest' | 'balanced' | 'stable'

export type ModelScope = 'all' | 'selected'

export const STRATEGIES: Strategy[] = ['cheapest', 'balanced', 'stable']

/** GET/PUT /api/marketplace/key/pref */
export interface KeyPref {
  token_id: number
  strategy: Strategy
  /** all = every marketplace model; selected = only the rows in models. */
  model_scope: ModelScope
  /** Multiplier cap in milli (850 = 0.85x); 0 means no cap. */
  price_cap_milli: number
  prefer_seller_ids: number[]
  block_seller_ids: number[]
  only_seller_id: number
  allow_fallback: boolean
  rpm: number
  /** Route only through official-kind channels. */
  official_only: boolean
  models: KeyModel[]
}

/** GET /api/marketplace/key/models */
export interface KeyModel {
  model: string
  /** Per-model multiplier cap in milli; 0 inherits the key cap. */
  price_cap_milli: number
  added_at: number
}

export interface ConsoleKey {
  token: TokenRecord
  pref: KeyPref | null
}

export interface KeyDetail {
  token: TokenRecord
  pref: KeyPref | null
  models: KeyModel[]
}

export type UsageTier = 'peak' | 'offpeak' | 'flat' | string

/** One row of GET /api/marketplace/records/usage. Amounts are 微元. */
export interface UsageRecord {
  request_id: string
  model: string
  seller_id: number
  tier: UsageTier
  tier_label: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cache_hit_tokens: number
  search_count: number
  fetch_count: number
  tool_debit_micro: number
  official_cost_micro: number
  total_debit_micro: number
  saved_micro: number
  usage_estimated: boolean
  status: string
  created_at: string
}

export interface UsageRecordDetail extends UsageRecord {
  retry_chain: string
}

export interface UsageDayStat {
  date: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  tokens: number
  total_debit_micro: number
  saved_micro: number
}

/** GET /api/marketplace/records/usage/stats */
export interface UsageStats {
  days: number
  start: string
  end: string
  items: UsageDayStat[]
}

/** GET /api/marketplace/wallet */
export interface WalletSummary {
  balance_micro: number | string
  month_spend_micro: number | string
  month_saved_micro: number | string
}

/** GET /api/marketplace/wallet/ledger */
export interface WalletLedgerEntry {
  request_id: string
  entry_type: string
  amount_micro: number | string
  status: string
  created_at: string
}

export interface KeyPrefInput {
  token_id: number
  strategy: Strategy
  model_scope: ModelScope
  price_cap_milli: number
  prefer_seller_ids: number[]
  block_seller_ids: number[]
  only_seller_id: number
  allow_fallback: boolean
  rpm: number
  official_only: boolean
}

/** One-shot POST /marketplace/key body (presets are expanded by the UI). */
export interface CreateKeyInput {
  /** Empty lets the backend assign "Key N". */
  name: string
  strategy: Strategy
  model_scope: ModelScope
  /** null = unlimited spend cap; otherwise integer micro-yuan. */
  spend_cap_micro: number | null
  price_cap_milli: number
  rpm: number
  prefer_seller_ids: number[]
  block_seller_ids: number[]
  only_seller_id: number
  allow_fallback: boolean
  official_only: boolean
  /** Unix seconds; -1 means never expires. */
  expired_time: number
  /** One IP or CIDR per line; empty means no restriction. */
  allow_ips: string
  models: { model: string; price_cap_milli: number }[]
}

export interface CreateKeyResult {
  id: number
  name: string
  /** Returned exactly once by the create call. */
  key: string
  model_scope: ModelScope
  models: KeyModel[]
  strategy: Strategy
}

/** PUT /api/marketplace/key/token */
export interface KeyTokenLimitsInput {
  token_id: number
  /** Unix seconds; -1 means never expires. */
  expired_time: number
  /** One IP or CIDR per line; empty means no restriction. */
  allow_ips: string
}

export interface KeyTokenLimits {
  expired_time: number
  allow_ips: string
}

/** GET /api/user/topup/info */
export interface TopupPayMethod {
  name: string
  type: string
  color?: string
  icon?: string
  min_topup?: string
}

export interface TopupInfo {
  enable_online_topup: boolean
  enable_redemption?: boolean
  pay_methods: TopupPayMethod[] | null
  min_topup: number
  amount_options: number[] | null
  discount: Record<string, number> | null
  topup_link: string
  payment_compliance_confirmed?: boolean
}

/** POST /api/user/pay — epay never wraps this answer in an envelope. */
export interface EpayPayment {
  message: string
  data: Record<string, string>
  url: string
}
