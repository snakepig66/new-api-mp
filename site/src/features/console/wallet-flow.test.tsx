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

import { WalletPage } from '@/app/routes/console/wallet'

import type { WalletFlowRow } from './api'

const ALL_TRANSACTIONS: WalletFlowRow[] = [
  {
    id: 'ledger:1',
    kind: 'usage',
    amount_micro: -1_250_000,
    status: 'available',
    reference: 'req-usage',
    created_at: '2026-10-24T10:00:00Z',
  },
  {
    id: 'ledger:2',
    kind: 'refund',
    amount_micro: 500_000,
    status: 'available',
    reference: 'req-refund',
    created_at: '2026-10-24T09:00:00Z',
  },
  {
    id: 'ledger:3',
    kind: 'earning',
    amount_micro: 3_000_000,
    status: 'pending',
    reference: 'req-earning',
    created_at: '2026-10-24T08:00:00Z',
  },
  {
    id: 'payout:4',
    kind: 'withdrawal',
    amount_micro: -60_000_000,
    status: 'approved',
    reference: 'payout-order-4',
    created_at: '2026-10-24T07:00:00Z',
  },
  {
    id: 'topup:5',
    kind: 'topup',
    amount_micro: 10_000_000,
    status: 'success',
    reference: 'trade-5',
    money: 10,
    created_at: '2026-10-24T06:00:00Z',
  },
]

let transactionUrls: string[] = []
let exportUrls: string[] = []

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function itemsForKind(kind: string | null): WalletFlowRow[] {
  if (kind === 'usage' || kind === 'refund' || kind === 'earning' || kind === 'withdrawal' || kind === 'topup') {
    return ALL_TRANSACTIONS.filter((row) => row.kind === kind)
  }
  return ALL_TRANSACTIONS
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/wallet/transactions/export')) {
    exportUrls.push(url)
    return new Response('\ufeff时间,类型,说明,金额元,状态,单号\n', {
      status: 200,
      headers: { 'content-type': 'text/csv; charset=utf-8' },
    })
  }
  if (url.startsWith('/api/marketplace/wallet/transactions')) {
    transactionUrls.push(url)
    const parsed = new URL(url, 'http://localhost')
    const kind = parsed.searchParams.get('kind')
    const items = itemsForKind(kind)
    return jsonResponse({
      success: true,
      data: {
        page: Number(parsed.searchParams.get('p') ?? 1),
        page_size: Number(parsed.searchParams.get('page_size') ?? 20),
        total: items.length,
        items,
      },
    })
  }
  if (url === '/api/marketplace/wallet') {
    return jsonResponse({
      success: true,
      data: {
        balance_micro: 12_340_000,
        month_spend_micro: -1_250_000,
        month_saved_micro: 300_000,
      },
    })
  }
  if (url === '/api/user/topup/info') {
    return jsonResponse({
      success: true,
      data: {
        enable_online_topup: false,
        enable_redemption: true,
        pay_methods: [],
        min_topup: 1,
        amount_options: [],
        discount: null,
        topup_link: '',
      },
    })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  transactionUrls = []
  exportUrls = []
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
  const walletRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/wallet',
    component: WalletPage,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([walletRoute]),
    history: createMemoryHistory({ initialEntries: ['/console/wallet'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('wallet flow filters', () => {
  it('offers all five filters plus the combined stream', async () => {
    renderPage()
    expect(await screen.findByText('req-usage')).toBeTruthy()

    for (const label of ['全部', '消费', '充值', '收益', '提现', '退款']) {
      expect(screen.getByRole('tab', { name: label })).toBeTruthy()
    }
    expect(screen.getAllByRole('tab')).toHaveLength(6)
  })

  it('requests the matching kind for 收益 / 提现 / 退款', async () => {
    renderPage()
    expect(await screen.findByText('req-usage')).toBeTruthy()

    fireEvent.click(screen.getByRole('tab', { name: '收益' }))
    await waitFor(() =>
      expect(transactionUrls.some((url) => url.includes('kind=earning'))).toBe(true),
    )
    const earningRow = (await screen.findByText('req-earning')).closest('tr')
    expect(earningRow?.textContent).toContain('收益')
    expect(earningRow?.textContent).toContain('分享收益')
    expect(earningRow?.textContent).toContain('待结算')
    expect(screen.queryByText('req-usage')).toBeNull()

    fireEvent.click(screen.getByRole('tab', { name: '提现' }))
    await waitFor(() =>
      expect(transactionUrls.some((url) => url.includes('kind=withdrawal'))).toBe(true),
    )
    const payoutRow = (await screen.findByText('payout-order-4')).closest('tr')
    expect(payoutRow?.textContent).toContain('提现')
    expect(payoutRow?.textContent).toContain('已通过')
    expect(screen.queryByText('req-earning')).toBeNull()

    fireEvent.click(screen.getByRole('tab', { name: '退款' }))
    await waitFor(() =>
      expect(transactionUrls.some((url) => url.includes('kind=refund'))).toBe(true),
    )
    const refundRow = (await screen.findByText('req-refund')).closest('tr')
    expect(refundRow?.textContent).toContain('退款')
    expect(refundRow?.textContent).toContain('退款入账')
    expect(refundRow?.textContent).toContain('¥0.50')
  })

  it('points the CSV download at the selected filter', async () => {
    renderPage()
    expect(await screen.findByText('req-usage')).toBeTruthy()

    const link = screen.getByRole('link', { name: '导出 CSV' })
    expect(link.getAttribute('href')).toContain('kind=all')
    expect(link.getAttribute('href')).toContain(
      '/api/marketplace/wallet/transactions/export',
    )

    fireEvent.click(screen.getByRole('tab', { name: '提现' }))
    await waitFor(() =>
      expect(screen.getByRole('link', { name: '导出 CSV' }).getAttribute('href')).toContain(
        'kind=withdrawal',
      ),
    )

    // The download is fetched (not navigated to) so the bearer token goes
    // along; the URL still reflects the active filter.
    fireEvent.click(screen.getByRole('link', { name: '导出 CSV' }))
    await waitFor(() =>
      expect(exportUrls.some((url) => url.includes('kind=withdrawal'))).toBe(true),
    )
  })
})
