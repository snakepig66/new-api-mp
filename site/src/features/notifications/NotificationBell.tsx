import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import {
  markAllNotificationsRead,
  markNotificationRead,
  notificationsQuery,
  type NotificationItem,
} from '@/features/notifications/api'

function relativeTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const diff = Date.now() - date.getTime()
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} 天前`
  return date.toLocaleDateString('zh-CN')
}

function kindLabel(kind: NotificationItem['kind']): string {
  return copy.notifications.kinds[kind] ?? copy.notifications.kinds.system
}

/**
 * The header bell. It shows the unread count and the eight newest rows; a
 * notification with an in-app link is marked read and navigated to on click.
 * The panel closes on outside click and on route change.
 */
export function NotificationBell() {
  const t = copy.notifications
  const queryClient = useQueryClient()
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  const notifications = useQuery({ ...notificationsQuery(1, 8), enabled: open })
  const items = notifications.data?.items ?? []
  const unread = notifications.data?.unread_count ?? 0

  useEffect(() => {
    setOpen(false)
  }, [pathname])

  useEffect(() => {
    if (!open) return
    function onPointerDown(event: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    return () => document.removeEventListener('mousedown', onPointerDown)
  }, [open])

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: ['marketplace', 'notifications'] })

  const markOne = useMutation({
    mutationFn: (id: number) => markNotificationRead(id),
    onSuccess: invalidate,
  })
  const markAll = useMutation({
    mutationFn: markAllNotificationsRead,
    onSuccess: invalidate,
  })

  function openItem(item: NotificationItem) {
    if (!item.read) markOne.mutate(item.id)
    setOpen(false)
  }

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        aria-label={t.aria}
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="relative grid h-10 w-10 place-items-center rounded-full text-ink-2 transition-colors hover:bg-bg-soft hover:text-ink"
      >
        <svg width="19" height="19" viewBox="0 0 20 20" fill="none" aria-hidden="true">
          <path
            d="M10 3a5 5 0 0 0-5 5v3.2L3.8 13.6a.6.6 0 0 0 .53.9h11.34a.6.6 0 0 0 .53-.9L15 11.2V8a5 5 0 0 0-5-5Z"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinejoin="round"
          />
          <path d="M8 16.5a2 2 0 0 0 4 0" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
        {unread > 0 ? (
          <span className="absolute right-1.5 top-1.5 grid h-4 min-w-4 place-items-center rounded-full bg-danger px-1 text-[11px] font-medium leading-none text-white">
            {unread > 99 ? '99+' : unread}
          </span>
        ) : null}
      </button>

      {open ? (
        <div className="absolute right-0 top-full z-40 mt-2 w-[320px] overflow-hidden rounded-md border border-line bg-bg">
          <div className="flex items-center justify-between border-b border-line px-4 py-3">
            <span className="text-[14px] font-medium text-ink">{t.title}</span>
            <button
              type="button"
              onClick={() => markAll.mutate()}
              disabled={markAll.isPending || unread === 0}
              className="text-[13px] text-brand transition-colors hover:underline disabled:cursor-default disabled:text-ink-3 disabled:no-underline"
            >
              {markAll.isPending ? t.marking : t.markAll}
            </button>
          </div>

          {notifications.isPending ? (
            <p className="px-4 py-6 text-center text-[13px] text-ink-3">{t.loading}</p>
          ) : notifications.isError ? (
            <div className="px-4 py-6 text-center">
              <p className="text-[13px] text-danger">{t.loadError}</p>
              <button
                type="button"
                onClick={() => void notifications.refetch()}
                className="mt-2 text-[13px] text-brand hover:underline"
              >
                {t.retry}
              </button>
            </div>
          ) : items.length === 0 ? (
            <p className="px-4 py-6 text-center text-[13px] text-ink-3">{t.empty}</p>
          ) : (
            <ul className="max-h-[360px] overflow-y-auto">
              {items.map((item) => {
                const body = (
                  <>
                    <span className="flex items-center gap-2">
                      <span className="text-[13px] text-ink-3">{kindLabel(item.kind)}</span>
                      <span className="text-[13px] text-ink-3">{relativeTime(item.created_at)}</span>
                      {!item.read ? (
                        <span
                          aria-hidden="true"
                          className="ml-auto h-1.5 w-1.5 rounded-full bg-brand"
                        />
                      ) : null}
                    </span>
                    <span
                      className={cn(
                        'mt-1 block text-[14px] leading-5',
                        item.read ? 'text-ink-2' : 'font-medium text-ink',
                      )}
                    >
                      {item.title}
                    </span>
                    {item.body ? (
                      <span className="mt-0.5 block text-[13px] leading-5 text-ink-3">
                        {item.body}
                      </span>
                    ) : null}
                  </>
                )
                return (
                  <li key={item.id} className="border-b border-line last:border-b-0">
                    {item.link ? (
                      <Link
                        to={item.link}
                        onClick={() => openItem(item)}
                        className="block px-4 py-3 transition-colors hover:bg-bg-soft"
                      >
                        {body}
                      </Link>
                    ) : (
                      <button
                        type="button"
                        onClick={() => openItem(item)}
                        className="block w-full px-4 py-3 text-left transition-colors hover:bg-bg-soft"
                      >
                        {body}
                      </button>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      ) : null}
    </div>
  )
}
