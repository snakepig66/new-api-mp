import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { SellerListingsPage } from './index'

vi.mock('@/features/seller/ListingEditDrawer', () => ({
  ListingEditDrawer: ({ listingId, onClose }: { listingId: number; onClose: () => void }) => (
    <div>
      <span>edit-drawer-{listingId}</span>
      <button type="button" onClick={onClose}>
        edit-drawer-close
      </button>
    </div>
  ),
}))

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const listings = [
  {
    id: 1,
    vendor: 'deepseek',
    label: '主力渠道',
    models: ['deepseek-flash'],
    model_count: 2,
    enabled_model_count: 2,
    auto_disabled_model_count: 0,
    multiplier_milli: 500,
    multiplier_min_milli: 500,
    multiplier_max_milli: 500,
    concurrency_limit: 10,
    rpm_limit: 60,
    daily_cap_micro: 0,
    hours: '',
    status: 'live',
    key_fingerprint8: 'abcdef12',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 2,
    vendor: 'deepseek',
    label: '备用渠道',
    models: ['deepseek-v4-pro'],
    multiplier_milli: 600,
    multiplier_min_milli: 500,
    multiplier_max_milli: 700,
    concurrency_limit: 10,
    rpm_limit: 60,
    daily_cap_micro: 0,
    hours: '',
    status: 'live',
    key_fingerprint8: 'bbbbbb22',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 3,
    vendor: 'deepseek',
    label: '套餐渠道',
    models: ['deepseek-flash'],
    multiplier_milli: 700,
    concurrency_limit: 5,
    rpm_limit: 30,
    daily_cap_micro: 0,
    hours: '',
    status: 'paused',
    pause_reason: 'plan_quota_exhausted',
    resume_at: '2026-10-24T11:00:00+08:00',
    key_fingerprint8: 'cccccc33',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 4,
    vendor: 'deepseek',
    label: '异常渠道',
    models: ['deepseek-flash'],
    model_count: 3,
    enabled_model_count: 1,
    auto_disabled_model_count: 2,
    multiplier_milli: 800,
    concurrency_limit: 5,
    rpm_limit: 30,
    daily_cap_micro: 0,
    hours: '',
    status: 'live',
    key_fingerprint8: 'dddddd44',
    created_at: '2026-10-24T10:00:00+08:00',
  },
  {
    id: 5,
    vendor: 'deepseek',
    label: '草稿渠道',
    models: ['deepseek-flash'],
    multiplier_milli: 400,
    concurrency_limit: 5,
    rpm_limit: 30,
    daily_cap_micro: 0,
    hours: '',
    status: 'draft',
    key_fingerprint8: 'eeeeee55',
    created_at: '2026-10-24T10:00:00+08:00',
  },
]

const health = [
  {
    listing_id: 1,
    latest_probe: {
      type: 'patrol',
      ok: false,
      latency_ms: 75,
      fail_reason: 'upstream 500',
      model_results: [],
      created_at: '2026-10-24T10:05:00+08:00',
    },
    today_requests: 200,
    today_success_rate_milli: 995,
    today_income_micro: 12_340_000,
    requests_1h: 100,
    success_rate_1h_milli: 990,
    requests_24h: 500,
    success_rate_24h_milli: -1,
    requests_7d: 5,
    success_rate_7d_milli: 980,
  },
  {
    listing_id: 2,
    latest_probe: null,
    today_requests: 10,
    today_success_rate_milli: 950,
    today_income_micro: 0,
    requests_1h: 3,
    success_rate_1h_milli: 950,
    requests_24h: 50,
    success_rate_24h_milli: 950,
  },
  {
    listing_id: 3,
    latest_probe: null,
    today_requests: 10,
    today_success_rate_milli: 800,
    today_income_micro: 0,
  },
]

const vendors = [
  {
    id: 'deepseek',
    display_name: 'DeepSeek',
    tier: 'official',
    kind: 'official',
    protocols: { openai: 'https://api.deepseek.com', anthropic: '' },
    models: [],
    passthrough_body: false,
  },
]

const multiplierBounds = {
  absolute_min_milli: 1,
  absolute_max_milli: 1000,
  kinds: {
    official: { min_milli: 100, max_milli: 1000 },
    plan: { min_milli: 100, max_milli: 1000 },
    third_party: { min_milli: 100, max_milli: 1000 },
  },
}

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = String(input)
  const method = init?.method ?? 'GET'
  if (url === '/api/marketplace/seller/listings/health') {
    return jsonResponse({ success: true, data: health })
  }
  if (url === '/api/marketplace/seller/listings' && method === 'GET') {
    return jsonResponse({ success: true, data: listings })
  }
  if (method === 'DELETE' && /^\/api\/marketplace\/seller\/listings\/\d+$/.test(url)) {
    return jsonResponse({ success: true, data: { id: Number(url.split('/').pop()), status: 'deleted' } })
  }
  if (url === '/api/marketplace/seller/listings/batch-models' && method === 'POST') {
    return jsonResponse({ success: true, data: { ok: true } })
  }
  if (url === '/api/marketplace/vendors') {
    return jsonResponse({ success: true, data: vendors })
  }
  if (url === '/api/marketplace/seller/multiplier-bounds') {
    return jsonResponse({ success: true, data: multiplierBounds })
  }
  return jsonResponse({ success: false, message: 'not found' })
  },
)


beforeEach(() => {
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderPage(initialSearch = '') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute()
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console/listings',
    validateSearch: (search: Record<string, unknown>): { edit?: number } => {
      const parsed = Number(search.edit)
      return { edit: Number.isInteger(parsed) && parsed > 0 ? parsed : undefined }
    },
    component: SellerListingsPage,
  })
  const dummy = ['/console/listings/new', '/console/listings/$id'].map((path) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: () => null }),
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, ...dummy]),
    history: createMemoryHistory({ initialEntries: [`/console/listings${initialSearch}`] }),
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

/** Renders the table view and waits for listings, health and vendors to resolve. */
async function renderLoadedTable(initialSearch = '') {
  renderPage(initialSearch)
  await screen.findByText('主力渠道')
  await screen.findByText('¥12.34')
  // Channel names/kind tags come from the vendors catalogue; wait for it so
  // vendor-derived accessible names are stable.
  expect((await screen.findAllByText('官方')).length).toBeGreaterThan(0)
}

function rowOf(name: string): HTMLElement {
  return screen.getByRole('link', { name }).closest('tr') as HTMLElement
}

function rowById(id: number): HTMLElement {
  const names: Record<number, string> = {
    1: '选择 主力渠道 #1',
    2: '选择 备用渠道 #2',
    3: '选择 套餐渠道 #3',
    4: '选择 异常渠道 #4',
    5: '选择 草稿渠道 #5',
  }
  return screen.getByRole('checkbox', { name: names[id] }).closest('tr') as HTMLElement
}

async function showCards() {
  fireEvent.click(screen.getByRole('radio', { name: '卡片' }))
}

describe('seller listings table view', () => {
  it('renders the channel, success-rate, models, multiplier, income and status columns', async () => {
    await renderLoadedTable()

    for (const header of ['渠道', '成功率', '模型', '倍率', '今日收入', '状态', '操作']) {
      expect(screen.getByRole('columnheader', { name: header })).toBeTruthy()
    }

    // 渠道: label + #id + kind tag.
    const row1 = rowOf('主力渠道')
    expect(within(row1).getByText('#1')).toBeTruthy()
    expect(within(row1).getByText('官方')).toBeTruthy()
    expect(within(row1).getByText('¥12.34')).toBeTruthy()
    expect(within(row1).getByText('在售')).toBeTruthy()

    // 成功率 windows: 99% / — / 98%.
    expect(within(row1).getByText('99%')).toBeTruthy()
    expect(within(row1).getByText('—')).toBeTruthy()
    expect(within(row1).getByText('98%')).toBeTruthy()

    // 模型: 启用/总数.
    expect(within(row1).getByText('2 / 2')).toBeTruthy()
    const row4 = rowById(4)
    expect(within(row4).getByText('1 / 3')).toBeTruthy()
    const dot = row4.querySelector('span[role="img"]')
    expect(dot?.getAttribute('aria-label')).toBe('2 个模型被自动停用')

    // 倍率 band renders both ends when min !== max.
    expect(within(rowById(2)).getByText('0.50x')).toBeTruthy()
    expect(within(rowById(2)).getByText('0.70x')).toBeTruthy()
  })

  it('renders an em dash for -1 success rates and windows with fewer than 5 requests', async () => {
    await renderLoadedTable()

    // Row 1: 24h window is -1.
    const row1 = rowOf('主力渠道')
    expect(within(row1).getAllByText('—')).toHaveLength(1)

    // Row 2: 1h has only 3 requests, 7d is missing.
    const row2 = rowById(2)
    expect(within(row2).getByText('95%')).toBeTruthy()
    expect(within(row2).getAllByText('—')).toHaveLength(2)

    // Rows without any health row show no sample in every window.
    const row3 = rowOf('套餐渠道')
    expect(within(row3).getAllByText('—')).toHaveLength(3)
  })

  it('filters the table by the status chips', async () => {
    await renderLoadedTable()

    const chip = (label: string) => screen.getByRole('button', { name: new RegExp(label) })
    expect(chip('全部').textContent).toContain('5')
    expect(chip('运行中').textContent).toContain('2')
    expect(chip('冷却中').textContent).toContain('1')
    expect(chip('待审核').textContent).toContain('1')
    expect(chip('异常').textContent).toContain('1')
    expect(document.querySelectorAll('tbody tr')).toHaveLength(5)

    fireEvent.click(chip('运行中'))
    expect(document.querySelectorAll('tbody tr')).toHaveLength(2)
    expect(screen.queryByText('套餐渠道')).toBeNull()
    expect(screen.queryByText('1 / 3')).toBeNull()

    fireEvent.click(chip('异常'))
    expect(document.querySelectorAll('tbody tr')).toHaveLength(1)
    expect(screen.getByText('1 / 3')).toBeTruthy()

    fireEvent.click(chip('冷却中'))
    expect(document.querySelectorAll('tbody tr')).toHaveLength(1)
    expect(screen.getByText('套餐渠道')).toBeTruthy()

    fireEvent.click(chip('待审核'))
    expect(document.querySelectorAll('tbody tr')).toHaveLength(1)
    expect(screen.getByText('待验真')).toBeTruthy()

    fireEvent.click(chip('全部'))
    expect(document.querySelectorAll('tbody tr')).toHaveLength(5)
  })

  it('disables the pause switch with the pause reason for platform pauses', async () => {
    await renderLoadedTable()

    const liveSwitch = within(rowOf('主力渠道')).getByRole('switch') as HTMLButtonElement
    expect(liveSwitch.disabled).toBe(false)
    expect(liveSwitch.getAttribute('aria-checked')).toBe('true')

    const pausedSwitch = within(rowOf('套餐渠道')).getByRole('switch') as HTMLButtonElement
    expect(pausedSwitch.disabled).toBe(true)
    expect(pausedSwitch.getAttribute('aria-checked')).toBe('false')
    expect(pausedSwitch.getAttribute('title')).toContain('套餐额度已用尽')
    expect(pausedSwitch.getAttribute('title')).toContain('预计 2026-10-24 11:00:00 自动恢复')

    const draftSwitch = within(rowById(5)).getByRole('switch') as HTMLButtonElement
    expect(draftSwitch.disabled).toBe(true)
  })

  it('opens the edit drawer from the edit button and closes it via the search param', async () => {
    await renderLoadedTable()

    fireEvent.click(within(rowOf('主力渠道')).getByRole('button', { name: '编辑' }))
    expect(await screen.findByText('edit-drawer-1')).toBeTruthy()

    fireEvent.click(screen.getByText('edit-drawer-close'))
    await waitFor(() => expect(screen.queryByText('edit-drawer-1')).toBeNull())
  })

  it('renders the drawer directly when the edit search param is present', async () => {
    await renderLoadedTable('?edit=3')
    expect(screen.getByText('edit-drawer-3')).toBeTruthy()

    fireEvent.click(screen.getByText('edit-drawer-close'))
    await waitFor(() => expect(screen.queryByText('edit-drawer-3')).toBeNull())
  })

  it('asks for confirmation before deleting a listing', async () => {
    await renderLoadedTable()

    fireEvent.click(within(rowOf('主力渠道')).getByRole('button', { name: '更多操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '删除' }))

    expect(screen.getByText('删除渠道')).toBeTruthy()
    expect(screen.getByText(/不可恢复/)).toBeTruthy()
    expect(screen.getByText(/历史账单与结算记录会保留/)).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '删除' }))

    await waitFor(() => {
      const deleted = fetchMock.mock.calls.some(
        ([input, init]) =>
          String(input) === '/api/marketplace/seller/listings/1' && init?.method === 'DELETE',
      )
      expect(deleted).toBe(true)
    })
    await screen.findByText('渠道已删除')
    await waitFor(() => expect(screen.queryByText('删除渠道')).toBeNull())
  })

  it('copies the listing config summary to the clipboard without the key', async () => {
    await renderLoadedTable()

    const writeText = vi.fn(async (_text: string) => undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    fireEvent.click(within(rowOf('主力渠道')).getByRole('button', { name: '更多操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '复制配置' }))

    await screen.findByText(/已复制到剪贴板/)
    expect(writeText).toHaveBeenCalledTimes(1)
    const summary = JSON.parse(String(writeText.mock.calls[0]?.[0])) as Record<string, unknown>
    expect(summary['models']).toEqual(['deepseek-flash'])
    expect(summary['multiplier_milli']).toBe(500)
    expect(summary['concurrency_limit']).toBe(10)
    expect(JSON.stringify(summary)).not.toContain('abcdef12')
    expect(JSON.stringify(summary)).not.toContain('api_key')
  })

  it('applies a batch multiplier to the selected listings', async () => {
    await renderLoadedTable()

    fireEvent.click(screen.getByRole('checkbox', { name: '选择 主力渠道 #1' }))
    fireEvent.click(screen.getByRole('checkbox', { name: '选择 备用渠道 #2' }))
    expect(screen.getByText('已选 2 个渠道')).toBeTruthy()

    fireEvent.change(screen.getByLabelText('倍率'), { target: { value: '0.5' } })
    fireEvent.click(screen.getByRole('button', { name: '应用' }))

    await screen.findByText('已批量设置倍率')
    const batchCall = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === '/api/marketplace/seller/listings/batch-models' &&
        init?.method === 'POST',
    )
    expect(batchCall).toBeTruthy()
    expect(JSON.parse(String(batchCall?.[1]?.body))).toEqual({
      listing_ids: [1, 2],
      multiplier_milli: 500,
    })
    // The bar clears after a successful batch.
    await waitFor(() => expect(screen.queryByText('已选 2 个渠道')).toBeNull())
  })

  it('shows per-row reasons when the batch is rejected server-side', async () => {
    await renderLoadedTable()

    fireEvent.click(screen.getByRole('checkbox', { name: '选择 主力渠道 #1' }))
    fireEvent.change(screen.getByLabelText('倍率'), { target: { value: '0.5' } })

    fetchMock.mockImplementationOnce(async () =>
      new Response(
        JSON.stringify({
          success: false,
          code: 'MODEL_CHANGE_REJECTED',
          message: 'multiplier change rejected',
          items: [
            {
              listing_id: 1,
              model: 'deepseek-flash',
              code: 'MULTIPLIER_COOLDOWN',
              message: '10 分钟内只能改一次',
              earliest_change_at: '2026-10-24T11:00:00+08:00',
            },
          ],
        }),
        { status: 409, headers: { 'content-type': 'application/json' } },
      ),
    )

    fireEvent.click(screen.getByRole('button', { name: '应用' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('有渠道未通过校验')
    expect(alert.textContent).toContain('#1 deepseek-flash：10 分钟内只能改一次')
    expect(alert.textContent).toContain('最早 2026-10-24 11:00:00 可再次修改')
    // All-or-nothing: the selection stays for a retry.
    expect(screen.getByText('已选 1 个渠道')).toBeTruthy()
  })
})

describe('seller listings card view', () => {
  it('renders one card per listing with the three-state dot', async () => {
    await renderLoadedTable()
    await showCards()

    expect(screen.getByRole('img', { name: '可用' })).toBeTruthy()
    expect(screen.getByRole('img', { name: '降级' })).toBeTruthy()
    expect(screen.getByRole('img', { name: '离线' })).toBeTruthy()
    expect(screen.getAllByRole('img', { name: '无样本' })).toHaveLength(2)
    expect(screen.getAllByRole('article')).toHaveLength(5)
  })

  it('shows today income, success rate, latency and the last probe failure', async () => {
    await renderLoadedTable()
    await showCards()

    expect(screen.getByText('¥12.34')).toBeTruthy()
    expect(screen.getByText('99.5%')).toBeTruthy()
    expect(screen.getByText('95%')).toBeTruthy()
    expect(screen.getByText('80%')).toBeTruthy()
    expect(screen.getByText('75 ms')).toBeTruthy()
    expect(screen.getByText('未通过')).toBeTruthy()
    expect(screen.getByText(/失败原因/)).toBeTruthy()
  })

  it('links each card to its detail page', async () => {
    await renderLoadedTable()
    await showCards()

    const links = screen.getAllByRole('link', { name: '查看详情' }) as HTMLAnchorElement[]
    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      '/console/listings/1',
      '/console/listings/2',
      '/console/listings/3',
      '/console/listings/4',
      '/console/listings/5',
    ])
  })
})
