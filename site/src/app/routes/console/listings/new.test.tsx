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

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: vendors })
  }
  if (url.startsWith('/api/marketplace/status')) {
    return jsonResponse({
      success: true,
      data: { enabled: true, version: 'test', platform_fee_percent: 7 },
    })
  }
  if (url === '/api/marketplace/seller/listings' && init?.method === 'POST') {
    return new Response(
      JSON.stringify({
        success: false,
        message: 'this api key is already listed by you',
        code: 'DUPLICATE_KEY_OWN',
        listing_id: 77,
      }),
      { status: 409, headers: { 'content-type': 'application/json' } },
    )
  }
  if (url.startsWith('/api/marketplace/seller/listings/probe')) {
    const body = init?.body ? (JSON.parse(String(init.body)) as { vendor_id?: string }) : {}
    const plan = body.vendor_id === 'volcengine-ark-coding-plan'
    const available = plan ? ['deepseek-v4-pro'] : ['deepseek-flash']
    return jsonResponse({
      success: true,
      data: {
        ok: true,
        models: ['deepseek-flash', 'deepseek-v4-pro'],
        verified_models: available,
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

describe('new listing wizard', () => {
  it('groups channels by kind and lets a third-party channel be selected', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    expect(screen.getByText('官方渠道')).toBeTruthy()
    expect(screen.getByText('套餐渠道')).toBeTruthy()
    expect(screen.getByText('第三方渠道')).toBeTruthy()
    expect(screen.getByText('tipig gateway')).toBeTruthy()

    const thirdParty = screen.getByRole('radio', { name: /tipig/ }) as HTMLInputElement
    expect(thirdParty.disabled).toBe(false)
  })

  it('blocks next when the selected channel has no priced models', async () => {
    renderPage()
    await screen.findByText('火山方舟')

    const volcengine = screen
      .getAllByRole('radio')
      .find((radio) => (radio as HTMLInputElement).value === 'volcengine') as HTMLInputElement
    fireEvent.click(volcengine)

    expect(
      screen.getByText('该渠道还没有已认证且可卖的模型，请联系运营配置'),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: '下一步' })).toHaveProperty('disabled', true)
  })

  it('lets a third-party vendor be selected', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    const thirdParty = screen.getByRole('radio', { name: /tipig/ }) as HTMLInputElement
    expect(thirdParty.disabled).toBe(false)
    expect(thirdParty.checked).toBe(false)

    fireEvent.click(thirdParty)
    expect(thirdParty.checked).toBe(true)
  })

  it('requires the plan-terms checkbox before submitting a plan channel', async () => {
    renderPage()
    await screen.findByText('火山方舟 Coding Plan')

    fireEvent.click(screen.getByRole('radio', { name: /火山方舟 Coding Plan/ }))
    expect(screen.getByText('套餐额度说明')).toBeTruthy()
    expect(
      screen.getByText(/每 5 小时重置一次（Asia\/Shanghai）/),
    ).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    const key = await screen.findByLabelText('上游 API Key')
    fireEvent.change(key, { target: { value: 'sk-plan-1234567890' } })
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    await screen.findByRole('checkbox', { name: 'deepseek-v4-pro' })
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    const terms = await screen.findByRole('checkbox', {
      name: '我确认该套餐条款允许共享',
    }) as HTMLInputElement
    const submit = screen.getByRole('button', { name: '提交渠道' }) as HTMLButtonElement
    expect(terms.checked).toBe(false)
    expect(submit.disabled).toBe(true)

    fireEvent.click(terms)
    await waitFor(() => expect(submit.disabled).toBe(false))
  })

  it('links to the existing listing when the seller already listed the key', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    fireEvent.click(screen.getByRole('radio', { name: /DeepSeek/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    const key = await screen.findByLabelText('上游 API Key')
    fireEvent.change(key, { target: { value: 'sk-duplicate-1234567890' } })
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    await screen.findByRole('checkbox', { name: 'deepseek-flash' })
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.click(await screen.findByRole('button', { name: '提交渠道' }))

    expect(
      await screen.findByText('你自己已上架过这个 Key（渠道 #77）。'),
    ).toBeTruthy()
    const link = screen.getByRole('link', { name: '查看已有渠道' }) as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe('/console/listings/77')
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

describe('new listing wizard: custom upstream', () => {
  it('gates step 0 on a valid custom URL and submits custom_base_url', async () => {
    renderPage()
    await screen.findByText('DeepSeek')

    const customRadio = screen
      .getAllByRole('radio')
      .find((radio) => (radio as HTMLInputElement).value === 'custom') as HTMLInputElement
    fireEvent.click(customRadio)

    const urlInput = (await screen.findByLabelText('自定义上游地址')) as HTMLInputElement
    fireEvent.change(urlInput, { target: { value: 'http://api.example.com' } })
    fireEvent.blur(urlInput)
    expect(
      await screen.findByText('请填写 https 开头的完整域名地址（可带 /v1 等路径）。'),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: '下一步' })).toHaveProperty('disabled', true)

    fireEvent.change(urlInput, { target: { value: 'https://api.example.com/v1' } })
    fireEvent.blur(urlInput)
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    const keyInput = (await screen.findByLabelText(/上游 API Key/)) as HTMLInputElement
    fireEvent.change(keyInput, { target: { value: 'sk-custom-12345678' } })
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    // The probe names the custom upstream, not a registered channel.
    await waitFor(() => {
      const probeCall = fetchMock.mock.calls.find(
        ([url, init]) => String(url).includes('/listings/probe') && init?.method === 'POST',
      )
      const body = JSON.parse(String(probeCall?.[1]?.body))
      expect(body.custom_base_url).toBe('https://api.example.com/v1')
    })

    // The probe's callable model is auto-selected; advance to the pricing step.
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.click(await screen.findByRole('button', { name: '提交渠道' }))

    await waitFor(() => {
      const createCall = fetchMock.mock.calls.find(
        ([url, init]) =>
          String(url) === '/api/marketplace/seller/listings' && init?.method === 'POST',
      )
      const body = JSON.parse(String(createCall?.[1]?.body))
      expect(body.custom_base_url).toBe('https://api.example.com/v1')
      expect(body.vendor_id).toBe('')
      expect(body.models).toEqual(['deepseek-flash'])
    })
  })
})
