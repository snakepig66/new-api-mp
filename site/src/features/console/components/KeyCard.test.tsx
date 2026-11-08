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

import { KeyCard, maskKey } from './KeyCard'

const now = Math.floor(Date.now() / 1000)

describe('maskKey helper', () => {
  it('masks standard length keys and preserves already masked keys', () => {
    expect(maskKey('sk-L7zkAbCdEfGhIjKlMnOpQrStUvWxYz123yYi')).toBe('sk-L7zk***********3yYi')
    expect(maskKey('sk-short')).toBe('sk-short')
    expect(maskKey('sk-L7zk***********3yYi')).toBe('sk-L7zk***********3yYi')
  })
})

function createTestToken(overrides: Record<string, unknown> = {}) {
  return {
    id: 10,
    user_id: 1,
    key: 'sk-abcdef1234567890abcdef1234567890',
    status: 1,
    name: 'Production Key',
    created_time: now - 86400 * 10,
    accessed_time: now - 86400 * 5,
    expired_time: now + 86400 * 30,
    remain_quota: 50_000_000,
    unlimited_quota: false,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '',
    used_quota: 10_000_000,
    group: 'default',
    cross_group_retry: false,
    ...overrides,
  }
}

function createTestPref(overrides: Record<string, unknown> = {}) {
  return {
    token_id: 10,
    strategy: 'balanced' as const,
    model_scope: 'all' as const,
    price_cap_milli: 0,
    prefer_seller_ids: [],
    block_seller_ids: [],
    only_seller_id: 0,
    allow_fallback: false,
    rpm: 0,
    official_only: false,
    models: [],
    ...overrides,
  }
}

let apiCalls: { url: string; method?: string; body?: unknown }[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  const method = init?.method || 'GET'
  let body: unknown = null
  if (init?.body) {
    try {
      body = JSON.parse(String(init.body))
    } catch {
      body = String(init.body)
    }
  }
  apiCalls.push({ url, method, body })

  if (url === '/api/token/10/key' && method === 'POST') {
    return new Response(JSON.stringify({ success: true, key: 'sk-revealed-full-key' }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  }

  if (url === '/api/token/?status_only=1' && method === 'PUT') {
    return new Response(JSON.stringify({ success: true }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  }

  if (url === '/api/token/10' && method === 'DELETE') {
    return new Response(JSON.stringify({ success: true }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  }

  return new Response(JSON.stringify({ success: true, data: null }), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
})

beforeEach(() => {
  apiCalls = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderKeyCard(tokenOverrides = {}, prefOverrides = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const listRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => (
      <KeyCard
        item={{
          token: createTestToken(tokenOverrides),
          pref: createTestPref(prefOverrides),
        }}
      />
    ),
  })
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/keys/$id',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([listRoute, detailRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('KeyCard component', () => {
  it('renders key details: name, masked key, quota, status, and activity', async () => {
    renderKeyCard()

    expect(await screen.findByText('Production Key')).toBeTruthy()
    expect(screen.getByText('sk-abcd***********7890')).toBeTruthy()
    expect(screen.getByText('启用中')).toBeTruthy()
    expect(screen.getByText('default')).toBeTruthy()
    expect(screen.getByText(/已用/)).toBeTruthy()
    expect(screen.getByText(/最近使用 5 天前/)).toBeTruthy()
    expect(screen.getByText('全部模型')).toBeTruthy()
    expect(screen.getByText('均衡')).toBeTruthy()
  })

  it('toggles key status when switch is clicked', async () => {
    renderKeyCard()

    const switchBtn = await screen.findByRole('switch', { name: '停用 Key' })
    expect(switchBtn.getAttribute('aria-checked')).toBe('true')

    fireEvent.click(switchBtn)

    await waitFor(() => {
      const call = apiCalls.find((c) => c.url === '/api/token/?status_only=1')
      expect(call).toBeTruthy()
      expect(call?.method).toBe('PUT')
      expect(call?.body).toEqual({ id: 10, status: 2 })
    })
  })

  it('confirms and deletes a key', async () => {
    renderKeyCard()

    const deleteBtn = await screen.findByRole('button', { name: '删除 Key' })
    fireEvent.click(deleteBtn)

    expect(screen.getByText('确定要删除「Production Key」吗？删除后此 Key 将立即失效且无法恢复。')).toBeTruthy()

    const confirmDeleteBtn = screen.getByRole('button', { name: '删除' })
    fireEvent.click(confirmDeleteBtn)

    await waitFor(() => {
      const call = apiCalls.find((c) => c.url === '/api/token/10' && c.method === 'DELETE')
      expect(call).toBeTruthy()
    })
  })
})
