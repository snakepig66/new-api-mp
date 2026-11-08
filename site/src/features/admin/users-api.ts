/**
 * 站点原生「用户与充值」后台 API。
 *
 * 直接复用 new-api 已有的管理接口（挂在 /api/user 下，AdminAuth）：
 *  - GET  /api/user            分页用户列表（PageInfo）
 *  - GET  /api/user/search     关键字搜索（keyword / role / status + 分页）
 *  - GET  /api/user/:id        单个用户
 *  - PUT  /api/user            更新用户（这里只用于改显示名/邮箱等基础字段）
 *  - POST /api/user/manage     启停、升降级（action: enable/disable/promote/demote）
 *  - POST /api/user/manage     调整额度（action: add_quota, mode: add/subtract/override）
 *  - GET  /api/user/topup      全平台充值记录（分页）
 *  - POST /api/user/topup/complete  补单 { trade_no }
 *
 * 额度单位为「额度」，生产 QUOTA_PER_UNIT=1,000,000 且等于 ¥1。
 * 金额展示统一走 lib/money.formatYuan（微元输入），由页面用换算后的微元调用。
 */

import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

export const DEFAULT_QUOTA_PER_UNIT = 1_000_000

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

export interface AdminUser {
  id: number
  username: string
  display_name: string
  email: string
  role: number
  status: number
  quota: number
  used_quota: number
  request_count: number
  group: string
  created_at: number
  /** Unix 秒；0 表示从未登录（model.User.last_login_at，登录时更新）。 */
  last_login_at: number
  gift_quota: number
  deleted_at: string | null
}

export function normalizeAdminUser(raw: unknown): AdminUser {
  const row = record(raw)
  const deleted = pick(row, 'deleted_at', 'DeletedAt')
  let deletedAt: string | null = null
  if (deleted && typeof deleted === 'object' && 'Time' in deleted) {
    const time = (deleted as UnknownRecord).Time
    deletedAt = time ? text(time) : null
  } else if (deleted) {
    deletedAt = text(deleted) || null
  }
  return {
    id: int(pick(row, 'id', 'Id', 'ID')),
    username: text(pick(row, 'username', 'Username')),
    display_name: text(pick(row, 'display_name', 'DisplayName')),
    email: text(pick(row, 'email', 'Email')),
    role: int(pick(row, 'role', 'Role')),
    status: int(pick(row, 'status', 'Status')),
    quota: int(pick(row, 'quota', 'Quota')),
    used_quota: int(pick(row, 'used_quota', 'UsedQuota')),
    request_count: int(pick(row, 'request_count', 'RequestCount')),
    group: text(pick(row, 'group', 'Group')),
    created_at: int(pick(row, 'created_at', 'CreatedAt')),
    last_login_at: int(pick(row, 'last_login_at', 'LastLoginAt')),
    gift_quota: int(pick(row, 'gift_quota', 'GiftQuota')),
    deleted_at: deletedAt,
  }
}

export interface AdminUserPage {
  page: number
  page_size: number
  total: number
  items: AdminUser[]
}

function normalizePage(raw: unknown): AdminUserPage {
  const envelope = record(raw)
  const items = Array.isArray(raw) ? raw : envelope.items
  return {
    page: int(envelope.page) || 1,
    page_size: int(envelope.page_size) || 20,
    total: int(envelope.total),
    items: (Array.isArray(items) ? items : []).map(normalizeAdminUser),
  }
}

export interface AdminUserFilters {
  keyword?: string
  role?: number
  status?: number
  page?: number
  page_size?: number
}

export const adminUsersQuery = (filters: AdminUserFilters = {}) => {
  const page = filters.page ?? 1
  const pageSize = filters.page_size ?? 20
  const keyword = filters.keyword?.trim() ?? ''
  return queryOptions({
    queryKey: [
      'admin',
      'users',
      keyword,
      filters.role ?? 'all',
      filters.status ?? 'all',
      page,
      pageSize,
    ],
    queryFn: async (): Promise<AdminUserPage> => {
      const query = { p: page, page_size: pageSize }
      const raw = keyword
        ? await api.get<unknown>('/user/search', {
            query: {
              ...query,
              keyword,
              role: filters.role,
              status: filters.status,
            },
          })
        : // Trailing slash is required: the backend registers the admin list
          // as GET /api/user/ (group root). "/api/user" (no slash) 404s.
          await api.get<unknown>('/user/', { query })
      return normalizePage(raw)
    },
    staleTime: 15_000,
  })
}

export function getAdminUser(id: number): Promise<AdminUser> {
  return api.get<unknown>(`/user/${id}`).then(normalizeAdminUser)
}

// ---------------------------------------------------------------------------
// 额度调整
// ---------------------------------------------------------------------------

export type QuotaAdjustMode = 'add' | 'subtract' | 'override'

/**
 * 调整额度。金额在页面上是人民币，传到后端前换算成整数额度。
 * 注意：后端调整的是 Quota 总余额，不是增量上限，负值会被拒绝。
 */
export function adjustUserQuota(
  id: number,
  mode: QuotaAdjustMode,
  quota: number,
): Promise<unknown> {
  return api.post<unknown>('/user/manage', {
    id,
    action: 'add_quota',
    mode,
    value: Math.round(quota),
  })
}

// ---------------------------------------------------------------------------
// 账号状态 / 角色
// ---------------------------------------------------------------------------

export type UserManageAction = 'enable' | 'disable' | 'promote' | 'demote'

export function manageAdminUser(id: number, action: UserManageAction): Promise<unknown> {
  return api.post<unknown>('/user/manage', { id, action })
}

// ---------------------------------------------------------------------------
// 充值记录
// ---------------------------------------------------------------------------

export interface AdminTopUp {
  id: number
  user_id: number
  amount: number
  money: number
  trade_no: string
  payment_method: string
  payment_provider: string
  create_time: number
  complete_time: number
  status: string
}

export function normalizeAdminTopUp(raw: unknown): AdminTopUp {
  const row = record(raw)
  return {
    id: int(pick(row, 'id', 'Id', 'ID')),
    user_id: int(pick(row, 'user_id', 'UserId', 'UserID')),
    amount: int(pick(row, 'amount', 'Amount')),
    money: Number(pick(row, 'money', 'Money') ?? 0) || 0,
    trade_no: text(pick(row, 'trade_no', 'TradeNo')),
    payment_method: text(pick(row, 'payment_method', 'PaymentMethod')),
    payment_provider: text(pick(row, 'payment_provider', 'PaymentProvider')),
    create_time: int(pick(row, 'create_time', 'CreateTime')),
    complete_time: int(pick(row, 'complete_time', 'CompleteTime')),
    status: text(pick(row, 'status', 'Status')),
  }
}

export interface AdminTopUpPage {
  page: number
  page_size: number
  total: number
  items: AdminTopUp[]
}

export const adminTopUpsQuery = (page = 1, pageSize = 20, keyword = '') => {
  const trimmed = keyword.trim()
  return queryOptions({
    queryKey: ['admin', 'topups', page, pageSize, trimmed],
    queryFn: async (): Promise<AdminTopUpPage> => {
      const raw = await api.get<unknown>('/user/topup', {
        query: { p: page, page_size: pageSize, keyword: trimmed || undefined },
      })
      const envelope = record(raw)
      const items = Array.isArray(raw) ? raw : envelope.items
      return {
        page: int(envelope.page) || page,
        page_size: int(envelope.page_size) || pageSize,
        total: int(envelope.total),
        items: (Array.isArray(items) ? items : []).map(normalizeAdminTopUp),
      }
    },
    staleTime: 15_000,
  })
}

/** 补单：把 pending 订单标记为已支付并入账。 */
export function completeAdminTopUp(tradeNo: string): Promise<unknown> {
  return api.post<unknown>('/user/topup/complete', { trade_no: tradeNo })
}

/** 从 /api/status 读 quota_per_unit，读不到用 1e6。 */
export async function fetchQuotaPerUnit(): Promise<number> {
  try {
    const raw = await api.get<unknown>('/status')
    const value = int(record(raw).quota_per_unit)
    return value > 0 ? value : DEFAULT_QUOTA_PER_UNIT
  } catch {
    return DEFAULT_QUOTA_PER_UNIT
  }
}

/** 后端调度的分页大小在 GetPageQuery 中回填，保持与页面一致。 */
export const ADMIN_USERS_PAGE_SIZE = 20
