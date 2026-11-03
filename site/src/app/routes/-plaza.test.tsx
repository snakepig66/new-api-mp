import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { clearSession } from '@/lib/session'
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
    base_prices: {
      input_micro_per_mtok: 30_000_000,
      output_micro_per_mtok: 150_000_000,
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
      cache_read_micro_per_mtok: 500_000,
      cache_write_micro_per_mtok: 0,
    },
    base_prices: {
      input_micro_per_mtok: 1_000_000,
      output_micro_per_mtok: 2_000_000,
      cache_read_micro_per_mtok: 500_000,
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
  if (url.includes('/api/token/')) {
    return Promise.resolve(jsonResponse({ success: true, data: { page: 1, page_size: 100, total: 0, items: [] } }))
  }
  if (url.includes('/api/user/ip') || url.includes('/user/ip')) {
    return Promise.resolve(jsonResponse({ ip: '127.0.0.1' }))
  }
  return Promise.resolve(jsonResponse({ success: true }))
})

beforeEach(() => {
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
  clearSession()
})

afterEach(() => {
  cleanup()
  clearSession()
  vi.unstubAllGlobals()
})

function renderPlaza() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const plazaRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/plaza',
    component: PlazaPage,
  })
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    validateSearch: (search: Record<string, unknown>) => ({
      redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
    }),
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([plazaRoute, loginRoute]),
    history: createMemoryHistory({ initialEntries: ['/plaza'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return router
}

describe('PlazaPage', () => {
  it('renders plaza cards with base and plaza prices', async () => {
    renderPlaza()

    expect(await screen.findByText('claude-3-7-sonnet-20250219')).toBeTruthy()
    expect(screen.getByText('deepseek-chat')).toBeTruthy()

    expect(screen.getByText('¥30')).toBeTruthy()
    expect(screen.getByText('¥150')).toBeTruthy()
    expect(screen.getByText('¥21')).toBeTruthy()
    expect(screen.getByText('¥105')).toBeTruthy()
    expect(screen.getByText('¥1')).toBeTruthy()
    expect(screen.getByText('¥2')).toBeTruthy()

    expect(screen.getByText('省 30%')).toBeTruthy()
  })

  it('hides the discount badge when the plaza price is not below base', async () => {
    renderPlaza()
    await screen.findByText('deepseek-chat')
    const badges = screen.queryAllByText(/省 /)
    expect(badges).toHaveLength(1)
  })

  it('does not render a cache row when there is no cache price', async () => {
    renderPlaza()
    await screen.findByText('claude-3-7-sonnet-20250219')
    expect(screen.getAllByText('缓存')).toHaveLength(1)
  })

  it('filters models by vendor and search query', async () => {
    renderPlaza()

    await screen.findByText('claude-3-7-sonnet-20250219')

    fireEvent.click(screen.getByRole('button', { name: 'anthropic' }))
    expect(screen.queryByText('deepseek-chat')).toBeNull()
    expect(screen.getByText('claude-3-7-sonnet-20250219')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '全部' }))
    expect(screen.getByText('deepseek-chat')).toBeTruthy()

    const searchInput = screen.getByPlaceholderText('搜索模型名称或厂商')
    fireEvent.change(searchInput, { target: { value: 'deepseek' } })
    expect(screen.queryByText('claude-3-7-sonnet-20250219')).toBeNull()
    expect(screen.getByText('deepseek-chat')).toBeTruthy()
  })

  it('does not offer 加入 Key on plaza cards', async () => {
    renderPlaza()
    await screen.findByText('deepseek-chat')
    expect(screen.queryByRole('button', { name: '加入 Key' })).toBeNull()
  })
})
