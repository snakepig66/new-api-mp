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
    kind: 'official',
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
    kind: 'official',
    protocols: { openai: 'https://ark.cn-beijing.volces.com/api/v3', anthropic: '' },
    models: [],
    passthrough_body: true,
  },
  {
    id: 'tipig',
    display_name: 'tipig gateway',
    tier: 'third_party',
    kind: 'third_party',
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
        verified_models: ['deepseek-flash'],
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
  it('groups channels by kind and keeps third-party disabled', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    expect(screen.getByText('官方渠道')).toBeTruthy()
    expect(screen.getByText('套餐渠道')).toBeTruthy()
    expect(screen.getByText('第三方（即将开放）')).toBeTruthy()
    expect(screen.getByText('tipig gateway')).toBeTruthy()

    const thirdParty = screen.getByRole('radio', { name: /tipig/ }) as HTMLInputElement
    expect(thirdParty.disabled).toBe(true)
  })

  it('blocks next when the selected channel has no priced models', async () => {
    renderPage()
    await screen.findByText('火山方舟')

    fireEvent.click(screen.getByRole('radio', { name: /火山方舟/ }))

    expect(
      screen.getByText('该渠道还没有已认证且可卖的模型，请联系运营配置'),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: '下一步' })).toHaveProperty('disabled', true)
  })

  it('never lets a third-party vendor be selected', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    const thirdParty = screen.getByRole('radio', { name: /tipig/ }) as HTMLInputElement
    expect(thirdParty.disabled).toBe(true)
    expect(thirdParty.checked).toBe(false)
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
    const pro = screen.getByRole('checkbox', {
      name: 'deepseek-v4-pro',
    }) as HTMLInputElement
    expect((flash as HTMLInputElement).checked).toBe(true)
    // verified_models narrows the selectable set.
    expect(pro.disabled).toBe(true)
    expect(pro.checked).toBe(false)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '下一步' })).toHaveProperty('disabled', false)
    })
  })
})
