/**
 * 运营后台「调用记录」数据层。
 *
 * 数据来自 new-api 自带的管理员日志接口：
 *  - GET /api/log/  全站日志（AdminAuth），支持 username / model_name /
 *    token_name / 时间范围过滤，返回 PageInfo
 *  - 额度单位与用户页一致：quota ÷ quota_per_unit = 人民币
 *
 * 页面上的「仅自己」只是把 username 固定成当前登录名，仍然是同一个接口，
 * 避免维护第二套查询逻辑。
 */

import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

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

export interface AdminLog {
  id: number
  user_id: number
  username: string
  token_name: string
  model_name: string
  quota: number
  prompt_tokens: number
  completion_tokens: number
  use_time: number
  is_stream: boolean
  channel: number
  channel_name: string
  group: string
  ip: string
  request_id: string
  created_at: number
}

export function normalizeAdminLog(raw: unknown): AdminLog {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'Id', 'ID')),
    user_id: int(pick(row, 'user_id', 'UserId')),
    username: text(pick(row, 'username', 'Username')),
    token_name: text(pick(row, 'token_name', 'TokenName')),
    model_name: text(pick(row, 'model_name', 'ModelName')),
    quota: int(pick(row, 'quota', 'Quota')),
    prompt_tokens: int(pick(row, 'prompt_tokens', 'PromptTokens')),
    completion_tokens: int(pick(row, 'completion_tokens', 'CompletionTokens')),
    use_time: int(pick(row, 'use_time', 'UseTime')),
    is_stream: Boolean(pick(row, 'is_stream', 'IsStream')),
    channel: int(pick(row, 'channel', 'Channel')),
    channel_name: text(pick(row, 'channel_name', 'ChannelName')),
    group: text(pick(row, 'group', 'Group')),
    ip: text(pick(row, 'ip', 'Ip')),
    request_id: text(pick(row, 'request_id', 'RequestId')),
    created_at: int(pick(row, 'created_at', 'CreatedAt')),
  }
}

export interface AdminLogPage {
  page: number
  page_size: number
  total: number
  items: AdminLog[]
}

export interface AdminLogFilters {
  username?: string
  model_name?: string
  token_name?: string
  start_timestamp?: number
  end_timestamp?: number
  page?: number
  page_size?: number
}

export const ADMIN_LOGS_PAGE_SIZE = 20

export const adminLogsQuery = (filters: AdminLogFilters = {}) => {
  const page = filters.page ?? 1
  const pageSize = filters.page_size ?? ADMIN_LOGS_PAGE_SIZE
  return queryOptions({
    queryKey: ['admin', 'logs', filters],
    queryFn: async (): Promise<AdminLogPage> => {
      const raw = await api.get<unknown>('/log/', {
        query: {
          p: page,
          page_size: pageSize,
          // 2 = 消费日志；充值/管理/系统日志不在「调用记录」里。
          type: 2,
          username: filters.username?.trim() || undefined,
          model_name: filters.model_name?.trim() || undefined,
          token_name: filters.token_name?.trim() || undefined,
          start_timestamp: filters.start_timestamp,
          end_timestamp: filters.end_timestamp,
        },
      })
      const envelope = record(raw)
      const items = Array.isArray(raw) ? raw : envelope.items
      return {
        page: int(envelope.page) || page,
        page_size: int(envelope.page_size) || pageSize,
        total: int(envelope.total),
        items: (Array.isArray(items) ? items : []).map(normalizeAdminLog),
      }
    },
    staleTime: 15_000,
  })
}
