import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { KeysPage } from './index'

const now = Math.floor(Date.now() / 1000)

function token(overrides: Record<string, unknown>) {
  return {
    id: 1,
    user_id: 1,
    key: 'sk-masked-1',
    status: 1,
    name: 'soon-key',
    created_time: now - 86400,
    accessed_time: now,
    expired_time: now + 2 * 86400,
    remain_quota: 1_000_000,
    unlimited_quota: false,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '203.0.113.10',
    used_quota: 0,
    group: '',
    cross_group_retry: false,
    ...overrides,
  }
}

const tokens = [
  token({ id: 1, name: 'soon-key', expired_time: now + 2 * 86400 }),
  token({ id: 2, name: 'dead-key', status: 3, expired_time: now - 86400 }),
  token({ id: 3, name: 'later-key', expired_time: now + 10 * 86400 }),
]

const pref = (tokenId: number) => ({
  token_id: tokenId,
  strategy: 'balanced',
  model_scope: 'all',
  price_cap_milli: 0,
  prefer_seller_ids: [],
  block_seller_ids: [],
  only_seller_id: 0,
  allow_fallback: false,
  rpm: 0,
  official_only: false,
  models: [],
})

let extendedBody: Record<string, unknown> | null = null

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.startsWith('/api/token/?') || url === '/api/token/') {
    return jsonResponse({
      success: true,
      data: { page: 1, page_size: 100, total: tokens.length, items: tokens },
    })
  }
  if (url.startsWith('/api/marketplace/key/pref')) {
    const tokenId = Number(new URL(url, 'http://localhost').searchParams.get('token_id'))
    return jsonResponse({ success: true, data: pref(tokenId) })
  }
  if (url.startsWith('/api/marketplace/key/token') && init?.method === 'PUT') {
    extendedBody = JSON.parse(String(init.body)) as Record<string, unknown>
    return jsonResponse({
      success: true,
      data: {
        expired_time: extendedBody.expired_time,
        allow_ips: extendedBody.allow_ips,
      },
    })
  }
  if (url.startsWith('/api/marketplace/key') && init?.method === 'POST') {
    return jsonResponse({
      success: true,
      data: { id: 9, name: 'Key 9', key: 'sk-new', model_scope: 'all', models: [], strategy: 'balanced' },
    })
  }
  if (url.startsWith('/api/marketplace/square/models')) {
    return jsonResponse({ success: true, data: [] })
  }
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: [] })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  extendedBody = null
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const listRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/keys',
    component: KeysPage,
  })
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/keys/$id',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([listRoute, detailRoute]),
    history: createMemoryHistory({ initialEntries: ['/console/keys'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('keys page expiry UX', () => {
  it('shows one banner for the soonest key and greys expired rows', async () => {
    renderPage()

    expect(await screen.findByText('soon-key')).toBeTruthy()
    expect(screen.getAllByText('Key 即将到期')).toHaveLength(1)
    const bannerLink = screen.getByRole('link', { name: '去延长' })
    expect(bannerLink.getAttribute('href')).toBe('/console/keys/1')
    expect(screen.getByText(/later-key/)).toBeTruthy()

    const deadRow = screen.getByText('dead-key').closest('tr')
    expect(deadRow?.className).toContain('opacity-60')
    const liveRow = screen.getByText('soon-key').closest('tr')
    expect(liveRow?.className).not.toContain('opacity-60')

    // Only the expired row offers extending.
    expect(screen.getAllByRole('button', { name: '延长' })).toHaveLength(1)
  })

  it('extends an expired key with a new expiry and reuses the IP whitelist', async () => {
    renderPage()
    expect(await screen.findByText('dead-key')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '延长' }))
    const future = new Date(Date.now() + 5 * 86400 * 1000)
    const pad = (value: number) => String(value).padStart(2, '0')
    const localValue = `${future.getFullYear()}-${pad(future.getMonth() + 1)}-${pad(future.getDate())}T${pad(future.getHours())}:${pad(future.getMinutes())}`
    fireEvent.change(screen.getByLabelText('新的过期时间'), {
      target: { value: localValue },
    })

    fireEvent.click(screen.getByRole('button', { name: '保存新有效期' }))

    await waitFor(() => expect(extendedBody).not.toBeNull())
    expect(extendedBody?.token_id).toBe(2)
    expect(extendedBody?.expired_time).toBe(Math.floor(new Date(localValue).getTime() / 1000))
    expect(extendedBody?.allow_ips).toBe('203.0.113.10')
    expect(await screen.findByText('有效期已延长')).toBeTruthy()
  })
})
