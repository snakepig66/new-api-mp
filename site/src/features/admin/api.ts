/**
 * 运营后台 API 形状与查询。
 *
 * 后端 admin 接口挂在 /api/marketplace/admin，要求超级管理员（RootAuth）。
 * 金额一律是整数微元，展示走 lib/money.ts 的 formatYuan，前端不做金额运算。
 *
 * 注意：价目、按次价、节假日、提现的 Go 结构体没有 JSON tag，响应键名是
 * Go 字段名（Model / AmountMicro / Date …）。这里统一做一次大小写归一化，
 * 页面只消费小写字段。
 */

import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

const adminBase = '/marketplace/admin'

type UnknownRecord = Record<string, unknown>

function record(raw: unknown): UnknownRecord {
  return raw && typeof raw === 'object' ? (raw as UnknownRecord) : {}
}

function pick(row: UnknownRecord, ...keys: string[]): unknown {
  for (const key of keys) {
    const value = row[key]
    if (value !== undefined && value !== null) return value
  }
  return undefined
}

function text(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

function int(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function micro(value: unknown): number | string {
  if (typeof value === 'number' || typeof value === 'string') return value
  return 0
}

// ---------------------------------------------------------------------------
// 价目表
// ---------------------------------------------------------------------------

export type AdminTier = 'peak' | 'offpeak' | 'flat'

export interface AdminPrice {
  id: number
  model: string
  vendor: string
  tier: string
  version: number
  input_micro_per_mtok: number | string
  output_micro_per_mtok: number | string
  cache_read_micro_per_mtok: number | string
  cache_write_micro_per_mtok: number | string
  effective_at: string
  created_at: string
}

export function normalizeAdminPrice(raw: unknown): AdminPrice {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    model: text(pick(row, 'model', 'Model')),
    vendor: text(pick(row, 'vendor', 'Vendor')),
    tier: text(pick(row, 'tier', 'Tier')),
    version: int(pick(row, 'version', 'Version')),
    input_micro_per_mtok: micro(pick(row, 'input_micro_per_mtok', 'InputMicroPerMTok')),
    output_micro_per_mtok: micro(pick(row, 'output_micro_per_mtok', 'OutputMicroPerMTok')),
    cache_read_micro_per_mtok: micro(
      pick(row, 'cache_read_micro_per_mtok', 'CacheReadMicroPerMTok'),
    ),
    cache_write_micro_per_mtok: micro(
      pick(row, 'cache_write_micro_per_mtok', 'CacheWriteMicroPerMTok'),
    ),
    effective_at: text(pick(row, 'effective_at', 'EffectiveAt')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export interface CreatePriceInput {
  model: string
  vendor: string
  tier: AdminTier
  input_micro_per_mtok: number
  output_micro_per_mtok: number
  cache_read_micro_per_mtok: number
  cache_write_micro_per_mtok: number
  version?: number
  effective_at?: string
}

export const adminPricesQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'prices'],
    queryFn: async (): Promise<AdminPrice[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/prices`)
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminPrice)
    },
    staleTime: 15_000,
  })

export function createAdminPrice(input: CreatePriceInput): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/prices`, input)
}

// ---------------------------------------------------------------------------
// 按次价目
// ---------------------------------------------------------------------------

export type PerCallItem = 'search' | 'fetch'

export interface AdminPerCallPrice {
  id: number
  vendor: string
  item: string
  version: number
  unit_price_micro: number | string
  effective_at: string
  created_at: string
}

export function normalizeAdminPerCallPrice(raw: unknown): AdminPerCallPrice {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    vendor: text(pick(row, 'vendor', 'Vendor')),
    item: text(pick(row, 'item', 'Item')),
    version: int(pick(row, 'version', 'Version')),
    unit_price_micro: micro(pick(row, 'unit_price_micro', 'UnitPriceMicro')),
    effective_at: text(pick(row, 'effective_at', 'EffectiveAt')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export interface CreatePerCallPriceInput {
  vendor: string
  item: PerCallItem
  unit_price_micro: number
  version?: number
  effective_at?: string
}

export const adminPerCallPricesQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'per-call-prices'],
    queryFn: async (): Promise<AdminPerCallPrice[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/per-call-prices`)
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminPerCallPrice)
    },
    staleTime: 15_000,
  })

export function createAdminPerCallPrice(input: CreatePerCallPriceInput): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/per-call-prices`, input)
}

// ---------------------------------------------------------------------------
// 节假日
// ---------------------------------------------------------------------------

export type HolidayKind = 'holiday' | 'workday'

export interface AdminHoliday {
  date: string
  kind: string
}

export interface AdminHolidaysPayload {
  holidays: AdminHoliday[]
  year: string
  next_year: number
  next_year_imported: boolean
}

function normalizeHoliday(raw: unknown): AdminHoliday {
  const row = record(raw)
  return {
    date: text(pick(row, 'date', 'Date')),
    kind: text(pick(row, 'kind', 'Kind')),
  }
}

export const adminHolidaysQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'holidays'],
    queryFn: async (): Promise<AdminHolidaysPayload> => {
      const raw = record(await api.get<unknown>(`${adminBase}/holidays`))
      const items = Array.isArray(raw.holidays) ? raw.holidays : []
      return {
        holidays: items.map(normalizeHoliday).filter((item) => item.date !== ''),
        year: text(raw.year),
        next_year: int(raw.next_year) || new Date().getFullYear() + 1,
        next_year_imported: raw.next_year_imported === true,
      }
    },
    staleTime: 30_000,
  })

export function importAdminHolidays(
  entries: AdminHoliday[],
): Promise<{ imported?: number }> {
  return api.post<{ imported?: number }>(`${adminBase}/holidays`, entries)
}

// ---------------------------------------------------------------------------
// 挂单审核
// ---------------------------------------------------------------------------

export interface AdminListing {
  id: number
  seller_id: number
  channel_id: number
  vendor: string
  models: string[]
  multiplier_milli: number
  concurrency_limit: number
  rpm_limit: number
  daily_cap_micro: number | string
  status: string
  key_fingerprint8: string
  created_at: string
}

export function normalizeAdminListing(raw: unknown): AdminListing {
  const row = record(raw)
  const models = Array.isArray(row.models) ? row.models.map((item) => text(item)) : []
  return {
    id: int(pick(row, 'id', 'ID')),
    seller_id: int(pick(row, 'seller_id', 'SellerID')),
    channel_id: int(pick(row, 'channel_id', 'ChannelID')),
    vendor: text(pick(row, 'vendor', 'Vendor')),
    models,
    multiplier_milli: int(pick(row, 'multiplier_milli', 'MultiplierMilli')),
    concurrency_limit: int(pick(row, 'concurrency_limit', 'ConcurrencyLimit')),
    rpm_limit: int(pick(row, 'rpm_limit', 'RPMLimit')),
    daily_cap_micro: micro(pick(row, 'daily_cap_micro', 'DailyCapMicro')),
    status: text(pick(row, 'status', 'Status')),
    key_fingerprint8: text(pick(row, 'key_fingerprint8', 'KeyFingerprint8')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export const adminListingsQuery = (status?: string) =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'listings', status ?? 'all'],
    queryFn: async (): Promise<AdminListing[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/listings`, {
        query: { status: status || undefined },
      })
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminListing)
    },
    staleTime: 15_000,
  })

export function banAdminListing(id: number): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/listings/${id}/ban`)
}

export function unbanAdminListing(id: number): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/listings/${id}/unban`)
}

// ---------------------------------------------------------------------------
// 待审队列（P5，按 host 聚合）
// ---------------------------------------------------------------------------

export interface AdminPendingListing {
  id: number
  seller_id: number
  vendor: string
  custom_base_url: string
  models: string[]
  created_at: string
  seller_real_name_status: string
}

export interface AdminPendingHostGroup {
  host: string
  count: number
  listings: AdminPendingListing[]
}

export function normalizeAdminPendingListing(raw: unknown): AdminPendingListing {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    seller_id: int(pick(row, 'seller_id', 'SellerID')),
    vendor: text(pick(row, 'vendor', 'Vendor')),
    custom_base_url: text(pick(row, 'custom_base_url', 'CustomBaseURL')),
    models: stringList(pick(row, 'models', 'Models')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
    seller_real_name_status: text(
      pick(row, 'seller_real_name_status', 'SellerRealNameStatus'),
    ),
  }
}

export function normalizeAdminPendingHostGroup(raw: unknown): AdminPendingHostGroup {
  const row = record(raw)
  const listings = Array.isArray(row.listings)
    ? row.listings.map(normalizeAdminPendingListing)
    : []
  return {
    host: text(pick(row, 'host', 'Host')),
    count: int(pick(row, 'count', 'Count')) || listings.length,
    listings,
  }
}

export const pendingListingsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'listings', 'pending-groups'],
    queryFn: async (): Promise<AdminPendingHostGroup[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/listings/pending`)
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminPendingHostGroup)
    },
    staleTime: 15_000,
  })

export type ListingRejectAction = 'draft' | 'ban'

export function approveListing(id: number, notes?: string): Promise<unknown> {
  const body = notes ? { notes } : undefined
  return api.post<unknown>(`${adminBase}/listings/${id}/approve`, body)
}

export function rejectListing(
  id: number,
  action: ListingRejectAction,
  notes?: string,
): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/listings/${id}/reject`, {
    action,
    ...(notes ? { notes } : {}),
  })
}

// ---------------------------------------------------------------------------
// 全链路排查（P5，按 request_id）
// ---------------------------------------------------------------------------

export interface AdminTraceRequest {
  id: number
  request_id: string
  buyer_user_id: number
  buyer_token_id: number
  seller_id: number
  listing_id: number
  model: string
  upstream_model: string
  model_fingerprint_anomaly: boolean
  tier: string
  price_version: number
  multiplier_milli: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  search_count: number
  fetch_count: number
  token_debit_micro: number | string
  tool_debit_micro: number | string
  total_debit_micro: number | string
  gift_debit_micro: number | string
  capped_micro: number | string
  platform_fee_micro: number | string
  seller_credit_micro: number | string
  official_cost_micro: number | string
  usage_estimated: boolean
  status: string
  latency_ms: number
  created_at: string
}

export interface AdminTraceHop {
  seq: number
  event: string
  listing_id: number
  detail: string
  at: string
}

export interface AdminTraceLedgerRow {
  id: number
  request_id: string
  entry_type: string
  account: string
  listing_id: number
  amount_micro: number | string
  price_version: number
  multiplier_milli: number
  status: string
  available_at: string | null
  created_at: string
}

export interface AdminTraceProbeRow {
  id: number
  listing_id: number
  type: string
  ok: boolean
  latency_ms: number
  detail: string
  created_at: string
}

export interface AdminRequestTrace {
  request: AdminTraceRequest
  retry_chain: AdminTraceHop[]
  ledger: AdminTraceLedgerRow[]
  probes: AdminTraceProbeRow[]
}

function nullableTime(value: unknown): string | null {
  return value === undefined || value === null || value === '' ? null : text(value)
}

export function normalizeAdminTraceHop(raw: unknown): AdminTraceHop {
  const row = record(raw)
  return {
    seq: int(pick(row, 'seq', 'Seq')),
    event: text(pick(row, 'event', 'Event')),
    listing_id: int(pick(row, 'listing_id', 'ListingID')),
    detail: text(pick(row, 'detail', 'Detail')),
    at: text(pick(row, 'at', 'At')),
  }
}

export function normalizeAdminTraceLedgerRow(raw: unknown): AdminTraceLedgerRow {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    request_id: text(pick(row, 'request_id', 'RequestID')),
    entry_type: text(pick(row, 'entry_type', 'EntryType')),
    account: text(pick(row, 'account', 'Account')),
    listing_id: int(pick(row, 'listing_id', 'ListingID')),
    amount_micro: micro(pick(row, 'amount_micro', 'AmountMicro')),
    price_version: int(pick(row, 'price_version', 'PriceVersion')),
    multiplier_milli: int(pick(row, 'multiplier_milli', 'MultiplierMilli')),
    status: text(pick(row, 'status', 'Status')),
    available_at: nullableTime(pick(row, 'available_at', 'AvailableAt')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export function normalizeAdminTraceProbeRow(raw: unknown): AdminTraceProbeRow {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    listing_id: int(pick(row, 'listing_id', 'ListingID')),
    type: text(pick(row, 'type', 'Type')),
    ok: bool(pick(row, 'ok', 'OK')),
    latency_ms: int(pick(row, 'latency_ms', 'LatencyMs')),
    detail: text(pick(row, 'detail', 'Detail')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export function normalizeAdminRequestTrace(raw: unknown): AdminRequestTrace {
  const row = record(raw)
  const req = record(pick(row, 'request', 'Request'))
  const chain = pick(row, 'retry_chain', 'RetryChain')
  const ledger = pick(row, 'ledger', 'Ledger')
  const probes = pick(row, 'probes', 'Probes')
  return {
    request: {
      id: int(pick(req, 'id', 'ID')),
      request_id: text(pick(req, 'request_id', 'RequestID')),
      buyer_user_id: int(pick(req, 'buyer_user_id', 'BuyerUserID')),
      buyer_token_id: int(pick(req, 'buyer_token_id', 'BuyerTokenID')),
      seller_id: int(pick(req, 'seller_id', 'SellerID')),
      listing_id: int(pick(req, 'listing_id', 'ListingID')),
      model: text(pick(req, 'model', 'Model')),
      upstream_model: text(pick(req, 'upstream_model', 'UpstreamModel')),
      model_fingerprint_anomaly: bool(
        pick(req, 'model_fingerprint_anomaly', 'ModelFingerprintAnomaly'),
      ),
      tier: text(pick(req, 'tier', 'Tier')),
      price_version: int(pick(req, 'price_version', 'PriceVersion')),
      multiplier_milli: int(pick(req, 'multiplier_milli', 'MultiplierMilli')),
      input_tokens: int(pick(req, 'input_tokens', 'InputTokens')),
      output_tokens: int(pick(req, 'output_tokens', 'OutputTokens')),
      cache_read_tokens: int(pick(req, 'cache_read_tokens', 'CacheReadTokens')),
      cache_write_tokens: int(pick(req, 'cache_write_tokens', 'CacheWriteTokens')),
      search_count: int(pick(req, 'search_count', 'SearchCount')),
      fetch_count: int(pick(req, 'fetch_count', 'FetchCount')),
      token_debit_micro: micro(pick(req, 'token_debit_micro', 'TokenDebitMicro')),
      tool_debit_micro: micro(pick(req, 'tool_debit_micro', 'ToolDebitMicro')),
      total_debit_micro: micro(pick(req, 'total_debit_micro', 'TotalDebitMicro')),
      gift_debit_micro: micro(pick(req, 'gift_debit_micro', 'GiftDebitMicro')),
      capped_micro: micro(pick(req, 'capped_micro', 'CappedMicro')),
      platform_fee_micro: micro(pick(req, 'platform_fee_micro', 'PlatformFeeMicro')),
      seller_credit_micro: micro(pick(req, 'seller_credit_micro', 'SellerCreditMicro')),
      official_cost_micro: micro(pick(req, 'official_cost_micro', 'OfficialCostMicro')),
      usage_estimated: bool(pick(req, 'usage_estimated', 'UsageEstimated')),
      status: text(pick(req, 'status', 'Status')),
      latency_ms: int(pick(req, 'latency_ms', 'LatencyMs')),
      created_at: text(pick(req, 'created_at', 'CreatedAt')),
    },
    retry_chain: (Array.isArray(chain) ? chain : []).map(normalizeAdminTraceHop),
    ledger: (Array.isArray(ledger) ? ledger : []).map(normalizeAdminTraceLedgerRow),
    probes: (Array.isArray(probes) ? probes : []).map(normalizeAdminTraceProbeRow),
  }
}

export const requestTraceQuery = (requestId: string) => {
  const trimmed = requestId.trim()
  return queryOptions({
    queryKey: ['marketplace', 'admin', 'request-trace', trimmed],
    enabled: trimmed !== '',
    queryFn: async (): Promise<AdminRequestTrace> =>
      normalizeAdminRequestTrace(
        await api.get<unknown>(`${adminBase}/requests/${encodeURIComponent(trimmed)}`),
      ),
    staleTime: 15_000,
  })
}


// ---------------------------------------------------------------------------
// 提现审批
// ---------------------------------------------------------------------------

export interface AdminPayout {
  id: number
  seller_id: number
  amount_micro: number | string
  status: string
  reviewer_id: number
  reviewed_at: string | null
  created_at: string
  updated_at: string
}

export function normalizeAdminPayout(raw: unknown): AdminPayout {
  const row = record(raw)
  const reviewed = pick(row, 'reviewed_at', 'ReviewedAt')
  return {
    id: int(pick(row, 'id', 'ID')),
    seller_id: int(pick(row, 'seller_id', 'SellerID')),
    amount_micro: micro(pick(row, 'amount_micro', 'AmountMicro')),
    status: text(pick(row, 'status', 'Status')),
    reviewer_id: int(pick(row, 'reviewer_id', 'ReviewerID')),
    reviewed_at: reviewed === undefined ? null : text(reviewed),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
    updated_at: text(pick(row, 'updated_at', 'UpdatedAt')),
  }
}

export const adminPayoutsQuery = (status?: string) =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'payouts', status ?? 'all'],
    queryFn: async (): Promise<AdminPayout[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/payouts`, {
        query: { status: status || undefined },
      })
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminPayout)
    },
    staleTime: 15_000,
  })

export function approveAdminPayout(id: number): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/payouts/${id}/approve`)
}

export function rejectAdminPayout(id: number, reason: string): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/payouts/${id}/reject`, { reason })
}

export function markAdminPayoutPaid(id: number): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/payouts/${id}/mark-paid`)
}

// ---------------------------------------------------------------------------
// 冲正与反查
// ---------------------------------------------------------------------------

export interface AdminReverseResult {
  reversed: boolean
  balances: Record<string, number | string>
}

export function normalizeReverseResult(raw: unknown): AdminReverseResult {
  const row = record(raw)
  const rawBalances = record(row.balances)
  const balances: Record<string, number | string> = {}
  for (const [account, value] of Object.entries(rawBalances)) {
    if (typeof value === 'number' || typeof value === 'string') balances[account] = value
  }
  return { reversed: row.reversed === true, balances }
}

export async function reverseAdminRequest(requestId: string): Promise<AdminReverseResult> {
  const raw = await api.post<unknown>(`${adminBase}/reverse`, { request_id: requestId })
  return normalizeReverseResult(raw)
}

export interface AdminDeanonResult {
  seller_id: number
  buyer_user_id: number
  code: string
}

export function deanonBuyer(
  sellerId: number,
  code: string,
): Promise<AdminDeanonResult> {
  return api.get<AdminDeanonResult>(`${adminBase}/deanon`, {
    query: { seller_id: sellerId, code },
  })
}

// ---------------------------------------------------------------------------
// 模型池 / 渠道池 / 渠道模型（MODEL-CHANNEL-POOL）
// ---------------------------------------------------------------------------

export type PoolChannelKind = 'official' | 'plan' | 'third_party'

export interface AdminPoolModel {
  id: number
  name: string
  origin_vendor: string
  context_length: number
  capabilities: string[]
  aliases: string[]
  verify_prompt: string
  status: string
}

export interface AdminPoolChannel {
  id: number
  slug: string
  name: string
  kind: string
  protocols: { openai: string; anthropic: string }
  allow_host_pattern: string
  key_pattern: string
  deny_paths: string[]
  deny_key_prefixes: string[]
  adaptor: string
  strip_v1_prefix: boolean
  passthrough_body: boolean
  quota_window: Record<string, unknown>
  exhaustion_signature: string
  terms_url: string
  terms_checked_at: string | null
  status: string
}

export interface AdminPoolChannelModel {
  id: number
  channel_slug: string
  upstream_model: string
  model: string
  coefficient_milli: number
  differences: Record<string, unknown>
  verified: boolean
  verified_at: string | null
  fingerprint_ratio: number
  status: string
}

export interface AdminPoolModelInput {
  name: string
  origin_vendor?: string
  context_length?: number
  capabilities?: string[]
  aliases?: string[]
  verify_prompt?: string
  status?: string
}

export interface AdminPoolChannelInput {
  slug: string
  name?: string
  kind?: PoolChannelKind
  protocols?: { openai: string; anthropic: string }
  allow_host_pattern?: string
  key_pattern?: string
  deny_paths?: string[]
  deny_key_prefixes?: string[]
  adaptor?: string
  strip_v1_prefix?: boolean
  passthrough_body?: boolean
  quota_window?: Record<string, unknown>
  exhaustion_signature?: string
  terms_url?: string
  terms_checked_at?: string | null
  status?: string
}

export interface AdminPoolChannelModelInput {
  channel_slug: string
  upstream_model: string
  model: string
  coefficient_milli?: number
  differences?: Record<string, unknown>
  verified?: boolean
  status?: string
}

function stringList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map((item) => text(item)).filter(Boolean)
  if (typeof value === 'string' && value.trim()) {
    try {
      const parsed: unknown = JSON.parse(value)
      if (Array.isArray(parsed)) return parsed.map((item) => text(item)).filter(Boolean)
    } catch {
      return []
    }
  }
  return []
}

function objectValue(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  if (typeof value === 'string' && value.trim()) {
    try {
      const parsed: unknown = JSON.parse(value)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return parsed as Record<string, unknown>
      }
    } catch {
      return {}
    }
  }
  return {}
}

function bool(value: unknown): boolean {
  return value === true || value === 'true' || value === 1
}

export function normalizeAdminPoolModel(raw: unknown): AdminPoolModel {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    name: text(pick(row, 'name', 'Name')),
    origin_vendor: text(pick(row, 'origin_vendor', 'OriginVendor')),
    context_length: int(pick(row, 'context_length', 'ContextLength')),
    capabilities: stringList(pick(row, 'capabilities', 'Capabilities')),
    aliases: stringList(pick(row, 'aliases', 'Aliases')),
    verify_prompt: text(pick(row, 'verify_prompt', 'VerifyPrompt')),
    status: text(pick(row, 'status', 'Status')),
  }
}

export function normalizeAdminPoolChannel(raw: unknown): AdminPoolChannel {
  const row = record(raw)
  const protocols = record(pick(row, 'protocols', 'Protocols'))
  const checked = pick(row, 'terms_checked_at', 'TermsCheckedAt')
  return {
    id: int(pick(row, 'id', 'ID')),
    slug: text(pick(row, 'slug', 'Slug')),
    name: text(pick(row, 'name', 'Name')),
    kind: text(pick(row, 'kind', 'Kind')),
    protocols: {
      openai: text(pick(protocols, 'openai', 'OpenAI')),
      anthropic: text(pick(protocols, 'anthropic', 'Anthropic')),
    },
    allow_host_pattern: text(pick(row, 'allow_host_pattern', 'AllowHostPattern')),
    key_pattern: text(pick(row, 'key_pattern', 'KeyPattern')),
    deny_paths: stringList(pick(row, 'deny_paths', 'DenyPaths')),
    deny_key_prefixes: stringList(pick(row, 'deny_key_prefixes', 'DenyKeyPrefixes')),
    adaptor: text(pick(row, 'adaptor', 'Adaptor')),
    strip_v1_prefix: bool(pick(row, 'strip_v1_prefix', 'StripV1Prefix')),
    passthrough_body: bool(pick(row, 'passthrough_body', 'PassthroughBody')),
    quota_window: objectValue(pick(row, 'quota_window', 'QuotaWindow')),
    exhaustion_signature: text(pick(row, 'exhaustion_signature', 'ExhaustionSignature')),
    terms_url: text(pick(row, 'terms_url', 'TermsURL')),
    terms_checked_at: checked === undefined ? null : text(checked),
    status: text(pick(row, 'status', 'Status')),
  }
}

export function normalizeAdminPoolChannelModel(raw: unknown): AdminPoolChannelModel {
  const row = record(raw)
  const verifiedAt = pick(row, 'verified_at', 'VerifiedAt')
  return {
    id: int(pick(row, 'id', 'ID')),
    channel_slug: text(pick(row, 'channel_slug', 'ChannelSlug')),
    upstream_model: text(pick(row, 'upstream_model', 'UpstreamModel')),
    model: text(pick(row, 'model', 'Model')),
    coefficient_milli: int(pick(row, 'coefficient_milli', 'CoefficientMilli')),
    differences: objectValue(pick(row, 'differences', 'Differences')),
    verified: bool(pick(row, 'verified', 'Verified')),
    verified_at: verifiedAt === undefined ? null : text(verifiedAt),
    fingerprint_ratio: Number(pick(row, 'fingerprint_ratio', 'FingerprintRatio') ?? 0) || 0,
    status: text(pick(row, 'status', 'Status')),
  }
}

async function listPoolItems<T>(
  path: string,
  normalize: (raw: unknown) => T,
): Promise<T[]> {
  const raw = await api.get<unknown>(path)
  const items = Array.isArray(raw) ? raw : record(raw).items
  return (Array.isArray(items) ? items : []).map(normalize)
}

export const adminPoolModelsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'pool', 'models'],
    queryFn: () =>
      listPoolItems(`${adminBase}/pool/models`, normalizeAdminPoolModel),
    staleTime: 15_000,
  })

export const adminPoolChannelsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'pool', 'channels'],
    queryFn: () =>
      listPoolItems(`${adminBase}/pool/channels`, normalizeAdminPoolChannel),
    staleTime: 15_000,
  })

export const adminPoolChannelModelsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'pool', 'channel-models'],
    queryFn: () =>
      listPoolItems(`${adminBase}/pool/channel-models`, normalizeAdminPoolChannelModel),
    staleTime: 15_000,
  })

export function putAdminPoolModel(input: AdminPoolModelInput): Promise<AdminPoolModel> {
  return api.put<unknown>(`${adminBase}/pool/models`, input).then(normalizeAdminPoolModel)
}

export function deleteAdminPoolModel(name: string): Promise<unknown> {
  return api.delete<unknown>(`${adminBase}/pool/models`, { query: { name } })
}

export function putAdminPoolChannel(input: AdminPoolChannelInput): Promise<AdminPoolChannel> {
  return api.put<unknown>(`${adminBase}/pool/channels`, input).then(normalizeAdminPoolChannel)
}

export function deleteAdminPoolChannel(slug: string): Promise<unknown> {
  return api.delete<unknown>(`${adminBase}/pool/channels`, { query: { slug } })
}

export function putAdminPoolChannelModel(
  input: AdminPoolChannelModelInput,
): Promise<AdminPoolChannelModel> {
  return api
    .put<unknown>(`${adminBase}/pool/channel-models`, input)
    .then(normalizeAdminPoolChannelModel)
}

export function deleteAdminPoolChannelModel(
  channelSlug: string,
  upstreamModel: string,
): Promise<unknown> {
  return api.delete<unknown>(`${adminBase}/pool/channel-models`, {
    query: { channel_slug: channelSlug, upstream_model: upstreamModel },
  })
}

// ---------------------------------------------------------------------------
// 认证池（P5）
// ---------------------------------------------------------------------------

export type TrustPoolKind = 'official' | 'third_party' | 'plan'
export type TrustPoolTrust = 'trusted' | 'watch' | 'blocked'
export type TrustPoolSource = 'seed' | 'manual' | 'probe'
export type TrustPoolEndpoint = 'openai_chat' | 'openai_responses' | 'anthropic_messages'

export const TRUST_POOL_ENDPOINTS: readonly TrustPoolEndpoint[] = [
  'openai_chat',
  'openai_responses',
  'anthropic_messages',
]

export interface AdminTrustPool {
  id: number
  host: string
  host_pattern: string
  path_prefix: string
  key_prefix: string
  vendor_slug: string
  kind: string
  trust: string
  endpoints: string[]
  allowed_models: unknown
  official_price_book_id: number
  quota_window_template: unknown
  deny_key_prefixes: string[]
  deny_paths: string[]
  error_code_table: unknown
  default_headers: unknown
  account_fingerprint_rules: unknown
  source: string
  notes: string
  expires_at: string | null
  created_at: string
  updated_at: string
}

export interface AdminTrustPoolList {
  items: AdminTrustPool[]
  total: number
  page: number
  page_size: number
}

export interface TrustPoolFilters {
  trust?: string
  kind?: string
  source?: string
  page?: number
  page_size?: number
}

export interface TrustPoolInput {
  host: string
  host_pattern: string
  path_prefix: string
  key_prefix: string
  vendor_slug: string
  kind: TrustPoolKind
  trust: TrustPoolTrust
  endpoints: TrustPoolEndpoint[]
  /** RFC3339; an empty string clears the expiry on update. */
  expires_at: string
  deny_key_prefixes: string[]
  deny_paths: string[]
  notes: string
}

/** JSON columns arrive already decoded; a raw string is parsed when possible. */
function jsonValue(value: unknown): unknown {
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return null
    try {
      return JSON.parse(trimmed) as unknown
    } catch {
      return value
    }
  }
  return value ?? null
}

export function normalizeAdminTrustPool(raw: unknown): AdminTrustPool {
  const row = record(raw)
  const expiresAt = pick(row, 'expires_at', 'ExpiresAt')
  return {
    id: int(pick(row, 'id', 'ID')),
    host: text(pick(row, 'host', 'Host')),
    host_pattern: text(pick(row, 'host_pattern', 'HostPattern')),
    path_prefix: text(pick(row, 'path_prefix', 'PathPrefix')),
    key_prefix: text(pick(row, 'key_prefix', 'KeyPrefix')),
    vendor_slug: text(pick(row, 'vendor_slug', 'VendorSlug')),
    kind: text(pick(row, 'kind', 'Kind')),
    trust: text(pick(row, 'trust', 'Trust')),
    endpoints: stringList(pick(row, 'endpoints', 'Endpoints')),
    allowed_models: jsonValue(pick(row, 'allowed_models', 'AllowedModels')),
    official_price_book_id: int(pick(row, 'official_price_book_id', 'OfficialPriceBookID')),
    quota_window_template: jsonValue(
      pick(row, 'quota_window_template', 'QuotaWindowTemplate'),
    ),
    deny_key_prefixes: stringList(pick(row, 'deny_key_prefixes', 'DenyKeyPrefixes')),
    deny_paths: stringList(pick(row, 'deny_paths', 'DenyPaths')),
    error_code_table: jsonValue(pick(row, 'error_code_table', 'ErrorCodeTable')),
    default_headers: jsonValue(pick(row, 'default_headers', 'DefaultHeaders')),
    account_fingerprint_rules: jsonValue(
      pick(row, 'account_fingerprint_rules', 'AccountFingerprintRules'),
    ),
    source: text(pick(row, 'source', 'Source')),
    notes: text(pick(row, 'notes', 'Notes')),
    expires_at: expiresAt === undefined ? null : text(expiresAt) || null,
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
    updated_at: text(pick(row, 'updated_at', 'UpdatedAt')),
  }
}

export const trustPoolQuery = (filters: TrustPoolFilters = {}) => {
  const page = filters.page ?? 1
  const pageSize = filters.page_size ?? 50
  return queryOptions({
    queryKey: [
      'marketplace',
      'admin',
      'trust-pool',
      filters.trust ?? 'all',
      filters.kind ?? 'all',
      filters.source ?? 'all',
      page,
      pageSize,
    ],
    queryFn: async (): Promise<AdminTrustPoolList> => {
      const raw = await api.get<unknown>(`${adminBase}/trust-pool`, {
        query: {
          trust: filters.trust || undefined,
          kind: filters.kind || undefined,
          source: filters.source || undefined,
          page,
          page_size: pageSize,
        },
      })
      const envelope = record(raw)
      const items = Array.isArray(raw) ? raw : envelope.items
      return {
        items: (Array.isArray(items) ? items : []).map(normalizeAdminTrustPool),
        total: int(envelope.total),
        page: int(envelope.page) || page,
        page_size: int(envelope.page_size) || pageSize,
      }
    },
    staleTime: 15_000,
  })
}

export const trustPoolDetailQuery = (id: number) =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'trust-pool', 'detail', id],
    enabled: id > 0,
    queryFn: async (): Promise<AdminTrustPool> =>
      normalizeAdminTrustPool(await api.get<unknown>(`${adminBase}/trust-pool/${id}`)),
    staleTime: 15_000,
  })

export function createTrustPool(input: TrustPoolInput): Promise<AdminTrustPool> {
  return api.post<unknown>(`${adminBase}/trust-pool`, input).then(normalizeAdminTrustPool)
}

export function updateTrustPool(id: number, input: TrustPoolInput): Promise<AdminTrustPool> {
  return api
    .put<unknown>(`${adminBase}/trust-pool/${id}`, input)
    .then(normalizeAdminTrustPool)
}

export function deleteTrustPool(id: number): Promise<unknown> {
  return api.delete<unknown>(`${adminBase}/trust-pool/${id}`)
}

/** One queued trust-pool change awaiting a second operator's decision. */
export interface AdminTrustPoolApproval {
  id: number
  trust_pool_id: number
  action: string
  target: string
  payload: unknown
  submitter_id: number
  approver_id: number
  status: string
  notes: string
  created_at: string
  approved_at: string | null
}

export function normalizeAdminTrustPoolApproval(raw: unknown): AdminTrustPoolApproval {
  const row = record(raw)
  const approvedAt = pick(row, 'approved_at', 'ApprovedAt')
  return {
    id: int(pick(row, 'id', 'ID')),
    trust_pool_id: int(pick(row, 'trust_pool_id', 'TrustPoolID')),
    action: text(pick(row, 'action', 'Action')),
    target: text(pick(row, 'target', 'Target')),
    payload: pick(row, 'payload', 'Payload') ?? null,
    submitter_id: int(pick(row, 'submitter_id', 'SubmitterID')),
    approver_id: int(pick(row, 'approver_id', 'ApproverID')),
    status: text(pick(row, 'status', 'Status')),
    notes: text(pick(row, 'notes', 'Notes')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
    approved_at: approvedAt === undefined || approvedAt === null ? null : text(approvedAt),
  }
}

export const trustPoolApprovalsQuery = (status = 'pending', enabled = true) =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'trust-pool', 'approvals', status],
    enabled,
    queryFn: async (): Promise<AdminTrustPoolApproval[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/trust-pool/approvals`, {
        query: { status },
      })
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminTrustPoolApproval)
    },
    staleTime: 10_000,
  })

export function approveTrustPoolChange(id: number): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/trust-pool/approvals/${id}/approve`)
}

export function rejectTrustPoolChange(id: number): Promise<unknown> {
  return api.post<unknown>(`${adminBase}/trust-pool/approvals/${id}/reject`)
}

// ---------------------------------------------------------------------------
// 派生指标（P5）
// ---------------------------------------------------------------------------

export const OPS_METRIC_NAMES = [
  'stream_cut_rate',
  '503_reason',
  'fingerprint_anomaly',
  'subscription_signal',
  'slot_usage',
  'slot_renew_failure',
  'pre_freeze_deviation',
] as const

export type OpsMetricName = (typeof OPS_METRIC_NAMES)[number]

export type OpsMetricRange = '1h' | '24h' | '7d'

export const OPS_METRIC_RANGES: readonly OpsMetricRange[] = ['1h', '24h', '7d']

/** RFC3339 lower bound for a range, computed once per range change. */
export function opsMetricSince(range: OpsMetricRange, now = Date.now()): string {
  const hours = range === '1h' ? 1 : range === '7d' ? 24 * 7 : 24
  return new Date(now - hours * 3_600_000).toISOString()
}

export interface AdminOpsMetric {
  id: number
  metric_name: string
  metric_value: number
  dimensions: Record<string, unknown>
  bucket_time: string
  created_at: string
}

export function normalizeAdminOpsMetric(raw: unknown): AdminOpsMetric {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'ID')),
    metric_name: text(pick(row, 'metric_name', 'MetricName')),
    metric_value: Number(pick(row, 'metric_value', 'MetricValue') ?? 0) || 0,
    dimensions: objectValue(pick(row, 'dimensions', 'Dimensions')),
    bucket_time: text(pick(row, 'bucket_time', 'BucketTime')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export interface OpsMetricFilters {
  metric_name?: OpsMetricName | ''
  since: string
}

export const opsMetricsQuery = (filters: OpsMetricFilters) =>
  queryOptions({
    queryKey: [
      'marketplace',
      'admin',
      'metrics',
      filters.metric_name || 'all',
      filters.since,
    ],
    queryFn: async (): Promise<AdminOpsMetric[]> => {
      const rows = await api.get<unknown[]>(`${adminBase}/metrics`, {
        query: {
          metric_name: filters.metric_name || undefined,
          since: filters.since,
        },
      })
      return (Array.isArray(rows) ? rows : []).map(normalizeAdminOpsMetric)
    },
    staleTime: 15_000,
  })

// ---------------------------------------------------------------------------
// 活跃预占 / 强制释放（P5-C）
// ---------------------------------------------------------------------------

export interface AdminActiveSession {
  request_id: string
  listing_id: number
  buyer_id: number
  frozen_micro: number | string
  created_at: string
}

export function normalizeAdminActiveSession(raw: unknown): AdminActiveSession {
  const row = record(raw)
  return {
    request_id: text(pick(row, 'request_id', 'RequestID')),
    listing_id: int(pick(row, 'listing_id', 'ListingID')),
    buyer_id: int(pick(row, 'buyer_id', 'BuyerID')),
    frozen_micro: micro(pick(row, 'frozen_micro', 'FrozenMicro')),
    created_at: text(pick(row, 'created_at', 'CreatedAt')),
  }
}

export interface AdminActiveSessions {
  total: number
  frozen_micro: number | string
  items: AdminActiveSession[]
}

/** listingID 0 lists the active sessions of every listing. */
export const activeSessionsQuery = (listingID = 0) =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'sessions', listingID],
    queryFn: async (): Promise<AdminActiveSessions> => {
      const raw = await api.get<unknown>(`${adminBase}/sessions`, {
        query: { listing_id: listingID > 0 ? listingID : undefined },
      })
      const envelope = record(raw)
      const items = Array.isArray(raw) ? raw : envelope.items
      return {
        total: int(envelope.total),
        frozen_micro: micro(envelope.frozen_micro),
        items: (Array.isArray(items) ? items : []).map(normalizeAdminActiveSession),
      }
    },
    staleTime: 5_000,
  })

export interface ForceReleaseResult {
  listing_id: number
  sessions: number
  frozen_micro: number | string
  slots_released: number
  refund_failures: number
}

/** Force-release every active freeze and slot of one listing. */
export function forceRelease(listingID: number): Promise<ForceReleaseResult> {
  return api
    .post<unknown>(`${adminBase}/sessions/force-release`, { listing_id: listingID })
    .then((raw) => {
      const row = record(raw)
      return {
        listing_id: int(pick(row, 'listing_id', 'ListingID')),
        sessions: int(pick(row, 'sessions', 'Sessions')),
        frozen_micro: micro(pick(row, 'frozen_micro', 'FrozenMicro')),
        slots_released: int(pick(row, 'slots_released', 'SlotsReleased')),
        refund_failures: int(pick(row, 'refund_failures', 'RefundFailures')),
      }
    })
}

// ---------------------------------------------------------------------------
// 风控图谱（P5-C）
// ---------------------------------------------------------------------------

export interface AdminRelatedUser {
  user_id: number
  relation_type: string
  value: string
}

export function normalizeAdminRelatedUser(raw: unknown): AdminRelatedUser {
  const row = record(raw)
  return {
    user_id: int(pick(row, 'user_id', 'UserID')),
    relation_type: text(pick(row, 'relation_type', 'RelationType')),
    value: text(pick(row, 'value', 'Value')),
  }
}

export interface AdminRelatedUsers {
  user_id: number
  items: AdminRelatedUser[]
}

/** Related accounts of one user (shared login IP or payout account). */
export const relatedUsersQuery = (userID: number) =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'risk', 'related', userID],
    enabled: userID > 0,
    queryFn: async (): Promise<AdminRelatedUsers> => {
      const raw = await api.get<unknown>(`${adminBase}/risk/related`, {
        query: { user_id: userID },
      })
      const envelope = record(raw)
      const items = Array.isArray(raw) ? raw : envelope.items
      return {
        user_id: int(pick(envelope, 'user_id', 'UserID')) || userID,
        items: (Array.isArray(items) ? items : []).map(normalizeAdminRelatedUser),
      }
    },
    staleTime: 15_000,
  })
