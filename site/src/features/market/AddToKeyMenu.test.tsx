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

import { clearSession, applyAuthBundle } from '@/lib/session'
import { ToastProvider } from '@/ui'

import { AddToKeyMenu } from './AddToKeyMenu'

const squareModels = [
  {
    model: 'glm-4-plus',
    aliases: ['GLM-4-Plus'],
    origin_vendor: 'zhipu',
    vendor: 'zhipu',
    prices: {
      flat: {
        input_micro_per_mtok: 3_000_000,
        output_micro_per_mtok: 12_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    listing_count: 1,
    channel_count: 1,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00Z',
  },
]

const vendors = [
  {
    id: 'zhipu',
    display_name: '智谱 BigModel',
    tier: 'official',
    kind: 'official',
    logo_url: '/vendors/zhipu.svg',
    protocols: { openai: '', anthropic: '' },
    models: [],
    passthrough_body: true,
  },
]

const tokens = [
  token(1, 'alpha'),
  token(2, 'beta'),
  token(3, 'gamma-all'),
  token(4, 'delta-added'),
]

function token(id: number, name: string) {
  return {
    id,
    user_id: 1,
    key: 'sk-masked',
    status: 1,
    name,
    created_time: 1,
    accessed_time: 1,
    expired_time: -1,
    remain_quota: 0,
    unlimited_quota: true,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: null,
    used_quota: 0,
    group: '',
    cross_group_retry: false,
  }
}

const prefs: Record<number, unknown> = {
  1: pref(1, 'selected', []),
  2: pref(2, 'selected', []),
  3: pref(3, 'all', []),
  4: pref(4, 'selected', [{ model: 'glm-4-plus', price_cap_milli: 0 }]),
}

function pref(tokenId: number, scope: 'all' | 'selected', models: unknown[]) {
  return {
    token_id: tokenId,
    strategy: 'balanced',
    model_scope: scope,
    price_cap_milli: 0,
    prefer_seller_ids: [],
    block_seller_ids: [],
    only_seller_id: 0,
    allow_fallback: false,
    rpm: 0,
    official_only: false,
    models,
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

let addedBodies: Record<string, unknown>[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.includes('/api/user/auth/refresh')) {
    return jsonResponse({ success: false })
  }
  if (url.startsWith('/api/token/')) {
    return jsonResponse({
      success: true,
      data: { page: 1, page_size: 100, total: tokens.length, items: tokens },
    })
  }
  if (url.startsWith('/api/marketplace/key/pref')) {
    const tokenId = Number(new URL(url, 'http://localhost').searchParams.get('token_id'))
    const found = prefs[tokenId]
    if (!found) {
      return jsonResponse(
        { success: false, code: 'key_pref_not_found', message: 'not found' },
        404,
      )
    }
    return jsonResponse({ success: true, data: found })
  }
  if (url.startsWith('/api/marketplace/key/models') && init?.method === 'POST') {
    const body = JSON.parse(String(init.body)) as Record<string, unknown>
    addedBodies.push(body)
    return jsonResponse({ success: true, data: { models: [] } })
  }
  if (url.startsWith('/api/marketplace/square/models')) {
    return jsonResponse({ success: true, data: squareModels })
  }
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: vendors })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  addedBodies = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
  clearSession()
})

afterEach(() => {
  cleanup()
  clearSession()
  vi.unstubAllGlobals()
})

function renderMenu(initialEntry = '/models?vendor=zhipu') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const modelsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models',
    component: () => <AddToKeyMenu model="glm-4-plus" />,
  })
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    validateSearch: (search: Record<string, unknown>) => ({
      redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
    }),
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([modelsRoute, loginRoute]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })

  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return router
}

function loginAsBuyer() {
  applyAuthBundle({
    access_token: 'test-access-token',
    user: { id: 1, username: 'buyer', role: 1 },
  })
}

describe('AddToKeyMenu', () => {
  it('sends a logged-out click to login with the current URL as redirect', async () => {
    const router = renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/login')
    })
    expect(router.state.location.search).toEqual({
      redirect: '/models?vendor=zhipu',
    })
  })

  it('adds the model to the selected keys only', async () => {
    loginAsBuyer()
    renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))

    const dialog = await screen.findByRole('dialog')
    await screen.findByRole('checkbox', { name: /alpha/ })

    const allday = screen.getByRole('checkbox', { name: /gamma-all/ }) as HTMLInputElement
    expect(allday.checked).toBe(true)
    expect(allday.disabled).toBe(true)
    const added = screen.getByRole('checkbox', { name: /delta-added/ }) as HTMLInputElement
    expect(added.checked).toBe(true)
    expect(added.disabled).toBe(true)

    fireEvent.click(screen.getByRole('checkbox', { name: /alpha/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /beta/ }))
    fireEvent.click(screen.getByRole('button', { name: '加入（2）' }))

    await waitFor(() => expect(addedBodies).toHaveLength(2))
    expect(addedBodies.map((body) => body.token_id).sort()).toEqual([1, 2])
    expect(addedBodies.every((body) => body.model === 'glm-4-plus')).toBe(true)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(dialog).toBeTruthy()
  })

  it('opens the create-key dialog with the model preselected', async () => {
    loginAsBuyer()
    renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))

    await screen.findByRole('dialog')
    fireEvent.click(await screen.findByRole('button', { name: '新建 Key' }))

    expect(await screen.findByText('新建 API Key')).toBeTruthy()
    expect((await screen.findAllByText('已选 1 个')).length).toBeGreaterThan(0)
    expect(
      screen.getByRole('button', { name: '仅指定模型' }).getAttribute('aria-pressed'),
    ).toBe('true')
  })
})
