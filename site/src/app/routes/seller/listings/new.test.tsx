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

import { NewListingPage } from './new'

const vendors = [
  {
    id: 'deepseek',
    display_name: 'DeepSeek',
    tier: 'official',
    protocols: {
      openai: 'https://api.deepseek.com',
      anthropic: 'https://api.deepseek.com/anthropic',
    },
    models: ['deepseek-flash', 'deepseek-v4-pro'],
    passthrough_body: true,
  },
  {
    id: 'volcengine',
    display_name: '火山方舟',
    tier: 'official',
    protocols: { openai: 'https://ark.cn-beijing.volces.com/api/v3', anthropic: '' },
    models: [],
    passthrough_body: true,
  },
  {
    id: 'tipig',
    display_name: 'tipig gateway',
    tier: 'third_party',
    protocols: { openai: 'https://api.tipig.cloud', anthropic: 'https://api.tipig.cloud' },
    models: ['deepseek-v4.1-flash-1017'],
    passthrough_body: true,
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: vendors })
  }
  if (url.startsWith('/api/marketplace/seller/listings/probe')) {
    return jsonResponse({
      success: true,
      data: {
        ok: true,
        models: ['deepseek-flash', 'deepseek-v4-pro'],
        missing_models: [],
        latency_ms: 120,
        prompt_cache: true,
        balance_micro: 10_000_000,
        key_valid: true,
        fail_reason: '',
      },
    })
  }
  return jsonResponse({ success: false, message: 'not found' })
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
  const rootRoute = createRootRoute()
  const newRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/seller/listings/new',
    component: NewListingPage,
  })
  const dummy = ['/seller', '/seller/listings'].map((path) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: () => null }),
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([newRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: ['/seller/listings/new'] }),
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('new listing wizard', () => {
  it('shows the two channel tiers and disables third-party', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    expect(screen.getByText('第三方渠道')).toBeTruthy()
    expect(screen.getByText('即将开放')).toBeTruthy()
    expect(screen.getByText('厂商开放平台直连，base_url 由平台固定。')).toBeTruthy()
  })

  it('blocks next when the selected channel has no priced models', async () => {
    renderPage()
    await screen.findByText('火山方舟')

    fireEvent.click(screen.getByRole('radio', { name: /火山方舟/ }))

    expect(
      screen.getByText('该渠道还没有可用价目的模型映射，请联系运营配置'),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: '下一步' })).toHaveProperty('disabled', true)
  })

  it('never offers third-party vendors in the official grid', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    expect(screen.queryByText('tipig gateway')).toBeNull()
    expect(screen.queryByRole('radio', { name: /tipig/ })).toBeNull()
  })

  it('lists only the selected channel models after probing', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    fireEvent.click(screen.getByRole('radio', { name: /DeepSeek/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    const key = await screen.findByLabelText('上游 API Key')
    fireEvent.change(key, { target: { value: 'sk-test-1234567890' } })
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    const flash = await screen.findByRole('checkbox', { name: 'deepseek-flash' })
    expect(screen.getByRole('checkbox', { name: 'deepseek-v4-pro' })).toBeTruthy()
    expect((flash as HTMLInputElement).checked).toBe(true)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '下一步' })).toHaveProperty('disabled', false)
    })
  })
})
