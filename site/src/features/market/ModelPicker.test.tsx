import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ModelPicker } from './ModelPicker'
import type { SquareModel } from './api'

const models: SquareModel[] = [
  model('deepseek-flash', 'deepseek', ['DeepSeek-Flash', 'ds-flash'], 1_000_000),
  model('deepseek-v4-pro', 'deepseek', ['DeepSeek-V4-Pro'], 4_500_000),
  model('glm-5.3', 'zhipu', ['GLM-5.3', 'zhipu/glm-5.3'], 8_000_000),
  model('kimi-k3', 'moonshot', ['Kimi-K3'], 20_000_000),
]

function model(
  name: string,
  vendor: string,
  aliases: string[],
  input: number,
): SquareModel {
  return {
    model: name,
    aliases,
    origin_vendor: vendor,
    vendor,
    tier: 'flat',
    tier_label: '平价',
    prices: {
      flat: {
        input_micro_per_mtok: input,
        output_micro_per_mtok: input * 2,
        cache_read_micro_per_mtok: 0,
        cache_write_micro_per_mtok: 0,
      },
    },
    next_switch: null,
    listing_count: 1,
    channel_count: 1,
    buyer_price_source: 'market',
    generated_at: '2030-01-01T00:00:00Z',
  }
}

const vendors = [
  {
    id: 'deepseek',
    display_name: 'DeepSeek',
    tier: 'official',
    kind: 'official',
    logo_url: '/vendors/deepseek.svg',
  },
  {
    id: 'zhipu',
    display_name: '智谱',
    tier: 'official',
    kind: 'official',
    logo_url: '/vendors/zhipu.svg',
  },
  { id: 'moonshot', display_name: '月之暗面', tier: 'official', kind: 'official' },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.startsWith('/api/marketplace/square/models')) {
    return jsonResponse({ success: true, data: models })
  }
  if (url.startsWith('/api/marketplace/vendors')) {
    return jsonResponse({ success: true, data: vendors })
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

function renderPicker(selection: Record<string, string>, onChange = vi.fn()) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <ModelPicker selection={selection} onChange={onChange} />
    </QueryClientProvider>,
  )
  return { onChange }
}

describe('ModelPicker', () => {
  it('filters by origin vendor and by standard name/alias only', async () => {
    renderPicker({})
    expect(await screen.findByText('deepseek-flash')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: /智谱/ }))
    expect(screen.getByText('glm-5.3')).toBeTruthy()
    expect(screen.queryByText('deepseek-flash')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: /^全部/ }))
    const search = screen.getByPlaceholderText('搜索模型名称或别名')
    fireEvent.change(search, { target: { value: 'glm' } })
    expect(screen.getByText('glm-5.3')).toBeTruthy()
    expect(screen.queryByText('deepseek-flash')).toBeNull()

    // Vendor/channel words must not match; only names and aliases do.
    fireEvent.change(search, { target: { value: 'moonshot' } })
    expect(screen.queryByText('kimi-k3')).toBeNull()

    fireEvent.change(search, { target: { value: 'ds-flash' } })
    expect(screen.getByText('deepseek-flash')).toBeTruthy()
    expect(screen.queryByText('deepseek-v4-pro')).toBeNull()
  })

  it('selects or clears a whole vendor group and disables caps until checked', async () => {
    const { onChange } = renderPicker({})
    expect(await screen.findByText('deepseek-flash')).toBeTruthy()

    const cap = screen.getByLabelText('deepseek-flash 上限') as HTMLInputElement
    expect(cap.disabled).toBe(true)

    const groupToggle = screen.getByLabelText(/DeepSeek 0 \/ 2/) as HTMLInputElement
    fireEvent.click(groupToggle)
    const first = onChange.mock.calls.at(-1)?.[0] as Record<string, string>
    expect(Object.keys(first).sort()).toEqual(['deepseek-flash', 'deepseek-v4-pro'])

    cleanup()
    renderPicker({ 'deepseek-flash': '0.8' })
    const partial = (await screen.findByLabelText(/DeepSeek 1 \/ 2/)) as HTMLInputElement
    await waitFor(() => expect(partial.indeterminate).toBe(true))
    const enabledCap = screen.getByLabelText('deepseek-flash 上限') as HTMLInputElement
    expect(enabledCap.disabled).toBe(false)
  })

  it('shows vendor logos, counts, and a monogram fallback', async () => {
    renderPicker({})
    expect(await screen.findByText('deepseek-flash')).toBeTruthy()

    const deepseek = screen.getByRole('button', { name: /DeepSeek 2 个模型/ })
    expect(deepseek.querySelector('img')?.getAttribute('src')).toBe('/vendors/deepseek.svg')

    const kimi = screen.getByRole('button', { name: /Kimi 1 个模型/ })
    expect(kimi.querySelector('img')).toBeNull()
    expect(within(kimi).getByText('K')).toBeTruthy()
  })

  it('shows the two different empty states', async () => {
    renderPicker({})
    expect(await screen.findByText('deepseek-flash')).toBeTruthy()
    fireEvent.change(screen.getByPlaceholderText('搜索模型名称或别名'), {
      target: { value: 'no-such-model' },
    })
    expect(screen.getByText('没有匹配的模型')).toBeTruthy()
    expect(screen.getByRole('button', { name: '清除筛选' })).toBeTruthy()
  })
})
