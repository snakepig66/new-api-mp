import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { CreateKeyDialog } from './CreateKeyDialog'

const squareModels = [
  {
    model: 'deepseek-flash',
    aliases: ['DeepSeek-Flash'],
    origin_vendor: 'deepseek',
    vendor: 'deepseek',
    tier: 'flat',
    tier_label: '平价',
    prices: {
      flat: {
        input_micro_per_mtok: 1_000_000,
        output_micro_per_mtok: 4_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: null,
    listing_count: 1,
    channel_count: 1,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00Z',
  },
  {
    model: 'glm-5.3',
    aliases: ['GLM-5.3'],
    origin_vendor: 'zhipu',
    vendor: 'zhipu',
    tier: 'flat',
    tier_label: '平价',
    prices: {
      flat: {
        input_micro_per_mtok: 8_000_000,
        output_micro_per_mtok: 28_000_000,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: null,
    listing_count: 1,
    channel_count: 1,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00Z',
  },
]

const vendors = [
  { id: 'deepseek', display_name: 'DeepSeek', tier: 'official', kind: 'official' },
  { id: 'zhipu', display_name: '智谱', tier: 'official', kind: 'official' },
]

const existingKey = {
  id: 7,
  user_id: 1,
  key: 'sk-masked',
  status: 1,
  name: 'team-key',
  created_time: 1,
  accessed_time: 1,
  expired_time: -1,
  remain_quota: 5_000_000,
  unlimited_quota: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: null,
  used_quota: 0,
  group: '',
  cross_group_retry: false,
}

const existingPref = {
  token_id: 7,
  strategy: 'cheapest',
  model_scope: 'selected',
  price_cap_milli: 850,
  prefer_seller_ids: [],
  block_seller_ids: [],
  only_seller_id: 0,
  allow_fallback: false,
  rpm: 60,
  official_only: true,
  models: [{ model: 'glm-5.3', price_cap_milli: 800, added_at: 1 }],
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

let createdBody: Record<string, unknown> | null = null

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/square/models')) {
    return jsonResponse({ success: true, data: squareModels })
  }
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: vendors })
  }
  if (url.startsWith('/api/token/?') || url === '/api/token/') {
    return jsonResponse({
      success: true,
      data: { page: 1, page_size: 100, total: 1, items: [existingKey] },
    })
  }
  if (url.startsWith('/api/marketplace/key/pref')) {
    return jsonResponse({ success: true, data: existingPref })
  }
  if (url.startsWith('/api/marketplace/key') && init?.method === 'POST') {
    createdBody = JSON.parse(String(init.body)) as Record<string, unknown>
    return jsonResponse({
      success: true,
      data: {
        id: 99,
        name: 'Key 1',
        key: 'sk-new-key',
        model_scope: 'all',
        models: [],
        strategy: 'balanced',
      },
    })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  createdBody = null
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderDialog() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <CreateKeyDialog open onOpenChange={() => undefined} onCreated={() => undefined} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('CreateKeyDialog', () => {
  it('submits model_scope=all by default', async () => {
    renderDialog()
    fireEvent.click(screen.getByRole('button', { name: '创建 Key' }))

    await waitFor(() => expect(createdBody).not.toBeNull())
    expect(createdBody?.model_scope).toBe('all')
    expect(createdBody?.models).toEqual([])
  })

  it('refuses selected scope with no models', async () => {
    renderDialog()
    fireEvent.click(await screen.findByRole('button', { name: '仅指定模型' }))
    fireEvent.click(screen.getByRole('button', { name: '创建 Key' }))

    expect(await screen.findByText('请至少勾选一个模型')).toBeTruthy()
    expect(createdBody).toBeNull()
  })

  it('copies settings and models from an existing key', async () => {
    renderDialog()
    fireEvent.click(await screen.findByText('自定义设置'))

    const select = await screen.findByLabelText('从已有 Key 复制设置')
    await waitFor(() => expect(select.querySelectorAll('option')).toHaveLength(2))
    fireEvent.change(select, { target: { value: '7' } })

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '仅指定模型' }).getAttribute('aria-pressed')).toBe(
        'true',
      )
    })
    const cap = (await screen.findByLabelText('glm-5.3 上限')) as HTMLInputElement
    expect(cap.value).toBe('0.8')
    expect((screen.getByLabelText('路由策略') as HTMLSelectElement).value).toBe('cheapest')
  })

  it('submits a custom expiry and IP whitelist from the custom settings', async () => {
    renderDialog()
    fireEvent.click(await screen.findByText('自定义设置'))

    fireEvent.click(screen.getByLabelText('永不过期'))
    const future = new Date(Date.now() + 7 * 24 * 3600 * 1000)
    const pad = (value: number) => String(value).padStart(2, '0')
    const localValue = `${future.getFullYear()}-${pad(future.getMonth() + 1)}-${pad(future.getDate())}T${pad(future.getHours())}:${pad(future.getMinutes())}`
    fireEvent.change(screen.getByLabelText('过期时间'), { target: { value: localValue } })
    fireEvent.change(screen.getByLabelText('IP 白名单'), {
      target: { value: '203.0.113.10\n198.51.100.0/24' },
    })

    fireEvent.click(screen.getByRole('button', { name: '创建 Key' }))

    await waitFor(() => expect(createdBody).not.toBeNull())
    expect(createdBody?.expired_time).toBe(Math.floor(new Date(localValue).getTime() / 1000))
    expect(createdBody?.allow_ips).toBe('203.0.113.10\n198.51.100.0/24')
  })
})
