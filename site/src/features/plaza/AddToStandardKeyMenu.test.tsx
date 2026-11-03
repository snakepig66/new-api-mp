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

import { applyAuthBundle, clearSession } from '@/lib/session'
import { ToastProvider } from '@/ui'

import { AddToStandardKeyMenu } from './AddToStandardKeyMenu'

const MODEL = 'glm-4-plus'

const tokens = [
  standard(1, 'alpha', false, ''),
  standard(2, 'beta', true, 'gpt-4o'),
  standard(3, 'gamma-all', false, ''),
  standard(4, 'delta-added', true, `${MODEL},gpt-4o`),
  standard(6, 'epsilon', true, ''),
  { ...standard(5, 'shared-key', false, ''), key_type: 'shared' as const },
]

function standard(id: number, name: string, limitsEnabled: boolean, limits: string) {
  return {
    id,
    user_id: 1,
    key: 'sk-masked',
    status: 1,
    name,
    created_time: 1,
    accessed_time: 1,
    expired_time: -1,
    remain_quota: 0,
    unlimited_quota: true,
    model_limits_enabled: limitsEnabled,
    model_limits: limits,
    allow_ips: null,
    used_quota: 0,
    group: '',
    cross_group_retry: false,
    key_type: 'standard' as const,
  }
}

function pref(tokenId: number) {
  return {
    token_id: tokenId,
    strategy: 'balanced',
    model_scope: 'all',
    price_cap_milli: 0,
    prefer_seller_ids: [],
    block_seller_ids: [],
    only_seller_id: 0,
    allow_fallback: false,
    rpm: 0,
    official_only: false,
    models: [],
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

let updatedBodies: Record<string, unknown>[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.includes('/api/user/auth/refresh')) {
    return jsonResponse({ success: false })
  }
  if (url.startsWith('/api/token/') && init?.method === 'PUT') {
    const body = JSON.parse(String(init.body)) as Record<string, unknown>
    updatedBodies.push(body)
    return jsonResponse({ success: true, data: body })
  }
  if (url.startsWith('/api/token/')) {
    return jsonResponse({
      success: true,
      data: { page: 1, page_size: 100, total: tokens.length, items: tokens },
    })
  }
  if (url.startsWith('/api/marketplace/key/pref')) {
    const tokenId = Number(new URL(url, 'http://localhost').searchParams.get('token_id'))
    return jsonResponse({ success: true, data: pref(tokenId) })
  }
  if (url.startsWith('/api/marketplace/key/models')) {
    return jsonResponse({ success: true, data: { models: [] } })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  updatedBodies = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
  clearSession()
})

afterEach(() => {
  cleanup()
  clearSession()
  vi.unstubAllGlobals()
})

function renderMenu() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const plazaRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/plaza',
    component: () => <AddToStandardKeyMenu model={MODEL} />,
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

function loginAsBuyer() {
  applyAuthBundle({
    access_token: 'test-access-token',
    user: { id: 1, username: 'buyer', role: 1 },
  })
}

describe('AddToStandardKeyMenu', () => {
  it('sends a logged-out click to login with the current URL as redirect', async () => {
    const router = renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(router.state.location.search).toEqual({ redirect: '/plaza' })
  })

  it('marks keys without limits as 已可用（全部模型） and already-added keys as 已加入', async () => {
    loginAsBuyer()
    renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))

    await screen.findByRole('checkbox', { name: /alpha/ })

    const allday = screen.getByRole('checkbox', { name: /gamma-all/ }) as HTMLInputElement
    expect(allday.checked).toBe(true)
    expect(allday.disabled).toBe(true)
    const added = screen.getByRole('checkbox', { name: /delta-added/ }) as HTMLInputElement
    expect(added.checked).toBe(true)
    expect(added.disabled).toBe(true)

    // Standard-key label is used, and shared keys are filtered out.
    expect(screen.getAllByText('已可用（全部模型）').length).toBeGreaterThan(0)
    expect(screen.queryByText(/shared-key/)).toBeNull()
  })

  it('appends the model to model_limits of the selected standard keys', async () => {
    loginAsBuyer()
    renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))
    await screen.findByRole('checkbox', { name: /alpha/ })

    fireEvent.click(screen.getByRole('checkbox', { name: /beta/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /epsilon/ }))
    fireEvent.click(screen.getByRole('button', { name: '加入（2）' }))

    await waitFor(() => expect(updatedBodies).toHaveLength(2))
    const byId = new Map(updatedBodies.map((body) => [body.id as number, body]))
    // Existing limits are preserved and the model is appended.
    expect(byId.get(2)?.model_limits).toBe(`gpt-4o,${MODEL}`)
    expect(byId.get(2)?.model_limits_enabled).toBe(true)
    expect(byId.get(6)?.model_limits).toBe(MODEL)
    // The full token row is sent so other fields are not wiped.
    expect(byId.get(2)?.name).toBe('beta')
    expect(byId.get(2)?.unlimited_quota).toBe(true)
  })

  it('opens the create-key dialog with a standard key preselected', async () => {
    loginAsBuyer()
    renderMenu()
    fireEvent.click(await screen.findByRole('button', { name: '加入 Key' }))
    await screen.findByRole('dialog')
    fireEvent.click(await screen.findByRole('button', { name: '新建普通密钥' }))

    expect(await screen.findByText('新建 API Key')).toBeTruthy()
    expect(screen.getByText('普通密钥')).toBeTruthy()
  })
})
