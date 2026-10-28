import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { ListingEditDrawer } from '@/features/seller/ListingEditDrawer'

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const detail = {
  id: 7,
  vendor: 'deepseek',
  label: '主力渠道',
  models: ['deepseek-flash', 'deepseek-v4-pro', 'deepseek-reasoner'],
  multiplier_milli: 600,
  concurrency_limit: 5,
  rpm_limit: 60,
  daily_cap_micro: 0,
  hours: '[{"start":"09:00","end":"12:00"}]',
  status: 'live',
  key_fingerprint8: 'abcd1234',
  created_at: '2026-10-24T10:00:00+08:00',
  group_id: 3,
  weight: 120,
  verification: {
    ok: true,
    fail_reason: '',
    prompt_cache_support: true,
    latency_ms: 320,
    model_results: [
      {
        model: 'deepseek-flash',
        upstream_model: 'deepseek-flash',
        ok: true,
        prompt_cache_support: true,
        latency_ms: 320,
      },
    ],
    checked_at: '2026-10-25T08:00:00+08:00',
  },
  probes: [
    {
      id: 1,
      type: 'patrol',
      ok: true,
      latency_ms: 90,
      detail: { model_results: [{ ok: true, latency_ms: 210 }] },
      created_at: '2026-10-25T08:30:00+08:00',
    },
    {
      id: 2,
      type: 'verify',
      ok: false,
      latency_ms: 40,
      detail: { fail_reason: 'upstream timeout' },
      created_at: '2026-10-25T07:00:00+08:00',
    },
  ],
  group: {
    id: 3,
    vendor_slug: 'deepseek',
    key_strategy: 'weighted_round_robin',
    affinity_enabled: true,
    channel_concurrency_limit: 10,
    channel_rpm_limit: 100,
    channel_daily_cap_micro: 0,
    channel_cumulative_cap_micro: 0,
    key_count: 2,
    listings: [
      {
        id: 7,
        status: 'live',
        models: ['deepseek-flash'],
        multiplier_milli: 600,
        weight: 120,
        key_fingerprint8: 'abcd1234',
      },
      {
        id: 8,
        status: 'paused',
        models: ['deepseek-flash'],
        multiplier_milli: 500,
        weight: 100,
        key_fingerprint8: 'efgh5678',
      },
    ],
  },
}

const modelsResponse = {
  items: [
    {
      model: 'deepseek-flash',
      upstream_model: 'deepseek-flash',
      enabled: true,
      auto_disabled: false,
      disabled_reason: '',
      disabled_detail: '',
      disabled_upstream_status: 0,
      disabled_upstream_message: '',
      multiplier_milli: 600,
      effective_multiplier_milli: 600,
      pending_multiplier_milli: 0,
      coefficient_milli: 1000,
      origin_vendor: 'deepseek',
      multiplier_min_milli: 100,
      multiplier_max_milli: 1000,
      official_prices: {
        flat: {
          input_micro_per_mtok: 1_000_000,
          output_micro_per_mtok: 2_000_000,
          cache_read_micro_per_mtok: 100_000,
          cache_write_micro_per_mtok: 0,
        },
      },
      buyer_prices: {
        flat: {
          input_micro_per_mtok: 600_000,
          output_micro_per_mtok: 1_200_000,
          cache_read_micro_per_mtok: 60_000,
          cache_write_micro_per_mtok: 0,
        },
      },
    },
    {
      model: 'deepseek-v4-pro',
      upstream_model: 'deepseek-v4-pro',
      enabled: true,
      auto_disabled: false,
      disabled_reason: '',
      disabled_detail: '',
      disabled_upstream_status: 0,
      disabled_upstream_message: '',
      multiplier_milli: 500,
      effective_multiplier_milli: 500,
      pending_multiplier_milli: 700,
      pending_effective_at: '2026-10-27T21:00:00+08:00',
      coefficient_milli: 1000,
      origin_vendor: 'deepseek',
      multiplier_min_milli: 100,
      multiplier_max_milli: 1000,
      official_prices: {},
      buyer_prices: {},
    },
    {
      model: 'deepseek-reasoner',
      upstream_model: 'deepseek-reasoner',
      enabled: false,
      auto_disabled: true,
      disabled_reason: 'patrol_failed',
      disabled_detail: '',
      disabled_upstream_status: 500,
      disabled_upstream_message: 'upstream exploded',
      disabled_at: '2026-10-26T09:15:00+08:00',
      multiplier_milli: 400,
      effective_multiplier_milli: 0,
      pending_multiplier_milli: 0,
      coefficient_milli: 1000,
      origin_vendor: 'deepseek',
      multiplier_min_milli: 100,
      multiplier_max_milli: 1000,
      official_prices: {},
      buyer_prices: {},
    },
  ],
  platform_fee_percent: 7,
  multiplier_min_milli: 100,
  multiplier_max_milli: 1000,
}

const headersCustom = {
  custom_channel: true,
  headers: [
    { name: 'X-Upstream-Tag', value_masked: 'ab••••••••yz' },
    { name: 'X-Trace', value_masked: '12••••••••90' },
  ],
  pending: {
    version: 2,
    submitted_at: '2026-10-26T09:30:00+08:00',
    headers: [{ name: 'X-Upstream-Tag', value_masked: 'ab••••••••yz' }],
  },
  can_edit: true,
}

const headersPlain = {
  custom_channel: false,
  headers: [],
  can_edit: false,
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

interface RecordedRequest {
  method: string
  url: string
  body: Record<string, unknown> | null
}

let recorded: RecordedRequest[] = []
let headersFixture: typeof headersCustom | typeof headersPlain = headersCustom
/** deepseek-flash 的可变状态：降价立即落库，涨价进入待生效。 */
let flashState: {
  multiplier_milli: number
  pending_multiplier_milli: number
  pending_effective_at?: string
}

function flashRow() {
  return modelsResponse.items.map((item) =>
    item.model === 'deepseek-flash' ? { ...item, ...flashState } : item,
  )
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  const method = init?.method ?? 'GET'
  let body: Record<string, unknown> | null = null
  if (init?.body) {
    try {
      body = JSON.parse(String(init.body)) as Record<string, unknown>
    } catch {
      body = null
    }
  }
  recorded.push({ method, url, body })

  if (url === '/api/marketplace/seller/listings/7' && method === 'GET') {
    return jsonResponse({ success: true, data: detail })
  }
  if (url === '/api/marketplace/seller/listings/7' && method === 'PUT') {
    return jsonResponse({ success: true, data: { ...detail, ...(body ?? {}) } })
  }
  if (url === '/api/marketplace/seller/listings/7/models' && method === 'GET') {
    return jsonResponse({ success: true, data: { ...modelsResponse, items: flashRow() } })
  }
  if (url === '/api/marketplace/seller/listings/7/headers' && method === 'GET') {
    return jsonResponse({ success: true, data: headersFixture })
  }
  if (url === '/api/marketplace/seller/listings/7/models/deepseek-flash' && method === 'PUT') {
    const requested = (body?.multiplier_milli as number | undefined) ?? flashState.multiplier_milli
    if (requested <= flashState.multiplier_milli) {
      flashState = { multiplier_milli: requested, pending_multiplier_milli: 0 }
    } else {
      flashState = {
        multiplier_milli: flashState.multiplier_milli,
        pending_multiplier_milli: requested,
        pending_effective_at: '2026-10-27T22:30:00+08:00',
      }
    }
    return jsonResponse({
      success: true,
      data: {
        model: 'deepseek-flash',
        enabled: true,
        multiplier_milli: flashState.multiplier_milli,
        pending_multiplier_milli: flashState.pending_multiplier_milli,
        pending_effective_at: flashState.pending_effective_at,
        auto_disabled: false,
        disabled_reason: '',
      },
    })
  }
  if (url === '/api/marketplace/seller/listings/7/models/restore' && method === 'POST') {
    return jsonResponse({
      success: true,
      data: {
        restored: 1,
        still_failing: [
          {
            listing_id: 7,
            model: 'deepseek-reasoner',
            code: 'MODEL_CHANGE_REJECTED',
            message: '上游仍然返回 500',
          },
        ],
        checked_at: '2026-10-27T10:00:00+08:00',
      },
    })
  }
  return jsonResponse({ success: false, message: 'not found' }, 404)
})

function requestsTo(matcher: (request: RecordedRequest) => boolean): RecordedRequest[] {
  return recorded.filter(matcher)
}

beforeEach(() => {
  recorded = []
  headersFixture = headersCustom
  flashState = { multiplier_milli: 600, pending_multiplier_milli: 0 }
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderDrawer() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ListingEditDrawer listingId={7} onClose={() => {}} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

async function rendered() {
  await screen.findByText('主力渠道')
  await screen.findAllByText('deepseek-flash')
}

describe('ListingEditDrawer', () => {
  it('renders the seven sections from mocked detail, models and headers data', async () => {
    renderDrawer()
    await rendered()

    const nav = screen.getByRole('navigation', { name: '编辑渠道区块导航' })
    for (const label of ['概览', '基本信息', '密钥', '模型与价格', '限制', '验真与巡检', '高级']) {
      expect(within(nav).getByRole('button', { name: label })).toBeTruthy()
    }

    // 概览：状态、指纹、备注、创建时间、验真摘要
    expect(screen.getAllByText('在售').length).toBeGreaterThan(0)
    expect(screen.getAllByText('abcd1234').length).toBeGreaterThan(0)
    expect(screen.getByText('2026-10-24 10:00:00')).toBeTruthy()
    expect(screen.getByText('全部模型通过')).toBeTruthy()
    expect(screen.getByText('最近验真')).toBeTruthy()

    // 密钥区块
    expect(screen.getByRole('button', { name: '更换 Key' })).toBeTruthy()

    // 模型与价格：平台费说明 + 官方价/买家价/到手价
    expect(screen.getByText('到手价为买家价扣除 7% 平台费后的金额。')).toBeTruthy()
    expect(screen.getByText('¥1.00')).toBeTruthy() // 官方输入价
    expect(screen.getByText('¥0.60')).toBeTruthy() // 买家输入价 600_000
    expect(screen.getByText('¥1.116')).toBeTruthy() // 到手价 1_200_000 × 0.93

    // 限制区块：营业时段摘要
    expect(screen.getByText('09:00–12:00')).toBeTruthy()

    // 验真与巡检：探测记录
    expect(screen.getByText('巡检')).toBeTruthy()
    expect(screen.getByText('验真')).toBeTruthy()
    expect(screen.getAllByText('通过').length).toBeGreaterThan(0)

    // 高级：分组 + 请求头（自定义渠道）
    expect(screen.getByText('加权轮询')).toBeTruthy()
    expect(screen.getByText('X-Upstream-Tag')).toBeTruthy()
    expect(screen.getByText('ab••••••••yz')).toBeTruthy()
    expect(
      screen.getByText('待生效版本 v2（2026-10-26 09:30:00 提交），验真通过后生效。'),
    ).toBeTruthy()
  })

  it('saves only the dirty label field via updateListingSettings', async () => {
    renderDrawer()
    await rendered()

    const input = screen.getByLabelText('渠道备注')
    fireEvent.change(input, { target: { value: '新备注' } })

    const save = screen.getByRole('button', { name: '保存' }) as HTMLButtonElement
    expect(save.disabled).toBe(false)
    fireEvent.click(save)

    await waitFor(() => {
      const puts = requestsTo(
        (request) =>
          request.method === 'PUT' && request.url === '/api/marketplace/seller/listings/7',
      )
      expect(puts).toHaveLength(1)
      expect(puts[0].body).toEqual({ label: '新备注' })
    })
  })

  it('posts a multiplier decrease immediately and stages an increase', async () => {
    renderDrawer()
    await rendered()

    // 降价：600 → 400，走单模型接口
    const flashInput = screen.getByLabelText('deepseek-flash 的倍率')
    fireEvent.change(flashInput, { target: { value: '0.400' } })
    fireEvent.click(screen.getByRole('button', { name: '保存 deepseek-flash 的修改' }))

    await waitFor(() => {
      const puts = requestsTo(
        (request) =>
          request.method === 'PUT' &&
          request.url === '/api/marketplace/seller/listings/7/models/deepseek-flash',
      )
      expect(puts).toHaveLength(1)
      expect(puts[0].body).toEqual({ multiplier_milli: 400 })
    })
    // 降价立即生效：输入框回到新值（deepseek-v4-pro 的待生效提示与此无关）。
    await waitFor(() => expect((flashInput as HTMLInputElement).value).toBe('0.400'))

    // 涨价：400 → 800，同样的请求体形状，由后端转为待生效
    fireEvent.change(flashInput, { target: { value: '0.800' } })
    fireEvent.click(screen.getByRole('button', { name: '保存 deepseek-flash 的修改' }))

    await waitFor(() => {
      const puts = requestsTo(
        (request) =>
          request.method === 'PUT' &&
          request.url === '/api/marketplace/seller/listings/7/models/deepseek-flash',
      )
      expect(puts).toHaveLength(2)
      expect(puts[1].body).toEqual({ multiplier_milli: 800 })
    })
    // 涨价进入待生效：显示「HH:MM 起调整为 x.xxx」。
    expect(await screen.findByText('22:30 起调整为 0.800x')).toBeTruthy()
  })

  it('shows the auto-disabled reason block with mapped Chinese reason', async () => {
    renderDrawer()
    await rendered()

    expect(screen.getByText('已停用')).toBeTruthy()
    expect(screen.getByText('停用原因')).toBeTruthy()
    expect(screen.getByText('巡检未通过')).toBeTruthy()
    expect(screen.getByText('upstream exploded')).toBeTruthy()
    expect(screen.getByText('2026-10-26 09:15:00')).toBeTruthy()
  })

  it('shows the pending increase line from pending_multiplier fields', async () => {
    renderDrawer()
    await rendered()

    expect(screen.getByText('21:00 起调整为 0.700x')).toBeTruthy()
  })

  it('restore button reports restored count and still-failing models with reasons', async () => {
    renderDrawer()
    await rendered()

    fireEvent.click(screen.getByRole('button', { name: '恢复全部' }))

    await waitFor(() => {
      expect(
        requestsTo(
          (request) =>
            request.method === 'POST' &&
            request.url === '/api/marketplace/seller/listings/7/models/restore',
        ),
      ).toHaveLength(1)
    })
    expect(await screen.findByText('已恢复 1 个模型。')).toBeTruthy()
    expect(screen.getByText('1 个模型仍未通过检测：')).toBeTruthy()
    expect(screen.getByText(/上游仍然返回 500/)).toBeTruthy()
  })

  it('hides the headers section for a non-custom channel', async () => {
    headersFixture = headersPlain
    renderDrawer()
    await rendered()

    expect(screen.queryByText('请求头')).toBeNull()
    expect(screen.queryByRole('button', { name: '添加请求头' })).toBeNull()
    expect(screen.queryByRole('button', { name: '提交请求头' })).toBeNull()
    // 分组信息仍然展示
    expect(screen.getByText('加权轮询')).toBeTruthy()
  })

  it('renders masked header values only; raw values never appear in the DOM', async () => {
    renderDrawer()
    await rendered()

    expect(screen.getByText('ab••••••••yz')).toBeTruthy()
    expect(screen.getByText('12••••••••90')).toBeTruthy()
    // 编辑表单只用掩码初始化名称，值输入框为空（不回显任何原始值）。
    const nameInput = screen.getByLabelText('第 1 条请求头的名称') as HTMLInputElement
    const valueInput = screen.getByLabelText('第 1 条请求头的值') as HTMLInputElement
    expect(nameInput.value).toBe('X-Upstream-Tag')
    expect(valueInput.value).toBe('')
    expect(document.body.textContent).not.toContain('sk-live')
    expect(document.body.textContent).not.toContain('X-Upstream-Tag:secret')
  })
})
