import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'
import { PlazaPage } from './plaza'

const plazaItems = [
  {
    model: 'claude-3-7-sonnet-20250219',
    aliases: ['claude-3-7-sonnet'],
    origin_vendor: 'anthropic',
    vendor: 'anthropic',
    prices: {
      input_micro_per_mtok: 21_000_000,
      output_micro_per_mtok: 105_000_000,
      cache_read_micro_per_mtok: 0,
      cache_write_micro_per_mtok: 0,
    },
    capabilities: ['text', 'tools', 'vision'],
    context_length: 200000,
    status: 'active',
    enable_plaza: true,
  },
  {
    model: 'deepseek-chat',
    aliases: ['deepseek-v3'],
    origin_vendor: 'deepseek',
    vendor: 'deepseek',
    prices: {
      input_micro_per_mtok: 1_000_000,
      output_micro_per_mtok: 2_000_000,
      cache_read_micro_per_mtok: 0,
      cache_write_micro_per_mtok: 0,
    },
    capabilities: ['text'],
    context_length: 65536,
    status: 'active',
    enable_plaza: true,
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn((input: RequestInfo | URL) => {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
  if (url.includes('/api/plaza/models') || url.includes('/plaza/models')) {
    return Promise.resolve(jsonResponse(plazaItems))
  }
  if (url.includes('/api/marketplace/square/models')) {
    return Promise.resolve(jsonResponse([]))
  }
  if (url.includes('/api/marketplace/keys') || url.includes('/marketplace/key')) {
    return Promise.resolve(jsonResponse([]))
  }
  if (url.includes('/api/user/ip') || url.includes('/user/ip')) {
    return Promise.resolve(jsonResponse({ ip: '127.0.0.1' }))
  }
  return Promise.resolve(jsonResponse({ success: true }))
})

beforeEach(() => {
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
        <PlazaPage />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('PlazaPage', () => {
  it('renders plaza models with prices and vendor badges', async () => {
    renderPlaza()

    expect(await screen.findByText('claude-3-7-sonnet-20250219')).toBeTruthy()
    expect(screen.getByText('deepseek-chat')).toBeTruthy()

    // Prices formatted in compact yuan
    expect(screen.getByText('¥21')).toBeTruthy()
    expect(screen.getByText('¥105')).toBeTruthy()
    expect(screen.getByText('¥1')).toBeTruthy()
    expect(screen.getByText('¥2')).toBeTruthy()

    // Action buttons
    expect(screen.getAllByRole('button', { name: '复制模型名' })).toHaveLength(2)
    expect(screen.getAllByRole('button', { name: '用普通密钥调用' })).toHaveLength(2)
  })

  it('filters models by vendor and search query', async () => {
    renderPlaza()

    await screen.findByText('claude-3-7-sonnet-20250219')

    // Filter by vendor
    fireEvent.click(screen.getByRole('button', { name: 'anthropic' }))
    expect(screen.queryByText('deepseek-chat')).toBeNull()
    expect(screen.getByText('claude-3-7-sonnet-20250219')).toBeTruthy()

    // Reset vendor filter
    fireEvent.click(screen.getByRole('button', { name: '全部' }))
    expect(screen.getByText('deepseek-chat')).toBeTruthy()

    // Search query
    const searchInput = screen.getByPlaceholderText('搜索模型名称或厂商')
    fireEvent.change(searchInput, { target: { value: 'deepseek' } })
    expect(screen.queryByText('claude-3-7-sonnet-20250219')).toBeNull()
    expect(screen.getByText('deepseek-chat')).toBeTruthy()
  })

  it('opens CreateKeyDialog with standard key when clicking "用普通密钥调用"', async () => {
    renderPlaza()

    await screen.findByText('claude-3-7-sonnet-20250219')
    const buttons = screen.getAllByRole('button', { name: '用普通密钥调用' })
    fireEvent.click(buttons[0])

    expect(await screen.findByText('新建 API Key')).toBeTruthy()
    expect(screen.getByText('普通密钥')).toBeTruthy()
  })
})
