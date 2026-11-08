import { describe, expect, it } from 'vitest'

import { announcementKey, readAnnouncementKeys } from './AnnouncementBanner'

describe('announcement read state', () => {
  it('prefers the backend id as the stable key', () => {
    expect(announcementKey({ id: 7, content: 'maintenance' })).toBe('id:7')
    expect(announcementKey({ id: 'a-1', title: 't' })).toBe('id:a-1')
  })

  it('falls back to a content hash and changes when the content changes', () => {
    const a = announcementKey({ content: 'first', publishDate: '2026-11-01T00:00:00Z' })
    const b = announcementKey({ content: 'second', publishDate: '2026-11-01T00:00:00Z' })
    expect(a).toMatch(/^hash:/)
    expect(a).not.toBe(b)
  })

  it('reads persisted read keys', () => {
    window.localStorage.setItem('mp:announcements.read', JSON.stringify(['id:1', 'id:2']))
    expect(readAnnouncementKeys()).toEqual(['id:1', 'id:2'])
    window.localStorage.removeItem('mp:announcements.read')
  })

  it('ignores corrupt storage', () => {
    window.localStorage.setItem('mp:announcements.read', '{not json')
    expect(readAnnouncementKeys()).toEqual([])
    window.localStorage.removeItem('mp:announcements.read')
  })
})
