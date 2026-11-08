import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { copy } from '@/copy'
import { systemStatusQuery, type AnnouncementInfo } from '@/features/auth/api'

const READ_KEY = 'mp:announcements.read'

/**
 * Stable identifier for one announcement: the backend id when present,
 * otherwise a content hash so an edited announcement counts as new again
 * (mirrors site/src/legacy/hooks/use-notifications.ts).
 */
export function announcementKey(item: AnnouncementInfo): string {
  if (item.id !== undefined && item.id !== null) return `id:${item.id}`
  const fingerprint = JSON.stringify([
    item.publishDate ?? '',
    (item.content ?? '').trim(),
    (item.title ?? '').trim(),
    item.link ?? '',
  ])
  let hash = 0
  for (let i = 0; i < fingerprint.length; i += 1) {
    hash = (hash << 5) - hash + fingerprint.charCodeAt(i)
    hash |= 0
  }
  return `hash:${hash.toString(36)}`
}

export function readAnnouncementKeys(): string[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = window.localStorage.getItem(READ_KEY)
    const parsed = raw ? (JSON.parse(raw) as unknown) : null
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : []
  } catch {
    return []
  }
}

/**
 * Dismissible banner for backend-configured system announcements
 * (`status.announcements`, admin-managed in 系统设置 → 公告). Read state is
 * persisted in localStorage; dismissing marks every currently-unread
 * announcement as read so the banner does not reappear on reload.
 */
export function AnnouncementBanner() {
  const status = useQuery(systemStatusQuery())
  const [read, setRead] = useState<string[]>(readAnnouncementKeys)

  if (status.data?.announcements_enabled !== true) return null
  const items = (status.data.announcements ?? []).filter(
    (item) => item && (item.content?.trim() || item.title?.trim()),
  )
  const unread = items.filter((item) => !read.includes(announcementKey(item)))
  if (unread.length === 0) return null

  const latest = unread[0]

  const dismiss = () => {
    const next = Array.from(new Set([...read, ...unread.map(announcementKey)]))
    setRead(next)
    try {
      window.localStorage.setItem(READ_KEY, JSON.stringify(next))
    } catch {
      // Storage may be unavailable (private mode); the banner just reappears.
    }
  }

  return (
    <div className="border-b border-brand/15 bg-brand-soft/60">
      <div className="mx-auto flex max-w-[1200px] items-start gap-3 px-5 py-2.5 sm:px-6">
        <span className="mt-0.5 shrink-0 rounded-full bg-white/70 px-2 py-0.5 text-caption font-medium text-brand-ink">
          {copy.announcement.badge}
        </span>
        <div className="min-w-0 flex-1 text-meta leading-5 text-ink-2">
          {latest.title ? (
            <span className="mr-1 font-medium text-ink">{latest.title}：</span>
          ) : null}
          <span className="line-clamp-2 break-words">{latest.content}</span>
          {latest.link ? (
            <a
              href={latest.link}
              target="_blank"
              rel="noreferrer"
              className="ml-1 text-brand hover:underline"
            >
              {copy.announcement.more}
            </a>
          ) : null}
        </div>
        <button
          type="button"
          onClick={dismiss}
          aria-label={copy.announcement.close}
          className="shrink-0 rounded p-1 text-ink-3 transition-colors hover:bg-white/60 hover:text-ink"
        >
          ×
        </button>
      </div>
    </div>
  )
}
