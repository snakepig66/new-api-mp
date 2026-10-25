import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { HomePage } from './index'

const squareItems = [
  {
    model: 'deepseek-chat',
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
    next_switch: '2030-01-01T10:00:00+08:00',
    listing_count: 2,
    generated_at: '2030-01-01T00:00:00+08:00',
  },
]

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/stats/public')) {
    return jsonResponse({ success: false, message: 'unavailable' }, 503)
  }
  if (url.startsWith('/api/marketplace/square/models/')) {
    const [item] = squareItems
    return jsonResponse({
      success: true,
      data: {
        ...item,
        multiplier_min_milli: 300,
        multiplier_max_milli: 480,
        official_prices: { offpeak: { ...item.prices.offpeak, version: 1 } },
      },
    })
  }
  if (url.startsWith('/api/marketplace/square/models')) {
    return jsonResponse({ success: true, data: squareItems })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderHome() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const homeRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models',
    component: HomePage,
  })
  const dummy = ['/register', '/console', '/seller', '/models/$id'].map((path) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: () => null }),
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([homeRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: ['/models'] }),
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe('home page', () => {
  it('hides the live stats strip when the stats request has no data', async () => {
    renderHome()

    expect(await screen.findByText('deepseek-chat')).toBeTruthy()
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining('/marketplace/stats/public'),
        expect.anything(),
      )
    })

    // §0: no placeholder — the strip simply does not render without data.
    expect(screen.queryByText('在售模型')).toBeNull()
    expect(screen.queryByText('实时数据接入后显示')).toBeNull()
    expect(screen.queryByText('示例')).toBeNull()
  })

  it('renders the hero CTA and the copyable curl example', async () => {
    renderHome()

    expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe(
      '同一个 Key，用官方模型更便宜',
    )
    expect(screen.getByRole('link', { name: '开始使用' })).toBeTruthy()
    expect(screen.getByRole('link', { name: '浏览模型' })).toBeTruthy()

    await waitFor(() => {
      expect(document.querySelector('pre code')?.textContent).toContain('curl "')
    })
  })
})
