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

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/marketplace/me') {
    return jsonResponse({ success: true, data: meBody })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
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

describe('seller onboarding three-step rail', () => {
  it('renders 协议 → 绑定收款 → 完成 with done/current/todo states', async () => {
    renderPage()
    await screen.findByText('签署分享者协议')

    expect(screen.getByText('绑定收款')).toBeTruthy()
    expect(screen.getByText('完成')).toBeTruthy()
    expect(screen.getByText('1/3')).toBeTruthy()

    const steps = screen.getAllByRole('listitem')
    expect(steps).toHaveLength(3)
    expect(within(steps[0]).getByText('已完成')).toBeTruthy()
    expect(within(steps[1]).getByText('当前')).toBeTruthy()
    expect(within(steps[2]).getByText('未开始')).toBeTruthy()
  })

  it('marks all three steps done once agreement and payout are complete', async () => {
    meBody = sellerMe({
      agreement_signed: true,
      payout_account_bound: true,
      next_step: 'done',
    })
    renderPage()
    await screen.findByText('签署分享者协议')

    expect(screen.getByText('3/3')).toBeTruthy()
    const steps = screen.getAllByRole('listitem')
    expect(steps).toHaveLength(3)
    for (const step of steps) {
      expect(within(step).getByText('已完成')).toBeTruthy()
    }
  })
})
