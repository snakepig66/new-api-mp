/**
 * 通知中心 API shapes (P8).
 *
 * The bell reads the logged-in user's notifications; the backend scopes every
 * query by the session user id and answers 401 without one. Reads and
 * mark-read mutate only the caller's rows.
 */

import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

export type NotificationKind =
  | 'review'
  | 'listing_degraded'
  | 'balance_low'
  | 'topup'
  | 'withdrawal'
  | 'key_expiring'
  | 'system'

export interface NotificationItem {
  id: number
  kind: NotificationKind
  title: string
  body: string
  link: string
  read: boolean
  read_at: string | null
  created_at: string
}

export interface NotificationPage {
  page: number
  page_size: number
  total: number
  unread_count: number
  items: NotificationItem[]
}

export const notificationsQuery = (page = 1, pageSize = 20) =>
  queryOptions({
    queryKey: ['marketplace', 'notifications', page, pageSize],
    queryFn: () =>
      api.get<NotificationPage>('/marketplace/notifications', {
        query: { page, page_size: pageSize },
      }),
    staleTime: 30_000,
    refetchInterval: 60_000,
  })

export function markNotificationRead(id: number): Promise<{ unread_count: number }> {
  return api.post<{ unread_count: number }>(`/marketplace/notifications/${id}/read`)
}

export function markAllNotificationsRead(): Promise<{ updated: number }> {
  return api.post<{ updated: number }>('/marketplace/notifications/read-all')
}
