/**
 * 广场渠道（new-api channels 表）管理接口。
 *
 * 后端路由见 router/channel-router.go，挂在 /api/channel 下，站点登录态
 * （AdminAuth）可直接调用。列表接口从不返回 key；编辑时空 key 表示不修改。
 */

import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

type UnknownRecord = Record<string, unknown>

function record(raw: unknown): UnknownRecord {
  return raw && typeof raw === 'object' ? (raw as UnknownRecord) : {}
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

function nullableText(value: unknown): string | null {
  return typeof value === 'string' ? value : null
}

/** Keep snake_case keys only; the Go struct has explicit json tags. */
export interface Channel {
  id: number
  type: number
  name: string
  key: string
  status: number
  base_url: string | null
  models: string
  model_mapping: string | null
  group: string
  priority: number
  weight: number
  tag: string | null
  response_time: number
  test_time: number
  test_model: string | null
  created_time: number
  remark: string | null
  setting: string | null
  other: string
  settings: string
}

export function normalizeChannel(raw: unknown): Channel {
  const row = record(raw)
  return {
    id: int(row.id),
    type: int(row.type),
    name: text(row.name),
    key: text(row.key),
    status: int(row.status),
    base_url: nullableText(row.base_url),
    models: text(row.models),
    model_mapping: nullableText(row.model_mapping),
    group: text(row.group) || 'default',
    priority: int(row.priority),
    weight: int(row.weight),
    tag: nullableText(row.tag),
    response_time: int(row.response_time),
    test_time: int(row.test_time),
    test_model: nullableText(row.test_model),
    created_time: int(row.created_time),
    remark: nullableText(row.remark),
    setting: nullableText(row.setting),
    other: text(row.other),
    settings: text(row.settings),
  }
}

/** Seller listings create their own managed channels (name "mp:<vendor>:<seller>");
 * those belong to the market, not the plaza, and are hidden here. */
export function isPlazaChannel(ch: Channel): boolean {
  return !ch.name.startsWith('mp:')
}

export interface ChannelListParams {
  page: number
  pageSize: number
  keyword?: string
  status?: 'enabled' | 'disabled'
}

export interface ChannelListResult {
  items: Channel[]
  total: number
}

function paramsToQuery(params: ChannelListParams) {
  return {
    p: params.page,
    page_size: params.pageSize,
    ...(params.status ? { status: params.status } : {}),
  }
}

export const channelsQuery = (params: ChannelListParams) =>
  queryOptions({
    queryKey: ['admin', 'channels', params],
    queryFn: async (): Promise<ChannelListResult> => {
      const keyword = params.keyword?.trim()
      if (keyword) {
        const raw = await api.get<{ items?: unknown[]; total?: number }>('/channel/search', {
          query: { ...paramsToQuery(params), keyword },
        })
        return {
          items: (raw?.items ?? []).map(normalizeChannel).filter(isPlazaChannel),
          total: int(raw?.total),
        }
      }
      const raw = await api.get<{ items?: unknown[]; total?: number }>('/channel/', {
        query: paramsToQuery(params),
      })
      return {
        items: (raw?.items ?? []).map(normalizeChannel).filter(isPlazaChannel),
        total: int(raw?.total),
      }
    },
    staleTime: 10_000,
  })

/** 渠道类型 → 内置默认 Base URL（空串的不会返回）。 */
export const channelDefaultBaseUrlsQuery = () =>
  queryOptions({
    queryKey: ['admin', 'channels', 'default-base-urls'],
    queryFn: async (): Promise<Record<number, string>> => {
      const raw = await api.get<Record<string, string>>('/channel/default_base_urls')
      const map: Record<number, string> = {}
      for (const [key, value] of Object.entries(raw ?? {})) {
        const type = Number(key)
        if (Number.isFinite(type) && typeof value === 'string' && value) {
          map[type] = value
        }
      }
      return map
    },
    staleTime: 5 * 60_000,
  })

export interface ChannelInput {
  id?: number
  type: number
  name: string
  base_url?: string
  key: string
  models: string
  model_mapping?: string
  group: string
  priority: number
  weight: number
  test_model?: string
  setting?: string
  other?: string
  settings?: string
}

/** POST /api/channel — body is { mode: 'single', channel }. */
export function createChannel(input: ChannelInput): Promise<unknown> {
  return api.post<unknown>('/channel/', { mode: 'single', channel: input })
}

/** PUT /api/channel — body is the channel patch itself. */
export function updateChannel(input: ChannelInput): Promise<unknown> {
  return api.put<unknown>('/channel/', input)
}

export function deleteChannel(id: number): Promise<unknown> {
  return api.delete<unknown>(`/channel/${id}`)
}

export function updateChannelStatus(id: number, status: number): Promise<unknown> {
  return api.post<unknown>(`/channel/${id}/status`, { status })
}

export interface ChannelTestResult {
  success: boolean
  message: string
  time: number
}

/**
 * GET /api/channel/test/:id — 后端失败也返回 200 + success:false，
 * api 封装会把它当作 ApiError 抛出；这里把错误归一化为可展示的结果。
 */
export async function testChannel(
  id: number,
  model?: string,
): Promise<ChannelTestResult> {
  try {
    const raw = await api.get<{ time?: number }>(`/channel/test/${id}`, {
      query: model ? { model } : undefined,
    })
    return { success: true, message: '', time: Number(raw?.time ?? 0) }
  } catch (error) {
    return {
      success: false,
      message: error instanceof Error ? error.message : String(error),
      time: 0,
    }
  }
}

/** 模型池（标准在售模型名），与 models.tsx 使用同一接口。 */
export const channelPoolModelsQuery = () =>
  queryOptions({
    queryKey: ['marketplace', 'admin', 'pool', 'models', 'names'],
    queryFn: async (): Promise<string[]> => {
      const raw = await api.get<unknown>('/marketplace/admin/pool/models')
      const payload = record(raw)
      const items = Array.isArray(raw) ? raw : payload.items
      const names = new Set<string>()
      for (const item of Array.isArray(items) ? items : []) {
        const name = text(record(item).name)
        if (name) names.add(name)
      }
      return Array.from(names)
    },
    staleTime: 60_000,
  })
