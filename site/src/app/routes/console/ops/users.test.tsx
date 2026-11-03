import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { UsersPage } from './users'

vi.mock('@/lib/auth', () => ({
  useAuth: () => ({
    user: { id: 1, username: 'root', role: 100 },
    session: {},
    isLoggedIn: true,
    role: 100,
    isAdmin: true,
    isRoot: true,
  }),
}))

const users = [
  {
    id: 2,
    username: 'alice',
    display_name: '爱丽丝',
    email: 'alice@example.com',
    role: 1,
    status: 1,
    quota: 5_000_000,
    used_quota: 1_000_000,
    request_count: 42,
    group: 'default',
    created_at: 1_700_000_000,
  },
  {
    id: 3,
    username: 'bob',
    display_name: '',
    email: 'bob@example.com',
    role: 10,
    status: 2,
    quota: 0,
    used_quota: 0,
    request_count: 0,
    group: 'default',
    created_at: 1_700_000_000,
  },
]

const topups = [
  {
    id: 9,
    user_id: 2,
    amount: 10_000_000,
    money: 10,
    trade_no: 'ORDER-1',
    payment_method: 'epay',
    payment_provider: 'epay',
    create_time: 1_700_000_000,
    complete_time: 0,
    status: 'pending',
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function page(items: unknown[], total: number) {
  return { success: true, data: { page: 1, page_size: 20, total, items } }
}

let postBodies: { url: string; body: Record<string, unknown> }[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (init?.method === 'POST') {
    const body = init.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {}
    postBodies.push({ url, body })
    return jsonResponse({ success: true, data: null })
  }
  if (url.includes('/api/status')) {
    return jsonResponse({ success: true, data: { quota_per_unit: 1_000_000 } })
  }
  if (url.includes('/api/user/topup')) {
    return jsonResponse(page(topups, topups.length))
  }
  if (url.includes('/api/user')) {
    return jsonResponse(page(users, users.length))
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  postBodies = []
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
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <UsersPage />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('ops users page', () => {
  it('renders the user list with formatted balance', async () => {
    renderPage()

    expect(await screen.findByText('alice')).toBeTruthy()
    expect(screen.getByText('爱丽丝')).toBeTruthy()
    expect(screen.getByText('bob')).toBeTruthy()
    // 5,000,000 quota / 1,000,000 = ¥5
    expect(screen.getByText('¥5.00')).toBeTruthy()
    expect(screen.getByText('普通用户')).toBeTruthy()
    expect(screen.getByText('管理员')).toBeTruthy()
  })

  it('sends an add_quota adjustment in quota units after confirmation', async () => {
    renderPage()

    const row = (await screen.findByText('alice')).closest('tr')
    expect(row).not.toBeNull()
    fireEvent.click(within(row as HTMLElement).getByText('调整余额'))

    const amount = await screen.findByLabelText('金额（元）')
    fireEvent.change(amount, { target: { value: '3' } })

    await waitFor(() => expect(screen.getByText('¥8.00')).toBeTruthy())

    fireEvent.click(screen.getByText('确认调整'))

    await waitFor(() => expect(postBodies.length).toBeGreaterThan(0))
    expect(postBodies[0].url).toContain('/api/user/manage')
    expect(postBodies[0].body).toMatchObject({
      id: 2,
      action: 'add_quota',
      mode: 'add',
      value: 3_000_000,
    })
  })

  it('loads top-up records and completes a pending order', async () => {
    renderPage()

    fireEvent.click(await screen.findByText('充值记录'))

    expect(await screen.findByText('ORDER-1')).toBeTruthy()
    fireEvent.click(screen.getByText('补单'))

    const dialogConfirm = await screen.findByText('确认补单')
    fireEvent.click(dialogConfirm)

    await waitFor(() => expect(postBodies.length).toBeGreaterThan(0))
    expect(postBodies[0].url).toContain('/api/user/topup/complete')
    expect(postBodies[0].body).toMatchObject({ trade_no: 'ORDER-1' })
  })
})
