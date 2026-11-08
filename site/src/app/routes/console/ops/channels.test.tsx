import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { api as legacyApi } from '@/legacy/lib/http-client'

import { Route } from './channels'

legacyApi.defaults.baseURL = 'http://localhost'

const ChannelsComponent = Route.options.component!

const channels = [
  {
    id: 7,
    type: 1,
    name: 'OpenAI 主渠道',
    status: 1,
    base_url: 'https://api.openai.com',
    models: 'gpt-4o,gpt-4o-mini',
    model_mapping: null,
    group: 'default',
    priority: 10,
    weight: 0,
    response_time: 321,
    test_time: 1767225600,
    test_model: null,
    created_time: 1767225600,
    setting: '{"proxy":""}',
    other: '',
    settings: '',
  },
  {
    id: 8,
    type: 14,
    name: 'Claude 备用',
    status: 3,
    base_url: 'https://api.anthropic.com',
    models: 'claude-3-5-sonnet',
    model_mapping: null,
    group: 'default',
    priority: 0,
    weight: 0,
    response_time: 0,
    test_time: 0,
    test_model: null,
    created_time: 1767225600,
    setting: '',
    other: '',
    settings: '',
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const calls: string[] = []
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input instanceof Request ? input.url : input)
  calls.push(url)
  if (url.includes('/api/channel/ops')) {
    return jsonResponse({ success: true, data: {} })
  }
  if (url.includes('/api/group/')) {
    return jsonResponse({ success: true, data: ['default'] })
  }
  if (url.includes('/api/channel/search')) {
    return jsonResponse({
      success: true,
      data: { items: channels, total: channels.length },
    })
  }
  if (url.includes('/api/channel/') || url.includes('/api/channel?')) {
    return jsonResponse({
      success: true,
      data: { items: channels, total: channels.length },
    })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })
  )
  window.localStorage.setItem(
    'token-market.auth.v1',
    JSON.stringify({
      accessToken: 'test-token',
      accessExpiresAt: 4102444800,
      sid: 'test-sid',
      user: { id: 1, username: 'root', role: 100 },
    })
  )
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  window.localStorage.clear()
  vi.unstubAllGlobals()
})

function renderChannels() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const channelsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/ops/channels',
    validateSearch: (search: Record<string, unknown>) => search,
    component: () => <ChannelsComponent />,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([channelsRoute]),
    history: createMemoryHistory({ initialEntries: ['/console/ops/channels'] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>
  )
}

describe('ported channel management', () => {
  // The ported legacy bundle takes ~30s to render in jsdom even unloaded
  // (P2: under full-suite parallel load it exceeded the 30s wait and flaked).
  it('renders the legacy channel list', { timeout: 180000 }, async () => {
    renderChannels()

    expect(
      await screen.findByText('OpenAI 主渠道', undefined, { timeout: 120000 })
    ).toBeTruthy()
    expect(screen.getByText('Claude 备用')).toBeTruthy()
    expect(calls.some((u) => u.includes('/api/channel?'))).toBe(true)
  })
})
