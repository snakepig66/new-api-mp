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
    plaza_multiplier_milli: 1500,
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

function renderPool() {
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

describe('admin model pool page', () => {
  it('renders the 模型池 title and vendor group with base prices', async () => {
    renderPool()

    expect(await screen.findByRole('heading', { name: '模型池', level: 1 })).toBeTruthy()
    await screen.findByText('deepseek-chat')
    expect(screen.getByText('deepseek-chat')).toBeTruthy()
    expect(screen.getByText('输入基价')).toBeTruthy()
    expect(screen.getByText('输出基价')).toBeTruthy()
    expect(screen.getByText('缓存命中基价')).toBeTruthy()
  })

  it('does not render plaza listing, multiplier or capability columns', async () => {
    renderPool()

    await screen.findByText('deepseek-chat')
    expect(screen.queryByRole('columnheader', { name: '广场上架' })).toBeNull()
    expect(screen.queryByText('广场倍率')).toBeNull()
    expect(screen.queryByText('广场渠道')).not.toBeNull()
    expect(screen.queryByText('别名: deepseek-v3')).toBeNull()
    expect(screen.queryByRole('switch')).toBeNull()
  })

  it('shows only the base-price fields in the edit drawer and keeps other fields on submit', async () => {
    renderPool()

    const editButton = await screen.findByRole('button', { name: '编辑' })
    fireEvent.click(editButton)

    expect(screen.getByLabelText('输入单价 (元 / 1M tokens)')).toBeTruthy()
    expect(screen.getByLabelText('输出单价 (元 / 1M tokens)')).toBeTruthy()
    expect(screen.queryByText('模型别名 (逗号分隔)')).toBeNull()
    expect(screen.queryByText('功能能力 / 标签')).toBeNull()
    expect(screen.queryByText('上下文窗口 (Tokens)')).toBeNull()

    fireEvent.change(screen.getByLabelText('输入单价 (元 / 1M tokens)'), {
      target: { value: '3.00' },
    })
    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))

    await waitFor(() => expect(putBodies.length).toBeGreaterThan(0))
    const body = putBodies[0]
    expect(body.name).toBe('deepseek-chat')
    expect(body.aliases).toEqual(['deepseek-v3'])
    expect(body.capabilities).toEqual(['text'])
    expect(body.context_length).toBe(65536)
    expect(body.enable_plaza).toBe(true)
    expect(body.plaza_multiplier_milli).toBe(1500)
    expect(body.status).toBe('active')
  })
})
