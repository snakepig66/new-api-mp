import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'
import { Route } from './plaza'

const PlazaComponent = Route.options.component as React.ComponentType

const poolModels = [
  {
    id: 1,
    name: 'deepseek-chat',
    origin_vendor: 'deepseek',
    context_length: 65536,
    capabilities: ['text'],
    aliases: [],
    verify_prompt: '',
    status: 'active',
    enable_plaza: true,
    plaza_multiplier_milli: 1500,
  },
  {
    id: 2,
    name: 'unpriced-model',
    origin_vendor: 'deepseek',
    context_length: 0,
    capabilities: [],
    aliases: [],
    verify_prompt: '',
    status: 'active',
    enable_plaza: false,
    plaza_multiplier_milli: 1000,
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
    cache_read_micro_per_mtok: 0,
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

function renderPlaza() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <PlazaComponent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('admin plaza listing page', () => {
  it('renders title, base price, multiplier and plaza price', async () => {
    renderPlaza()

    expect(await screen.findByRole('heading', { name: '广场上架', level: 1 })).toBeTruthy()
    await screen.findByText('deepseek-chat')
    expect(screen.getByText('deepseek-chat')).toBeTruthy()
    expect(screen.getByRole('button', { name: '编辑广场倍率 deepseek-chat' }).textContent).toBe(
      '1.50x',
    )
    expect(screen.getByText('已上架')).toBeTruthy()
  })

  it('reports an error when the multiplier is out of range', async () => {
    renderPlaza()

    const cell = await screen.findByRole('button', { name: '编辑广场倍率 deepseek-chat' })
    fireEvent.click(cell)
    const input = screen.getByTestId('plaza-multiplier-input') as HTMLInputElement
    fireEvent.change(input, { target: { value: '200' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    expect(await screen.findByText('广场倍率需在 0.001 到 100 之间')).toBeTruthy()
    expect(putBodies.length).toBe(0)
  })

  it('disables listing for models without a base price', async () => {
    renderPlaza()

    expect(await screen.findByText('无基价，不可上架')).toBeTruthy()
    const switchNames = screen.queryAllByRole('switch').map((el) => el.getAttribute('aria-label'))
    expect(switchNames).not.toContain('点击上架广场')
    expect(switchNames).toContain('点击下架广场')
  })
})
