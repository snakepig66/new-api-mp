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

import { ModelsPage } from './index'

const squareItems = [
  {
    model: 'deepseek-chat',
    aliases: ['DeepSeek-V3'],
    origin_vendor: 'deepseek',
    vendor: 'deepseek',
    prices: {
      flat: {
        input_micro_per_mtok: 2_000_000,
        output_micro_per_mtok: 8_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    listing_count: 2,
    channel_count: 2,
    official_available: true,
    capabilities: ['text', 'tools', 'cache'],
    context_length: 131072,
    metrics: {
      status: 'available',
      success_rate_1h_milli: 1000,
      success_rate_24h_milli: 995,
      success_rate_7d_milli: 990,
      requests_24h: 321,
      latency_ms: 420,
      tps_permille: 51200,
      cache_hit_permille: 640,
      last_success_at: '2030-01-01T00:00:00+08:00',
      sampled_at: '2030-01-01T00:00:00+08:00',
    },
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
  {
    model: 'deepseek-r1',
    origin_vendor: 'deepseek',
    vendor: 'deepseek',
    prices: {
      flat: {
        input_micro_per_mtok: 5_000_000,
        output_micro_per_mtok: 20_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    listing_count: 1,
    channel_count: 1,
    official_available: true,
    metrics: {
      status: 'degraded',
      success_rate_1h_milli: 800,
      success_rate_24h_milli: 800,
      success_rate_7d_milli: 850,
      requests_24h: 12,
      latency_ms: 1800,
      tps_permille: 0,
      cache_hit_permille: -1,
      last_success_at: null,
      sampled_at: '2030-01-01T00:00:00+08:00',
    },
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
  {
    model: 'glm-4-plus',
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
    listing_count: 0,
    channel_count: 0,
    official_available: false,
    buyer_price_source: 'official',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
  {
    model: 'doubao-pro',
    origin_vendor: 'volcengine-ark',
    vendor: 'volcengine-ark',
    prices: {
      flat: {
        input_micro_per_mtok: 2_500_000,
        output_micro_per_mtok: 9_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    listing_count: 1,
    channel_count: 1,
    official_available: false,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00+08:00',
  },
]

const vendors = [
  {
    id: 'deepseek',
    display_name: 'DeepSeek',
    tier: 'official',
    kind: 'official',
    logo_url: '/vendors/deepseek.svg',
    protocols: { openai: '', anthropic: '' },
    models: [],
    passthrough_body: true,
  },
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
  {
    id: 'volcengine-ark',
    display_name: '火山方舟',
    tier: 'official',
    kind: 'official',
    logo_url: '/vendors/doubao.svg',
    protocols: { openai: '', anthropic: '' },
    models: [],
    passthrough_body: true,
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
    channels:
      model === 'deepseek-chat'
        ? [
            {
              channel_slug: 'deepseek',
              name: 'DeepSeek',
              kind: 'official',
              verified: true,
              coefficient_milli: 1000,
              differences: {},
              listing_count: 1,
              min_multiplier_milli: 300,
              max_multiplier_milli: 400,
              metrics: {
                status: 'available',
                success_rate_24h_milli: 998,
                requests_24h: 120,
                latency_ms: 380,
                last_success_at: null,
              },
            },
          ]
        : [],
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
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: vendors })
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

function renderPage(initialEntry = '/models') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const modelsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models',
    validateSearch: (search: Record<string, unknown>) => search,
    component: ModelsPage,
  })
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/models/$id',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([modelsRoute, detailRoute]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return { router }
}

describe('models page', () => {
  it('renders one row per standard model with buyer prices', async () => {
    renderPage()
    await screen.findByText('deepseek-chat')

    expect(screen.getByText('glm-4-plus')).toBeTruthy()
    const row = screen.getByText('deepseek-chat').closest('tr')
    expect(row?.textContent).toContain('¥2.00')
    expect(row?.textContent).toContain('出 ¥8.00')
  })

  it('marks a model with no supply as official price', async () => {
    renderPage()
    await screen.findByText('glm-4-plus')

    const row = screen.getByText('glm-4-plus').closest('tr')
    expect(row?.textContent).toContain('官方价')
  })

  it('filters by a vendor pill, syncs the URL, and shows the vendor model row', async () => {
    const { router } = renderPage()
    await screen.findByText('deepseek-chat')

    fireEvent.click(screen.getByRole('button', { name: /智谱/ }))

    await waitFor(() => {
      expect(screen.queryByText('deepseek-chat')).toBeNull()
    })
    expect(screen.getAllByText('glm-4-plus').length).toBeGreaterThan(0)
    expect(router.state.location.search).toEqual({ vendor: 'zhipu' })

    // The duplicate per-vendor mini cards are gone; the table row is the
    // single source of truth for the filtered model.
    const link = screen.getByRole('link', { name: 'glm-4-plus' })
    expect(link.getAttribute('href')).toBe('/models/glm-4-plus')
    expect(link.closest('tr')?.textContent).toContain('¥3.00')
    expect(screen.getByRole('button', { name: /智谱/ }).getAttribute('aria-pressed')).toBe(
      'true',
    )
  })

  it('restores vendor and search from the URL', async () => {
    renderPage('/models?vendor=zhipu&q=glm')
    await screen.findAllByText('glm-4-plus')

    expect(screen.getByRole('button', { name: /智谱/ }).getAttribute('aria-pressed')).toBe(
      'true',
    )
    expect(screen.queryByText('deepseek-chat')).toBeNull()
    expect(screen.queryByText('doubao-pro')).toBeNull()
    expect(screen.getAllByText('glm-4-plus').length).toBeGreaterThan(0)
  })

  it('expands a row in place with price comparison, channels and reliability', async () => {
    renderPage()
    await screen.findByText('deepseek-chat')

    fireEvent.click(screen.getByText('deepseek-chat').closest('tr')!)

    expect(await screen.findByText('价格对比（¥ / 百万 tokens）')).toBeTruthy()
    expect(screen.getByText('渠道最低价')).toBeTruthy()
    expect(screen.getByText('可靠性')).toBeTruthy()
    expect(screen.getAllByText('99.5%').length).toBeGreaterThan(0)
    expect(screen.getByText('99.8%')).toBeTruthy()
    expect(screen.getByText('64%')).toBeTruthy()
    expect(screen.getByText('131K')).toBeTruthy()
  })

  it('shows the 24h success column when request metrics exist', async () => {
    renderPage()
    await screen.findByText('deepseek-chat')
    expect(screen.getAllByText('24h 成功率').length).toBeGreaterThan(0)
  })

  it('filters to official supply from the URL', async () => {
    renderPage('/models?official=true')
    await screen.findByText('deepseek-chat')
    expect(screen.queryByText('glm-4-plus')).toBeNull()
    expect(screen.queryByText('doubao-pro')).toBeNull()
  })

  it('sorts by success rate from the URL', async () => {
    renderPage('/models?sort=success')
    await screen.findByText('deepseek-chat')
    const names = screen
      .getAllByRole('link', { name: /deepseek-(chat|r1)/ })
      .map((link) => link.textContent)
    expect(names[0]).toBe('deepseek-chat')
    expect(names[1]).toBe('deepseek-r1')
  })
})
