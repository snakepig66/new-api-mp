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

import { ModelDetailPage } from './$id'

const officialPrice = {
  input_micro_per_mtok: 4_000_000,
  output_micro_per_mtok: 16_000_000,
  cache_read_micro_per_mtok: 0,
  cache_write_micro_per_mtok: 0,
  version: 1,
}

function detail(overrides: Record<string, unknown> = {}) {
  return {
    model: 'deepseek-flash',
    origin_vendor: 'deepseek',
    vendor: 'deepseek',
    tier: 'offpeak',
    tier_label: '空闲',
    prices: {
      offpeak: {
        input_micro_per_mtok: 2_000_000,
        output_micro_per_mtok: 8_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: null,
    listing_count: 3,
    channel_count: 2,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00+08:00',
    multiplier_min_milli: 300,
    multiplier_max_milli: 600,
    official_prices: { offpeak: officialPrice },
    channels: [
      {
        channel_slug: 'deepseek',
        name: 'DeepSeek',
        kind: 'official',
        verified: true,
        coefficient_milli: 1000,
        differences: {},
        listing_count: 2,
        min_multiplier_milli: 300,
        max_multiplier_milli: 600,
      },
      {
        channel_slug: 'volcengine-ark-coding-plan',
        name: '火山方舟 Coding Plan',
        kind: 'plan',
        verified: false,
        coefficient_milli: 800,
        differences: { context_length: 128000, vision: false },
        listing_count: 1,
        min_multiplier_milli: 500,
        max_multiplier_milli: 500,
      },
    ],
    ...overrides,
  }
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/square/models/')) {
    return new Response(JSON.stringify({ success: true, data: detail() }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  }
  return new Response(JSON.stringify({ success: false, message: 'not found' }), {
    status: 404,
    headers: { 'content-type': 'application/json' },
  })
})

beforeEach(() => {
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
    path: '/models/$id',
    component: ModelDetailPage,
  })
  const dummy = ['/models', '/console', '/login', '/docs', '/seller'].map((path) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: () => null }),
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([detailRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: ['/models/deepseek-flash'] }),
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('model detail channels', () => {
  it('renders sellable channels grouped by kind with badges and differences', async () => {
    renderPage()

    expect(await screen.findByText('渠道挂单')).toBeTruthy()
    expect(screen.getAllByText('DeepSeek').length).toBeGreaterThan(0)
    expect(screen.getByText('火山方舟 Coding Plan')).toBeTruthy()
    expect(screen.getByText('官方')).toBeTruthy()
    expect(screen.getByText('套餐')).toBeTruthy()
    expect(screen.getByText('已验真')).toBeTruthy()
    expect(screen.getByText('未验真')).toBeTruthy()
    expect(screen.getByText('上下文 128K')).toBeTruthy()
    expect(screen.getByText('无图片')).toBeTruthy()
    expect(screen.getByText('与官方一致')).toBeTruthy()
    expect(screen.getByText('2 个渠道 · 3 个挂单')).toBeTruthy()
  })

  it('shows the official price and an empty channel list without supply', async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('/api/marketplace/square/models/')) {
        return new Response(JSON.stringify({ success: false, message: 'not found' }), {
          status: 404,
          headers: { 'content-type': 'application/json' },
        })
      }
      return new Response(
        JSON.stringify({
          success: true,
          data: detail({
            listing_count: 0,
            channel_count: 0,
            buyer_price_source: 'official',
            multiplier_min_milli: 0,
            multiplier_max_milli: 0,
            channels: [],
          }),
        }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )
    })

    renderPage()

    expect(await screen.findByText('渠道挂单')).toBeTruthy()
    expect(screen.getByText('暂时没有渠道挂单')).toBeTruthy()
    expect(screen.getAllByText('官方价').length).toBeGreaterThan(0)
    expect(screen.getByText('0 个渠道 · 0 个挂单')).toBeTruthy()
  })
})
