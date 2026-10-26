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

import { SellerListingsPage } from './index'

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const listings = [
  {
    id: 1,
    vendor: 'deepseek',
    models: ['deepseek-flash'],
    multiplier_milli: 500,
    concurrency_limit: 10,
    rpm_limit: 60,
    daily_cap_micro: 0,
    hours: '',
    status: 'live',
    key_fingerprint8: 'abcdef12',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 2,
    vendor: 'deepseek',
    models: ['deepseek-v4-pro'],
    multiplier_milli: 600,
    concurrency_limit: 10,
    rpm_limit: 60,
    daily_cap_micro: 0,
    hours: '',
    status: 'live',
    key_fingerprint8: 'bbbbbb22',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 3,
    vendor: 'deepseek',
    models: ['deepseek-flash'],
    multiplier_milli: 700,
    concurrency_limit: 5,
    rpm_limit: 30,
    daily_cap_micro: 0,
    hours: '',
    status: 'paused',
    key_fingerprint8: 'cccccc33',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 4,
    vendor: 'deepseek',
    models: ['deepseek-flash'],
    multiplier_milli: 800,
    concurrency_limit: 5,
    rpm_limit: 30,
    daily_cap_micro: 0,
    hours: '',
    status: 'live',
    key_fingerprint8: 'dddddd44',
    created_at: '2026-10-24T10:00:00+08:00',
  },
]

const health = [
  {
    listing_id: 1,
    latest_probe: {
      type: 'patrol',
      ok: false,
      latency_ms: 75,
      fail_reason: 'upstream 500',
      model_results: [],
      created_at: '2026-10-24T10:05:00+08:00',
    },
    today_requests: 200,
    today_success_rate_milli: 995,
    today_income_micro: 12_340_000,
  },
  {
    listing_id: 2,
    latest_probe: null,
    today_requests: 10,
    today_success_rate_milli: 950,
    today_income_micro: 0,
  },
  {
    listing_id: 3,
    latest_probe: null,
    today_requests: 10,
    today_success_rate_milli: 800,
    today_income_micro: 0,
  },
]

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/marketplace/seller/listings/health') {
    return jsonResponse({ success: true, data: health })
  }
  if (url === '/api/marketplace/seller/listings') {
    return jsonResponse({ success: true, data: listings })
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
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/listings',
    component: SellerListingsPage,
  })
  const dummy = ['/console/listings/new', '/console/listings/$id'].map((path) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: () => null }),
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: ['/console/listings'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('seller listings health cards', () => {
  it('renders one card per listing with the three-state dot', async () => {
    renderPage()
    await screen.findAllByText('deepseek-flash')

    expect(screen.getByRole('img', { name: '可用' })).toBeTruthy()
    expect(screen.getByRole('img', { name: '降级' })).toBeTruthy()
    expect(screen.getByRole('img', { name: '离线' })).toBeTruthy()
    expect(screen.getByRole('img', { name: '无样本' })).toBeTruthy()
    expect(screen.getAllByRole('article')).toHaveLength(4)
  })

  it('shows today income, success rate, latency and the last probe failure', async () => {
    renderPage()
    await screen.findAllByText('deepseek-flash')

    expect(screen.getByText('¥12.34')).toBeTruthy()
    expect(screen.getByText('99.5%')).toBeTruthy()
    expect(screen.getByText('95%')).toBeTruthy()
    expect(screen.getByText('80%')).toBeTruthy()
    expect(screen.getByText('75 ms')).toBeTruthy()
    expect(screen.getByText('未通过')).toBeTruthy()
    expect(screen.getByText(/失败原因/)).toBeTruthy()
  })

  it('links each card to its detail page', async () => {
    renderPage()
    await screen.findAllByText('deepseek-flash')

    const links = screen.getAllByRole('link', { name: '查看详情' }) as HTMLAnchorElement[]
    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      '/console/listings/1',
      '/console/listings/2',
      '/console/listings/3',
      '/console/listings/4',
    ])
  })
})
