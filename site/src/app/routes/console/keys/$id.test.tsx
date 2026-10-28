import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { KeyDetailPage } from './$id'

const token = {
  id: 5,
  user_id: 1,
  key: 'sk-masked-5',
  status: 1,
  name: 'official-key',
  created_time: 1,
  accessed_time: 1,
  expired_time: -1,
  remain_quota: 1_000_000,
  unlimited_quota: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: null,
  used_quota: 0,
  group: '',
  cross_group_retry: false,
}

const squareModels = [
  {
    model: 'glm-5.3',
    origin_vendor: 'zhipu',
    vendor: 'zhipu',
    prices: {
      flat: {
        input_micro_per_mtok: 8_000_000,
        output_micro_per_mtok: 28_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    listing_count: 1,
    channel_count: 1,
    multiplier_min_milli: 900,
    official_available: true,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00Z',
  },
  {
    model: 'no-official',
    origin_vendor: 'zhipu',
    vendor: 'zhipu',
    prices: {
      flat: {
        input_micro_per_mtok: 1_000_000,
        output_micro_per_mtok: 4_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    listing_count: 1,
    channel_count: 1,
    multiplier_min_milli: 500,
    official_available: false,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00Z',
  },
]

let officialOnly = true

const pref = () => ({
  token_id: 5,
  strategy: 'balanced',
  model_scope: 'selected',
  price_cap_milli: 0,
  prefer_seller_ids: [],
  block_seller_ids: [],
  only_seller_id: 0,
  allow_fallback: false,
  rpm: 0,
  official_only: officialOnly,
  models: [
    { model: 'glm-5.3', price_cap_milli: 0, added_at: 1 },
    { model: 'no-official', price_cap_milli: 0, added_at: 1 },
  ],
})

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/token/5') return jsonResponse({ success: true, data: token })
  if (url.startsWith('/api/marketplace/key/pref')) {
    return jsonResponse({ success: true, data: pref() })
  }
  if (url.startsWith('/api/marketplace/key/models')) {
    return jsonResponse({ success: true, data: { models: pref().models } })
  }
  if (url.startsWith('/api/marketplace/square/models')) {
    return jsonResponse({ success: true, data: squareModels })
  }
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({
      success: true,
      data: [{ id: 'zhipu', display_name: '智谱', tier: 'official', kind: 'official' }],
    })
  }
  if (url.startsWith('/api/user/ip')) {
    return jsonResponse({ success: true, data: { ip: '203.0.113.9' } })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  officialOnly = true
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
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/keys/$id',
    component: KeyDetailPage,
  })
  const listRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/keys',
    component: () => null,
  })
  const modelsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([detailRoute, listRoute, modelsRoute]),
    history: createMemoryHistory({ initialEntries: ['/console/keys/5'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('key detail official-only notice', () => {
  it('lists selected models that no official channel can serve', async () => {
    renderPage()

    expect(await screen.findByText('当前设置下调不了的模型')).toBeTruthy()
    expect(screen.getAllByText('no-official').length).toBeGreaterThan(0)
    const link = screen.getByRole('link', { name: '去模型广场看看' })
    expect(link.getAttribute('href')).toBe('/models')
    expect(screen.getByText('你当前的 IP：203.0.113.9')).toBeTruthy()
  })

  it('shows nothing when the key does not use official-only routing', async () => {
    officialOnly = false
    renderPage()

    expect(await screen.findByText('official-key')).toBeTruthy()
    expect(screen.queryByText('当前设置下调不了的模型')).toBeNull()
  })
})
