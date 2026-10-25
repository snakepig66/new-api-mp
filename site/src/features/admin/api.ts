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
