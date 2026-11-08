import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { MetricsContent } from './metrics'

const rows = [
  {
    id: 1,
    metric_name: 'stream_cut_rate',
    metric_value: 0.25,
    dimensions: { total: 4, hits: 1 },
    bucket_time: '2026-10-01T00:00:00+08:00',
    created_at: '2026-10-01T00:05:00+08:00',
  },
  {
    id: 2,
    metric_name: '503_reason',
    metric_value: 2,
    dimensions: { reason: 'plan_quota_exhausted' },
    bucket_time: '2026-10-01T00:00:00+08:00',
    created_at: '2026-10-01T00:05:00+08:00',
  },
  {
    id: 3,
    metric_name: 'pre_freeze_deviation',
    metric_value: 1000,
    dimensions: { capped_requests: 1 },
    bucket_time: '2026-10-01T00:00:00+08:00',
    created_at: '2026-10-01T00:05:00+08:00',
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

let requestedURLs: string[] = []
let payload: unknown[] = rows

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  requestedURLs.push(url)
  if (url.startsWith('/api/marketplace/admin/metrics')) {
    return jsonResponse({ success: true, data: payload })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  requestedURLs = []
  payload = rows
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
        <MetricsContent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('admin metrics page', () => {
  it('lists metric values and decoded dimensions', async () => {
    renderPage()

    expect(await screen.findByText('25.00%')).toBeTruthy()
    expect(screen.getByText('2')).toBeTruthy()
    expect(screen.getByText('¥0.001')).toBeTruthy()
    expect(screen.getByText('total=4, hits=1')).toBeTruthy()
    expect(screen.getByText('reason=plan_quota_exhausted')).toBeTruthy()
    expect(screen.getByText('capped_requests=1')).toBeTruthy()
    expect(requestedURLs[0]).toContain('/api/marketplace/admin/metrics?since=')
  })

  it('filters by metric name and time range', async () => {
    renderPage()
    await screen.findByText('25.00%')

    fireEvent.change(screen.getByLabelText('指标'), {
      target: { value: 'stream_cut_rate' },
    })
    await waitFor(() =>
      expect(
        requestedURLs.some((url) => url.includes('metric_name=stream_cut_rate')),
      ).toBe(true),
    )

    fireEvent.click(screen.getByRole('tab', { name: '最近 7 天' }))
    await waitFor(() => {
      const last = requestedURLs.at(-1) ?? ''
      const since = new URL(last, 'http://localhost').searchParams.get('since')
      expect(since).not.toBeNull()
      expect(Date.now() - Date.parse(since!)).toBeGreaterThan(24 * 3_600_000)
    })
  })

  it('shows the empty state when the range has no metrics', async () => {
    payload = []
    renderPage()

    expect(await screen.findByText('当前范围内没有指标')).toBeTruthy()
  })
})
