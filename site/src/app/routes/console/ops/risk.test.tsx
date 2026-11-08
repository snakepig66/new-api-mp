import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { RiskContent } from './risk'

const sessions = {
  total: 1,
  frozen_micro: 1_230_000,
  items: [
    {
      request_id: 'req-abc',
      listing_id: 42,
      buyer_id: 7,
      frozen_micro: 1_230_000,
      created_at: '2026-10-01T00:00:00+08:00',
    },
  ],
}

const related = {
  user_id: 10011,
  items: [
    { user_id: 10012, relation_type: 'same_ip', value: '203.0.113.9' },
    { user_id: 10013, relation_type: 'same_payout_account', value: 'al***om' },
  ],
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

let forceBody: Record<string, unknown> | null = null

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  const method = init?.method ?? 'GET'
  if (method === 'POST' && url === '/api/marketplace/admin/sessions/force-release') {
    forceBody = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null
    return jsonResponse({
      success: true,
      data: {
        listing_id: 123,
        sessions: 1,
        frozen_micro: 2_000_000,
        slots_released: 2,
        refund_failures: 0,
      },
    })
  }
  if (url.startsWith('/api/marketplace/admin/sessions')) {
    return jsonResponse({ success: true, data: sessions })
  }
  if (url.startsWith('/api/marketplace/admin/risk/related')) {
    return jsonResponse({ success: true, data: related })
  }
  return jsonResponse({ success: false, message: 'not found' }, 404)
})

beforeEach(() => {
  forceBody = null
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
        <RiskContent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('risk admin page', () => {
  it('lists active sessions with their frozen total', async () => {
    renderPage()

    expect(await screen.findByText('req-abc')).toBeTruthy()
    expect(screen.getByText('#42')).toBeTruthy()
    expect(screen.getByText('#7')).toBeTruthy()
    expect(screen.getByText('¥1.23')).toBeTruthy()
    expect(screen.getByText('共 1 条 · 冻结合计 ¥1.23')).toBeTruthy()
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/marketplace/admin/sessions',
      expect.anything(),
    )
  })

  it('requires a user id and renders the related accounts', async () => {
    renderPage()
    await screen.findByText('req-abc')

    fireEvent.click(screen.getByRole('button', { name: '查询' }))
    expect(await screen.findByText('请填写用户 ID')).toBeTruthy()

    fireEvent.change(screen.getByLabelText('用户 ID'), {
      target: { value: '10011' },
    })
    fireEvent.click(screen.getByRole('button', { name: '查询' }))

    expect(await screen.findByText('#10012')).toBeTruthy()
    expect(screen.getByText('同登录 IP')).toBeTruthy()
    expect(screen.getByText('203.0.113.9')).toBeTruthy()
    expect(screen.getByText('同收款账户')).toBeTruthy()
    expect(screen.getByText('al***om')).toBeTruthy()
  })

  it('force-releases a listing only after confirmation', async () => {
    renderPage()
    await screen.findByText('req-abc')

    fireEvent.change(screen.getByLabelText('挂单 ID'), { target: { value: '123' } })
    fireEvent.click(screen.getByRole('button', { name: '强制释放' }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('确认释放挂单 #123？')).toBeTruthy()
    expect(forceBody).toBeNull()

    fireEvent.click(dialog.getByRole('button', { name: '确认释放' }))

    await waitFor(() => expect(forceBody).toEqual({ listing_id: 123 }))
    expect(await screen.findByText('已强制释放')).toBeTruthy()
  })
})
