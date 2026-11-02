import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'
import { Route } from './models'

const ModelsComponent = Route.options.component as React.ComponentType

const poolModels = [
  {
    id: 1,
    name: 'deepseek-chat',
    origin_vendor: 'deepseek',
    context_length: 65536,
    capabilities: ['text'],
    aliases: ['deepseek-v3'],
    verify_prompt: 'hi',
    status: 'active',
    enable_plaza: true,
  },
]

const prices = [
  {
    id: 1,
    model: 'deepseek-chat',
    vendor: 'deepseek',
    tier: 'flat',
    version: 1,
    input_micro_per_mtok: 1_000_000,
    output_micro_per_mtok: 2_000_000,
    cache_read_micro_per_mtok: 500_000,
    effective_at: '2026-01-01T00:00:00Z',
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

let putBodies: Record<string, unknown>[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.includes('/api/marketplace/admin/pool/models') && init?.method === 'PUT') {
    const body = JSON.parse(String(init.body)) as Record<string, unknown>
    putBodies.push(body)
    return jsonResponse({ success: true, data: body })
  }
  if (url.includes('/api/marketplace/admin/pool/models')) {
    return jsonResponse({ success: true, data: poolModels })
  }
  if (url.includes('/api/marketplace/admin/prices')) {
    return jsonResponse({ success: true, data: prices })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  putBodies = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderModels() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ModelsComponent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('admin models and plaza management', () => {
  it('renders "广场渠道管理" entry linking to /ops-legacy', async () => {
    renderModels()

    const channelLink = await screen.findByRole('link', { name: '广场渠道管理' })
    expect(channelLink.getAttribute('href')).toBe('/ops-legacy')
  })

  it('renders "广场上架" column with toggle switch for enable_plaza', async () => {
    renderModels()

    expect(await screen.findByText('广场上架')).toBeTruthy()
    expect(screen.getByText('已上架')).toBeTruthy()

    const switchBtn = screen.getByRole('switch', { name: '点击下架广场' })
    expect(switchBtn.getAttribute('aria-checked')).toBe('true')

    fireEvent.click(switchBtn)
    await waitFor(() => expect(putBodies.length).toBeGreaterThan(0))
    expect(putBodies[0].enable_plaza).toBe(false)
  })
})
