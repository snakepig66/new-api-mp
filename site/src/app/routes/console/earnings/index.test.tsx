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

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/marketplace/seller/stats') {
    return jsonResponse({
      success: true,
      data: {
        pending_micro: 9_300_000,
        available_micro: 50_000_000,
        paid_micro: 20_000_000,
        month_income_micro: 9_300_000,
        platform_fee_total_micro: 700_000,
        daily: [],
        as_of: '2026-10-24T10:00:00+08:00',
      },
    })
  }
  if (url === '/api/marketplace/seller/listings/health') {
    return jsonResponse({ success: true, data: [] })
  }
  if (url === '/api/marketplace/seller/listings') {
    return jsonResponse({ success: true, data: [] })
  }
  if (url === '/api/marketplace/me') {
    return jsonResponse({
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
    })
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

    expect(await screen.findByText('待结算')).toBeTruthy()
    expect(screen.getByText('可提现')).toBeTruthy()
    expect(screen.getByText('已提现')).toBeTruthy()

    expect(screen.getByText('¥9.30')).toBeTruthy()
    expect(screen.getByText('¥50.00')).toBeTruthy()
    expect(screen.getByText('¥20.00')).toBeTruthy()
  })

  it('explains the T+7 maturity and keeps the withdrawal gate', async () => {
    renderPage()

    expect((await screen.findAllByText(/T\+7/)).length).toBeGreaterThan(0)
    expect(screen.getByText('提现条件')).toBeTruthy()
    expect(screen.getByText('已满足 3 / 4 项')).toBeTruthy()
  })
})
