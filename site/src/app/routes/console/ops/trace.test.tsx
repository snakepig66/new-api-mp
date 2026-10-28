import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { TraceContent } from './trace'

const trace = {
  request: {
    id: 1,
    request_id: 'req-abc',
    buyer_user_id: 5,
    buyer_token_id: 7,
    seller_id: 6,
    listing_id: 9,
    model: 'deepseek-flash',
    upstream_model: 'deepseek-flash-0901',
    price_version: 1,
    multiplier_milli: 500,
    input_tokens: 10,
    output_tokens: 20,
    total_debit_micro: 1234,
    platform_fee_micro: 12,
    seller_credit_micro: 1000,
    official_cost_micro: 2000,
    status: 'success',
    latency_ms: 321,
    created_at: '2026-10-01T00:00:00+08:00',
  },
  retry_chain: [
    {
      seq: 1,
      event: 'select',
      listing_id: 9,
      detail: 'cheapest healthy upstream',
      at: '2026-10-01T00:00:00+08:00',
    },
  ],
  ledger: [
    {
      id: 1,
      request_id: 'req-abc',
      entry_type: 'buyer_debit',
      account: 'buyer:5',
      listing_id: 9,
      amount_micro: -1234,
      price_version: 1,
      multiplier_milli: 500,
      status: 'available',
      available_at: null,
      created_at: '2026-10-01T00:00:01+08:00',
    },
  ],
  probes: [
    {
      id: 1,
      listing_id: 9,
      type: 'verify',
      ok: true,
      latency_ms: 10,
      detail: 'ok',
      created_at: '2026-10-01T00:00:02+08:00',
    },
  ],
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url === '/api/marketplace/admin/requests/req-abc') {
    return jsonResponse({ success: true, data: trace })
  }
  return jsonResponse({ success: false, message: 'request not found' }, 404)
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
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <TraceContent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('admin request trace page', () => {
  it('requires a request id before querying', async () => {
    renderPage()
    expect(screen.getByText('输入 request_id 开始排查')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '查询' }))
    expect(await screen.findByText('请填写请求 ID')).toBeTruthy()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('renders the request, retry chain, ledger and probes', async () => {
    renderPage()

    fireEvent.change(screen.getByLabelText('请求 ID'), { target: { value: 'req-abc' } })
    fireEvent.click(screen.getByRole('button', { name: '查询' }))

    expect(await screen.findByText('req-abc')).toBeTruthy()
    expect(screen.getByText('deepseek-flash-0901')).toBeTruthy()
    expect(screen.getByText('#1 · select')).toBeTruthy()
    expect(screen.getByText('cheapest healthy upstream')).toBeTruthy()
    expect(screen.getByText('买家扣费')).toBeTruthy()
    expect(screen.getByText('buyer:5')).toBeTruthy()
    expect(screen.getByText('验真')).toBeTruthy()
    expect(screen.getByText('通过')).toBeTruthy()
    expect(screen.getByText('321 ms')).toBeTruthy()
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/marketplace/admin/requests/req-abc',
      expect.anything(),
    )
  })

  it('shows the API error for an unknown request id', async () => {
    renderPage()

    fireEvent.change(screen.getByLabelText('请求 ID'), { target: { value: 'missing' } })
    fireEvent.click(screen.getByRole('button', { name: '查询' }))

    expect(await screen.findByText('没有找到对应的记录。')).toBeTruthy()
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/marketplace/admin/requests/missing',
        expect.anything(),
      ),
    )
  })
})
