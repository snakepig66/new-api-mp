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

import { ModelsPage } from './index'

const squareItems = [
  {
    model: 'deepseek-chat',
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
      peak: {
        input_micro_per_mtok: 4_000_000,
        output_micro_per_mtok: 16_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: '2030-01-01T10:00:00+08:00',
    listing_count: 2,
    channel_count: 2,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
  {
    model: 'deepseek-r1',
    origin_vendor: 'deepseek',
    vendor: 'deepseek',
    tier: 'peak',
    tier_label: '高峰',
    prices: {
      peak: {
        input_micro_per_mtok: 5_000_000,
        output_micro_per_mtok: 20_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: '2030-01-01T18:00:00+08:00',
    listing_count: 1,
    channel_count: 1,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
  {
    model: 'glm-4-plus',
    origin_vendor: 'zhipu',
    vendor: 'zhipu',
    tier: 'flat',
    tier_label: '平价',
    prices: {
      flat: {
        input_micro_per_mtok: 3_000_000,
        output_micro_per_mtok: 12_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: null,
    listing_count: 0,
    channel_count: 0,
    buyer_price_source: 'official',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
]

function detailFor(model: string) {
  const item = squareItems.find((row) => row.model === model)!
  const officialPrices = Object.fromEntries(
    Object.entries(item.prices).map(([tier, price]) => [tier, { ...price, version: 1 }]),
  )
  return {
    ...item,
    multiplier_min_milli: model === 'deepseek-r1' ? 520 : model === 'glm-4-plus' ? 680 : 300,
    multiplier_max_milli: model === 'deepseek-r1' ? 650 : 480,
    official_prices: officialPrices,
    channels: [],
  }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/square/models/')) {
    const model = decodeURIComponent(url.split('/').pop() ?? '')
    return jsonResponse({ success: true, data: detailFor(model) })
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

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const modelsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models',
    component: ModelsPage,
  })
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models/$id',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([modelsRoute, detailRoute]),
    history: createMemoryHistory({ initialEntries: ['/models'] }),
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe('models page', () => {
  it('renders live rows and enriches them with official prices and multipliers', async () => {
    renderPage()

    expect(await screen.findByText('deepseek-chat')).toBeTruthy()
    expect(screen.getByText('deepseek-r1')).toBeTruthy()
    expect(screen.getByText('glm-4-plus')).toBeTruthy()

    // Detail enrichment: the list endpoint has no multiplier/official price.
    await waitFor(() => {
      expect(screen.getAllByText('0.30x').length).toBeGreaterThan(0)
    })
    expect(screen.getAllByText('0.52x').length).toBeGreaterThan(0)

    // The whole row is one link to the detail page.
    const link = screen.getByRole('link', { name: /deepseek-chat/ })
    expect(link.getAttribute('href')).toBe('/models/deepseek-chat')
  })

  it('filters by model name from the search field', async () => {
    renderPage()
    await screen.findByText('deepseek-chat')

    fireEvent.change(screen.getByLabelText('按模型名称搜索'), {
      target: { value: 'r1' },
    })

    await waitFor(() => {
      expect(screen.queryByText('deepseek-chat')).toBeNull()
    })
    expect(screen.getByText('deepseek-r1')).toBeTruthy()
    expect(screen.queryByText('glm-4-plus')).toBeNull()
  })

  it('marks a model with no supply as official price', async () => {
    renderPage()
    await screen.findByText('glm-4-plus')

    const row = screen.getByText('glm-4-plus').closest('tr')
    expect(row?.textContent).toContain('官方价')
  })

  it('filters by a vendor pill generated from the data', async () => {
    renderPage()
    await screen.findByText('deepseek-chat')

    fireEvent.click(screen.getByRole('button', { name: '智谱' }))

    await waitFor(() => {
      expect(screen.queryByText('deepseek-chat')).toBeNull()
    })
    expect(screen.getByText('glm-4-plus')).toBeTruthy()
  })
})
