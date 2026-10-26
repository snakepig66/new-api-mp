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

import {
  walletDescription,
  walletKindLabel,
  walletStatusMeta,
} from '@/features/console/wallet-labels'
import type { WalletTransaction } from '@/features/console/types'

import { WalletPage } from './wallet'

const ALL_TRANSACTIONS: WalletTransaction[] = [
  {
    id: 'ledger:30',
    kind: 'usage',
    amount_micro: -1_250_000,
    status: 'available',
    reference: 'req-30',
    created_at: '2026-10-24T10:00:00Z',
  },
  {
    id: 'ledger:31',
    kind: 'refund',
    amount_micro: 500_000,
    status: 'available',
    reference: 'req-31',
    created_at: '2026-10-24T09:00:00Z',
  },
  {
    id: 'topup:7',
    kind: 'topup',
    amount_micro: 10_000_000,
    status: 'success',
    reference: 'trade-7',
    money: 10,
    created_at: '2026-10-24T08:00:00Z',
  },
  {
    id: 'topup:8',
    kind: 'topup',
    amount_micro: 5_000_000,
    status: 'pending',
    reference: 'trade-8',
    money: 5,
    created_at: '2026-10-24T07:00:00Z',
  },
]

let transactionUrls: string[] = []

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function itemsForKind(kind: string | null): WalletTransaction[] {
  if (kind === 'usage') return ALL_TRANSACTIONS.filter((row) => row.kind !== 'topup')
  if (kind === 'topup') return ALL_TRANSACTIONS.filter((row) => row.kind === 'topup')
  return ALL_TRANSACTIONS
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
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

describe('wallet unified transactions table', () => {
  it('renders ledger debits, refunds and top-ups in one table', async () => {
    renderPage()
    expect(await screen.findByText('trade-7')).toBeTruthy()

    expect(document.querySelectorAll('tbody tr')).toHaveLength(4)

    const usageRow = screen.getByText('req-30').closest('tr')
    expect(usageRow?.textContent).toContain('消费')
    expect(usageRow?.textContent).toContain('模型调用扣费')
    expect(usageRow?.textContent).toContain('-¥1.25')
    expect(usageRow?.textContent).toContain('已入账')

    const refundRow = screen.getByText('req-31').closest('tr')
    expect(refundRow?.textContent).toContain('退款')
    expect(refundRow?.textContent).toContain('退款入账')
    expect(refundRow?.textContent).toContain('¥0.50')

    // A top-up shows the paid money in the description and the credited quota
    // in the amount column.
    const paidRow = screen.getByText('trade-7').closest('tr')
    expect(paidRow?.textContent).toContain('充值')
    expect(paidRow?.textContent).toContain('在线充值 · 实付 ¥10.00')
    expect(paidRow?.textContent).toContain('¥10.00')
    expect(paidRow?.textContent).toContain('已到账')

    const pendingRow = screen.getByText('trade-8').closest('tr')
    expect(pendingRow?.textContent).toContain('待支付')
  })

  it('filters the stream by the 全部 / 消费 / 充值 tabs', async () => {
    renderPage()
    expect(await screen.findByText('trade-7')).toBeTruthy()
    expect(transactionUrls.some((url) => url.includes('kind=all'))).toBe(true)

    fireEvent.click(screen.getByRole('tab', { name: '消费' }))
    await waitFor(() =>
      expect(transactionUrls.some((url) => url.includes('kind=usage'))).toBe(true),
    )
    await waitFor(() => expect(screen.queryByText('trade-7')).toBeNull())
    expect(screen.getByText('req-30')).toBeTruthy()
    expect(screen.getByText('req-31')).toBeTruthy()

    fireEvent.click(screen.getByRole('tab', { name: '充值' }))
    await waitFor(() =>
      expect(transactionUrls.some((url) => url.includes('kind=topup'))).toBe(true),
    )
    await waitFor(() => expect(screen.queryByText('req-30')).toBeNull())
    expect(screen.getByText('trade-7')).toBeTruthy()
    expect(screen.getByText('trade-8')).toBeTruthy()
    expect(screen.getAllByRole('tab', { name: '充值' })[0].getAttribute('aria-selected')).toBe(
      'true',
    )
  })
})

describe('wallet transaction labels', () => {
  it('maps ledger and top-up statuses', () => {
    expect(walletStatusMeta('usage', 'available')).toEqual({
      label: '已入账',
      tone: 'brand',
    })
    expect(walletStatusMeta('usage', 'pending')).toEqual({
      label: '待结算',
      tone: 'neutral',
    })
    expect(walletStatusMeta('refund', 'reversed')).toEqual({
      label: '已冲正',
      tone: 'danger',
    })
    expect(walletStatusMeta('topup', 'success')).toEqual({
      label: '已到账',
      tone: 'brand',
    })
    expect(walletStatusMeta('topup', 'pending')).toEqual({
      label: '待支付',
      tone: 'neutral',
    })
    expect(walletStatusMeta('topup', 'failed')).toEqual({
      label: '已关闭',
      tone: 'danger',
    })
    expect(walletStatusMeta('topup', 'expired')).toEqual({
      label: '已关闭',
      tone: 'danger',
    })
    expect(walletStatusMeta('topup', 'unknown-status').label).toBe('未知')
  })

  it('labels kinds and describes paid top-up money', () => {
    expect(walletKindLabel('usage')).toBe('消费')
    expect(walletKindLabel('refund')).toBe('退款')
    expect(walletKindLabel('topup')).toBe('充值')

    expect(
      walletDescription({
        id: 'topup:1',
        kind: 'topup',
        amount_micro: 10_000_000,
        status: 'success',
        reference: 'trade-1',
        money: 10,
        created_at: '2026-10-24T08:00:00Z',
      }),
    ).toBe('在线充值 · 实付 ¥10.00')
    expect(
      walletDescription({
        id: 'ledger:1',
        kind: 'usage',
        amount_micro: -1,
        status: 'available',
        reference: 'req-1',
        created_at: '2026-10-24T08:00:00Z',
      }),
    ).toBe('模型调用扣费')
  })
})
