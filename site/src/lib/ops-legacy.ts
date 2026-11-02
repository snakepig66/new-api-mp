import type { MouseEvent } from 'react'

import { api } from '@/lib/api'

/**
 * Safely navigates an admin or root user to the legacy upstream dashboard.
 * Requests a short-lived (30s) single-use ticket via POST /api/ops-legacy/ticket,
 * then navigates to /ops-legacy?ticket=... where the backend sets an HttpOnly
 * session cookie and redirects to /ops-legacy (stripping the ticket).
 */
export async function navigateToOpsLegacy(e?: MouseEvent): Promise<void> {
  if (e) {
    e.preventDefault()
  }
  try {
    const data = await api.post<{ ticket: string }>('/ops-legacy/ticket')
    if (data?.ticket) {
      window.location.href = `/ops-legacy?ticket=${encodeURIComponent(data.ticket)}`
      return
    }
  } catch {
    // If ticket generation fails (e.g. unauthenticated or network error),
    // fall back to direct navigation (backend will redirect to /login).
  }
  window.location.href = '/ops-legacy'
}
