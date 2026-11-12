import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { SellerOnboardingPage } from './onboarding'

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function sellerMe(progress: Record<string, unknown>) {
  return {
    user_id: 1,
    seller: {
      exists: true,
      status: 'active',
      progress: {
        agreement_signed: false,
        real_name_verified: false,
        payout_account_bound: false,
        next_step: 'agreement',
        agreement_version: 'v1',
        ...progress,
      },
    },
  }
}

let meBody = sellerMe({})
let listingsBody: unknown[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/marketplace/me') {
    return jsonResponse({ success: true, data: meBody })
  }
  if (url.startsWith('/api/marketplace/seller/listings')) {
    return jsonResponse({ success: true, data: listingsBody })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
  listingsBody = []
  meBody = sellerMe({
    agreement_signed: true,
    next_step: 'payout_account',
  })
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
  const pageRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/onboarding',
    component: SellerOnboardingPage,
  })
  const dummy = [
    '/console/listings/new',
    '/console/listings',
    '/console/earnings',
    '/agreement',
    '/privacy',
  ].map((path) => createRoute({ getParentRoute: () => rootRoute, path, component: () => null }))
  const router = createRouter({
    routeTree: rootRoute.addChildren([pageRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: ['/console/onboarding'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

function stepItems() {
  const rail = screen.getByLabelText('开通进度')
  return within(rail).getAllByRole('listitem')
}

describe('seller onboarding rail', () => {
  it('shows three steps, with only the agreement active before signing', async () => {
    meBody = sellerMe({
      agreement_signed: false,
      next_step: 'agreement',
    })
    renderPage()
    await screen.findByText('签署分享者协议')

    expect(screen.getByText('0/3')).toBeTruthy()
    expect(screen.getByText('绑定收款账户')).toBeTruthy()
    expect(screen.getByText('发布第一个渠道')).toBeTruthy()

    const steps = stepItems()
    expect(steps).toHaveLength(3)
    expect(within(steps[0]).getByText('当前')).toBeTruthy()
    expect(within(steps[1]).getByText('未开始')).toBeTruthy()
    expect(within(steps[2]).getByText('未开始')).toBeTruthy()
  })

  it('asks for the payout account after the agreement is signed', async () => {
    renderPage()
    await screen.findByText('收款账户设置')

    expect(screen.getByText('1/3')).toBeTruthy()
    const steps = stepItems()
    expect(within(steps[0]).getByText('已完成')).toBeTruthy()
    expect(within(steps[1]).getByText('当前')).toBeTruthy()
    expect(screen.queryByText('发布第一个渠道')).toBeTruthy()
  })

  it('marks all steps done once a listing exists', async () => {
    meBody = sellerMe({
      agreement_signed: true,
      payout_account_bound: true,
      next_step: 'done',
    })
    listingsBody = [{ id: 7, status: 'live', models: [] }]
    renderPage()
    await screen.findByText('已开通，可以分享渠道了')

    expect(screen.getByText('3/3')).toBeTruthy()
    for (const step of stepItems()) {
      expect(within(step).getByText('已完成')).toBeTruthy()
    }
  })
})
