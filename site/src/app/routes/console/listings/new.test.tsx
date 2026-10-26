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
    id: 'volcengine-ark-coding-plan',
    display_name: '火山方舟 Coding Plan',
    tier: 'plan',
    kind: 'plan',
    protocols: {
      openai: 'https://ark.cn-beijing.volces.com/api/coding/v3',
      anthropic: '',
    },
    models: ['deepseek-v4-pro'],
    passthrough_body: true,
    quota_window: { period_seconds: 18000, reset_timezone: 'Asia/Shanghai' },
  },
]

const commonAddresses = [
  {
    host: 'api.deepseek.com',
    vendor_name: 'DeepSeek',
    kind: 'official',
    endpoints: ['openai_chat', 'anthropic_messages'],
  },
]

const trustedPool = {
  matched: true,
  trust: 'trusted',
  kind: 'official',
  vendor_name: 'DeepSeek',
  endpoints: ['openai_chat', 'anthropic_messages'],
  badge_text: 'DeepSeek · 官方 · OpenAI + Anthropic · 免审核',
}

const unlistedPool = {
  matched: false,
  trust: 'unlisted',
  kind: '',
  vendor_name: '',
  endpoints: [],
  badge_text: '未收录地址 · 需人工审核',
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/seller/vendors')) {
    return jsonResponse({ success: true, data: vendors })
  }
  if (url.startsWith('/api/marketplace/seller/common-addresses')) {
    return jsonResponse({ success: true, data: commonAddresses })
  }
  if (url.startsWith('/api/marketplace/status')) {
    return jsonResponse({
      success: true,
      data: { enabled: true, version: 'test', platform_fee_percent: 7 },
    })
  }
  if (url.startsWith('/api/marketplace/square/models/')) {
    return jsonResponse({
      success: true,
      data: {
        model: decodeURIComponent(url.split('/').pop() ?? ''),
        tier: 'flat',
        official_prices: { flat: { input_micro_per_mtok: 1_000_000, output_micro_per_mtok: 2_000_000 } },
        multiplier_min_milli: 300,
        multiplier_max_milli: 1000,
        channels: [],
      },
    })
  }
  if (url === '/api/marketplace/seller/listings' && init?.method === 'POST') {
    return jsonResponse({ success: true, data: { id: 91, status: 'draft' } })
  }
  if (url.startsWith('/api/marketplace/seller/listings') && init?.method !== 'POST') {
    return jsonResponse({ success: true, data: [] })
  }
  if (url.startsWith('/api/marketplace/seller/listings/probe')) {
    const body = init?.body
      ? (JSON.parse(String(init.body)) as { api_key?: string; custom_base_url?: string })
      : {}
    const custom = Boolean(body.custom_base_url)
    const bad = String(body.api_key ?? '').includes('bad')
    const models = ['deepseek-flash', 'deepseek-v4-pro']
    return jsonResponse({
      success: true,
      data: {
        ok: !bad,
        models,
        verified_models: bad ? models : ['deepseek-flash'],
        coefficients: { 'deepseek-flash': 1000 },
        missing_models: [],
        latency_ms: 120,
        prompt_cache: true,
        balance_micro: 10_000_000,
        key_valid: !bad,
        fail_reason: bad ? 'key rejected by upstream' : '',
        trust_pool: custom ? unlistedPool : trustedPool,
      },
    })
  }
  return jsonResponse({ success: false, message: 'not found' }, 404)
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
  const dummy = ['/seller', '/seller/listings', '/seller/listings/$id'].map((path) =>
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

async function fillAddressAndKeys(url: string, keys: string) {
  renderPage()
  const urlInput = await screen.findByLabelText('上游地址')
  fireEvent.change(urlInput, { target: { value: url } })
  const keyInput = screen.getByLabelText('上游 API Key')
  fireEvent.change(keyInput, { target: { value: keys } })
}

async function clickNext() {
  const next = screen.getByRole('button', { name: '下一步' })
  await waitFor(() => expect(next).toHaveProperty('disabled', false), { timeout: 4000 })
  fireEvent.click(next)
}

async function advance(steps: number) {
  for (let index = 0; index < steps; index += 1) {
    await clickNext()
  }
}

async function reachVerify(url = 'https://api.deepseek.com', keys = 'sk-good-1234567890') {
  await fillAddressAndKeys(url, keys)
  await advance(4)
  await screen.findByRole('heading', { level: 2, name: '验真' })
}

describe('new listing wizard: five steps', () => {
  it('walks forward through all five steps and back', async () => {
    await fillAddressAndKeys('https://api.deepseek.com', 'sk-good-1234567890')
    await advance(1)
    expect(await screen.findByRole('heading', { level: 2, name: '模型' })).toBeTruthy()
    await advance(1)
    expect(await screen.findByRole('heading', { level: 2, name: '限制' })).toBeTruthy()
    await advance(1)
    expect(await screen.findByRole('heading', { level: 2, name: '高级' })).toBeTruthy()
    await advance(1)
    expect(await screen.findByRole('heading', { level: 2, name: '验真' })).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '上一步' }))
    expect(await screen.findByRole('heading', { level: 2, name: '高级' })).toBeTruthy()
  })

  it('fills a common address, probes it and shows the trust-pool badge', async () => {
    renderPage()
    const address = await screen.findByRole('button', { name: /DeepSeek/ })
    fireEvent.click(address)
    const urlInput = screen.getByLabelText('上游地址') as HTMLInputElement
    expect(urlInput.value).toBe('https://api.deepseek.com')

    fireEvent.change(screen.getByLabelText('上游 API Key'), {
      target: { value: 'sk-good-1234567890' },
    })

    expect(await screen.findByText(trustedPool.badge_text, {}, { timeout: 4000 })).toBeTruthy()
    const probeCall = fetchMock.mock.calls.find(
      ([url, init]) => String(url).includes('/listings/probe') && init?.method === 'POST',
    )
    const body = JSON.parse(String(probeCall?.[1]?.body))
    expect(body.vendor_id).toBe('deepseek')
    expect(body.api_key).toBe('sk-good-1234567890')
  })

  it('shows the model table, applies a batch multiplier and previews the buyer price', async () => {
    await fillAddressAndKeys('https://api.deepseek.com', 'sk-good-1234567890')
    await advance(1)

    const flash = await screen.findByRole('checkbox', { name: 'deepseek-flash' })
    expect((flash as HTMLInputElement).checked).toBe(true)
    expect(screen.getByText('接口')).toBeTruthy()

    const batch = screen.getByLabelText('批量倍率') as HTMLInputElement
    fireEvent.change(batch, { target: { value: '0.3' } })
    fireEvent.click(screen.getByRole('button', { name: '应用到全部' }))

    const multiplier = screen.getByLabelText('deepseek-flash 的倍率') as HTMLInputElement
    expect(multiplier.value).toBe('0.3')
    expect((await screen.findAllByText('¥0.30', {}, { timeout: 4000 })).length).toBeGreaterThan(0)
  })

  it('collects limits and hours before the advanced step', async () => {
    await fillAddressAndKeys('https://api.deepseek.com', 'sk-good-1234567890')
    await advance(2)

    const concurrency = screen.getByLabelText('并发数') as HTMLInputElement
    const rpm = screen.getByLabelText('RPM') as HTMLInputElement
    const daily = screen.getByLabelText('日限额（元）') as HTMLInputElement
    fireEvent.change(concurrency, { target: { value: '8' } })
    fireEvent.change(rpm, { target: { value: '120' } })
    fireEvent.change(daily, { target: { value: '20' } })
    expect(concurrency.value).toBe('8')
    expect(rpm.value).toBe('120')
    expect(daily.value).toBe('20')

    const weekday = screen.getByRole('checkbox', { name: '启用工作日时段' })
    fireEvent.click(weekday)
    expect((weekday as HTMLInputElement).checked).toBe(true)

    await clickNext()
    expect(await screen.findByRole('heading', { level: 2, name: '高级' })).toBeTruthy()
  })

  it('keeps advanced collapsed and locks headers for a listed address', async () => {
    await fillAddressAndKeys('https://api.deepseek.com', 'sk-good-1234567890')
    await advance(3)

    expect(screen.queryByLabelText('请求头')).toBeNull()
    const expand = screen.getByRole('button', { name: '展开高级设置' })
    expect(expand.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(expand)
    expect(expand.getAttribute('aria-expanded')).toBe('true')
    expect(await screen.findByText('已收录地址不允许自定义请求头。')).toBeTruthy()
    expect(screen.queryByLabelText('请求头')).toBeNull()
  })

  it('unlocks request headers for an unlisted address', async () => {
    await fillAddressAndKeys('https://unlisted.example.com/v1', 'sk-good-1234567890')
    await advance(3)
    fireEvent.click(screen.getByRole('button', { name: '展开高级设置' }))
    expect(await screen.findByLabelText('请求头')).toBeTruthy()
  })
})

describe('new listing wizard: verification', () => {
  it('shows key × model × endpoint results and publishes only passing models', async () => {
    await reachVerify('https://api.deepseek.com', 'sk-good-1234567890\nsk-bad-1234567890')

    expect(await screen.findByRole('heading', { level: 2, name: '验真' })).toBeTruthy()
    await waitFor(() => expect(screen.getAllByText('通过').length).toBeGreaterThan(0))
    expect(screen.getAllByText('未通过').length).toBeGreaterThan(0)
    expect(screen.getAllByText('key rejected by upstream').length).toBeGreaterThan(0)

    fireEvent.click(screen.getByRole('button', { name: '通过的先上架' }))
    await waitFor(() => {
      const calls = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === '/api/marketplace/seller/listings' && init?.method === 'POST',
      )
      expect(calls).toHaveLength(1)
      const body = JSON.parse(String(calls[0][1]?.body))
      expect(body.api_key).toBe('sk-good-1234567890')
      expect(body.models).toEqual(['deepseek-flash'])
      expect(body.vendor_id).toBe('deepseek')
    })
  })

  it('keeps every selected model as a draft for each key', async () => {
    await reachVerify('https://unlisted.example.com/v1', 'sk-bad-1234567890')

    fireEvent.click(await screen.findByRole('button', { name: '失败的保留为草稿' }))
    await waitFor(() => {
      const calls = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === '/api/marketplace/seller/listings' && init?.method === 'POST',
      )
      expect(calls).toHaveLength(1)
      const body = JSON.parse(String(calls[0][1]?.body))
      expect(body.api_key).toBe('sk-bad-1234567890')
      expect(body.models).toEqual(['deepseek-flash', 'deepseek-v4-pro'])
      expect(body.custom_base_url).toBe('https://unlisted.example.com/v1')
      expect(body.vendor_id).toBe('')
    })
  })
})
