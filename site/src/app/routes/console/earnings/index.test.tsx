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

import { SellerOverviewPage } from './index'

// ECharts 按需动态加载；测试只断言图表容器挂载，不测图表内部实现，
// 因此把 echarts 各入口 mock 掉，避免 jsdom 没有画布导致 zrender 崩溃。
vi.mock('echarts/core', () => ({
  use: () => {},
  init: () => ({ setOption: () => {}, dispose: () => {}, resize: () => {} }),
}))
vi.mock('echarts/charts', () => ({ BarChart: {} }))
vi.mock('echarts/components', () => ({
  GridComponent: {},
  TooltipComponent: {},
  LegendComponent: {},
}))
vi.mock('echarts/renderers', () => ({ CanvasRenderer: {} }))

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const mePayload = {
  success: true,
  data: {
    user_id: 1,
    seller: {
      exists: true,
      status: 'active',
      progress: {
        agreement_signed: true,
        real_name_verified: true,
        payout_account_bound: true,
        next_step: 'done',
        agreement_version: 'v1',
      },
    },
  },
}

const defaultStats = {
  pending_micro: 9_300_000,
  available_micro: 50_000_000,
  paid_micro: 20_000_000,
  month_income_micro: 15_600_000,
  platform_fee_total_micro: 700_000,
  month_calls: 128,
  month_tokens: 2_450_000,
  range: { days: 30, start: '2026-09-28', end: '2026-10-27' },
  by_model: [],
  daily_by_model: [
    {
      model: 'glm-4.7',
      days: [
        { date: '2026-10-26', tokens: 1_200, income_micro: 1_500_000 },
        { date: '2026-10-27', tokens: 800, income_micro: 900_000 },
      ],
    },
    {
      model: 'other',
      days: [
        { date: '2026-10-26', tokens: 300, income_micro: 250_000 },
        { date: '2026-10-27', tokens: 0, income_micro: 0 },
      ],
    },
  ],
  by_channel: [
    { listing_id: 9, label: '备用渠道', calls: 10, tokens: 100_000, income_micro: 500_000 },
    { listing_id: 7, label: '主力渠道', calls: 80, tokens: 1_500_000, income_micro: 6_000_000 },
  ],
  daily: [{ date: '2026-10-26', income_micro: 1_750_000 }],
  as_of: '2026-10-27T10:00:00+08:00',
}

const emptyStats = {
  ...defaultStats,
  pending_micro: 0,
  available_micro: 0,
  paid_micro: 0,
  month_income_micro: 0,
  platform_fee_total_micro: 0,
  month_calls: 0,
  month_tokens: 0,
  daily_by_model: [],
  by_channel: [],
  daily: [],
}

let statsPayload: Record<string, unknown> = defaultStats

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/marketplace/seller/stats') {
    return jsonResponse({ success: true, data: statsPayload })
  }
  if (url === '/api/marketplace/me') {
    return jsonResponse(mePayload)
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  statsPayload = defaultStats
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
    path: '/console/earnings',
    component: SellerOverviewPage,
  })
  const dummy = [
    '/console/earnings/payouts',
    '/console/onboarding',
    '/console/listings',
    '/console/listings/new',
  ].map((path) => createRoute({ getParentRoute: () => rootRoute, path, component: () => null }))
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: ['/console/earnings'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('seller earnings overview', () => {
  it('shows pending, available and paid as three separate balances', async () => {
    renderPage()

    // 待结算 appears both as the month stat card and the balance card.
    expect((await screen.findAllByText('待结算')).length).toBe(2)
    expect(screen.getByText('可提现')).toBeTruthy()
    expect(screen.getByText('已提现')).toBeTruthy()

    expect(screen.getAllByText('¥9.30').length).toBe(2)
    expect(screen.getByText('¥50.00')).toBeTruthy()
    expect(screen.getByText('¥20.00')).toBeTruthy()
  })

  it('explains the T+7 maturity and keeps the withdrawal gate', async () => {
    renderPage()

    expect((await screen.findAllByText(/T\+7/)).length).toBeGreaterThan(0)
    expect(screen.getByText('提现条件')).toBeTruthy()
    expect(screen.getByText('已满足 3 / 4 项')).toBeTruthy()
  })

  it('renders the four month stat cards from the stats payload', async () => {
    renderPage()

    expect(await screen.findByText('调用次数（本月至今）')).toBeTruthy()
    expect(screen.getByText('Tokens（本月至今）')).toBeTruthy()
    expect(screen.getByText('收入（本月至今）')).toBeTruthy()

    expect(screen.getByText('128')).toBeTruthy()
    expect(screen.getByText('2,450,000')).toBeTruthy()
    // 收入 stat card, plus the month-income/platform-fee line under the balances.
    expect(screen.getByText('¥15.60')).toBeTruthy()
    expect(screen.getByText('本月收入 ¥15.60 · 平台费（累计） ¥0.70')).toBeTruthy()
    expect(screen.getByText('持有期内收入，T+7 后转可提现。')).toBeTruthy()
  })

  it('mounts both stacked charts as accessible images', async () => {
    renderPage()

    const charts = await screen.findAllByRole('img')
    expect(charts.length).toBe(2)
    expect(screen.getByText('Tokens（按模型）')).toBeTruthy()
    expect(screen.getByText('收入（按模型）')).toBeTruthy()
    expect(screen.getByText('2026-09-28 至 2026-10-27，两张图都按模型堆叠。')).toBeTruthy()
  })

  it('ranks channels by income and links each row to the edit drawer', async () => {
    renderPage()

    const primary = await screen.findByRole('link', { name: '主力渠道' })
    expect(primary.getAttribute('href')).toContain('edit=7')
    const secondary = screen.getByRole('link', { name: '备用渠道' })
    expect(secondary.getAttribute('href')).toContain('edit=9')
  })

  it('shows guidance instead of charts and ranking when there is no data', async () => {
    statsPayload = emptyStats
    renderPage()

    expect(await screen.findByText('近 30 天还没有用量和收入')).toBeTruthy()
    expect(screen.getByText('近 30 天没有渠道收入')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })
})
