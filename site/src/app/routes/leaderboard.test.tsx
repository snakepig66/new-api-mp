import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type {
  LeaderboardRange,
  ModelLeaderboardItem,
  SellerLeaderboardItem,
  TrendDay,
} from '@/features/leaderboard/api'

import { LeaderboardPage } from './leaderboard'

interface Fixtures {
  modelItems: Record<LeaderboardRange, ModelLeaderboardItem[]>
  sellerItems: Record<LeaderboardRange, SellerLeaderboardItem[]>
  trendItems: TrendDay[]
}

function defaultFixtures(): Fixtures {
  return {
    modelItems: {
      '24h': [
        {
          rank: 1,
          model: 'deepseek-chat',
          requests: 321,
          success_rate_milli: 995,
          avg_latency_ms: 420,
          tokens: 12_000,
        },
        {
          rank: 2,
          model: 'gpt-4o',
          requests: 210,
          success_rate_milli: 1000,
          avg_latency_ms: 1800,
          tokens: 5_000,
        },
        {
          rank: 3,
          model: 'claude-sonnet',
          requests: 50,
          success_rate_milli: -1,
          avg_latency_ms: 0,
          tokens: 300,
        },
        {
          rank: 4,
          model: 'gemini-pro',
          requests: 8,
          success_rate_milli: 875,
          avg_latency_ms: 950,
          tokens: 90,
        },
      ],
      '7d': [
        {
          rank: 1,
          model: 'qwen-max',
          requests: 900,
          success_rate_milli: 990,
          avg_latency_ms: 600,
          tokens: 40_000,
        },
      ],
    },
    sellerItems: {
      '24h': [
        {
          rank: 1,
          alias: '分享者 #a1b2',
          requests: 321,
          success_rate_milli: 995,
          income_micro: 42_500_000,
        },
        {
          rank: 2,
          alias: '分享者 #b2c3',
          requests: 120,
          success_rate_milli: -1,
          income_micro: 1_000_000,
        },
      ],
      '7d': [
        {
          rank: 1,
          alias: '分享者 #d4e5',
          requests: 800,
          success_rate_milli: 980,
          income_micro: 99_000_000,
        },
      ],
    },
    trendItems: [
      { date: '2026-10-24', requests: 12, tokens: 3_000 },
      { date: '2026-10-25', requests: 30, tokens: 8_000 },
      { date: '2026-10-26', requests: 18, tokens: 4_000 },
    ],
  }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

let fixtures: Fixtures
let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fixtures = defaultFixtures()
  fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.includes('/marketplace/leaderboard/models')) {
      const range: LeaderboardRange = url.includes('range=7d') ? '7d' : '24h'
      return jsonResponse({
        success: true,
        data: {
          range,
          window: 'sliding',
          window_start: '2026-10-25T00:00:00+08:00',
          window_end: '2026-10-26T00:00:00+08:00',
          items: fixtures.modelItems[range],
        },
      })
    }
    if (url.includes('/marketplace/leaderboard/sellers')) {
      const range: LeaderboardRange = url.includes('range=7d') ? '7d' : '24h'
      return jsonResponse({
        success: true,
        data: {
          range,
          window: 'sliding',
          window_start: '2026-10-25T00:00:00+08:00',
          window_end: '2026-10-26T00:00:00+08:00',
          items: fixtures.sellerItems[range],
        },
      })
    }
    if (url.includes('/marketplace/leaderboard/trend')) {
      return jsonResponse({
        success: true,
        data: {
          days: 30,
          start: '2026-09-27',
          end: '2026-10-26',
          items: fixtures.trendItems,
        },
      })
    }
    return jsonResponse({ success: false, message: 'not found' })
  })
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderPage(initialEntry = '/leaderboard') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const leaderboardRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/leaderboard',
    validateSearch: (search: Record<string, unknown>) => search,
    component: LeaderboardPage,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([leaderboardRoute]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router }
}

describe('leaderboard page', () => {
  it('renders the model and seller boards with ranks, rates and amounts', async () => {
    renderPage()
    await screen.findByText('deepseek-chat')

    const first = screen.getByText('deepseek-chat').closest('li')!
    expect(first.textContent).toContain('321')
    expect(first.textContent).toContain('99.5%')
    expect(first.textContent).toContain('420ms')
    expect(first.textContent).toContain('12,000')
    expect(within(first).getByText('1').className).toContain('text-brand')

    const fourth = screen.getByText('gemini-pro').closest('li')!
    expect(within(fourth).getByText('4').className).toContain('text-ink-3')

    // 1800ms switches to seconds.
    const second = screen.getByText('gpt-4o').closest('li')!
    expect(second.textContent).toContain('1.8s')

    const seller = screen.getByText('分享者 #a1b2').closest('li')!
    expect(seller.textContent).toContain('¥42.50')
    expect(seller.textContent).toContain('99.5%')

    // -1 means "no sample", never 0%.
    const noSample = screen.getByText('claude-sonnet').closest('li')!
    expect(noSample.textContent).toContain('暂无数据')
    expect(noSample.textContent).not.toContain('0.0%')
  })

  it('switches the range, refetches and writes ?range= to the URL', async () => {
    const { router } = renderPage()
    await screen.findByText('deepseek-chat')

    expect(
      screen.getByRole('tab', { name: '24 小时' }).getAttribute('aria-selected'),
    ).toBe('true')

    fireEvent.click(screen.getByRole('tab', { name: '7 天' }))

    await waitFor(() => {
      expect(router.state.location.search).toEqual({ range: '7d' })
    })
    await screen.findByText('qwen-max')
    expect(screen.queryByText('deepseek-chat')).toBeNull()
    expect(screen.getByText('数据来源：近 7 天的平台调用记录；每 60 秒更新')).toBeTruthy()

    const urls = fetchMock.mock.calls.map(([input]) => String(input))
    expect(urls.some((url) => url.includes('/leaderboard/models?range=7d'))).toBe(true)
    expect(urls.some((url) => url.includes('/leaderboard/sellers?range=7d'))).toBe(true)

    fireEvent.click(screen.getByRole('tab', { name: '24 小时' }))
    await waitFor(() => {
      expect(router.state.location.search).toEqual({ range: '24h' })
    })
  })

  it('restores the range from the URL', async () => {
    renderPage('/leaderboard?range=7d')
    await screen.findByText('qwen-max')

    expect(
      screen.getByRole('tab', { name: '7 天' }).getAttribute('aria-selected'),
    ).toBe('true')
  })

  it('shows empty states for both boards and the trend', async () => {
    fixtures.modelItems = { '24h': [], '7d': [] }
    fixtures.sellerItems = { '24h': [], '7d': [] }
    fixtures.trendItems = []

    renderPage()

    expect(await screen.findByText('暂无模型数据')).toBeTruthy()
    expect(screen.getByText('暂无分享者数据')).toBeTruthy()
    expect(screen.getByText('暂无趋势数据')).toBeTruthy()
  })
})
