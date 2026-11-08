import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { ListingsContent } from './index'

const groups = [
  {
    host: 'api.alpha.example',
    count: 2,
    listings: [
      {
        id: 101,
        seller_id: 9,
        vendor: 'custom-9-aaa',
        custom_base_url: 'https://api.alpha.example/v1',
        models: ['deepseek-flash'],
        created_at: '2026-10-02T00:00:00+08:00',
        seller_real_name_status: 'verified',
      },
      {
        id: 102,
        seller_id: 9,
        vendor: 'custom-9-bbb',
        custom_base_url: 'https://api.alpha.example/v1',
        models: ['glm-5.3'],
        created_at: '2026-10-01T00:00:00+08:00',
        seller_real_name_status: 'unverified',
      },
    ],
  },
  {
    host: '',
    count: 1,
    listings: [
      {
        id: 103,
        seller_id: 10,
        vendor: 'deepseek',
        custom_base_url: '',
        models: ['deepseek-flash'],
        created_at: '2026-10-03T00:00:00+08:00',
        seller_real_name_status: 'unverified',
      },
    ],
  },
]

interface RecordedCall {
  url: string
  method: string
  body: unknown
}

let calls: RecordedCall[] = []

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  const method = init?.method ?? 'GET'
  if (method !== 'GET') {
    calls.push({
      url,
      method,
      body: init?.body ? (JSON.parse(String(init.body)) as unknown) : null,
    })
    return jsonResponse({ success: true, data: {} })
  }
  if (url.startsWith('/api/marketplace/admin/listings/pending')) {
    return jsonResponse({ success: true, data: groups })
  }
  if (url.startsWith('/api/marketplace/admin/listings')) {
    return jsonResponse({ success: true, data: [] })
  }
  return jsonResponse({ success: false, message: 'not found' }, 404)
})

beforeEach(() => {
  calls = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ListingsContent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

async function openPendingTab() {
  renderPage()
  fireEvent.click(screen.getByRole('tab', { name: '待审核' }))
  await screen.findByText('api.alpha.example')
}

describe('admin review pending queue', () => {
  it('groups pending listings by host with a count', async () => {
    await openPendingTab()

    expect(screen.getByText('2 个待审')).toBeTruthy()
    expect(screen.getByText('1 个待审')).toBeTruthy()
    expect(screen.getByText('平台内置渠道')).toBeTruthy()
    expect(screen.getByText('#101')).toBeTruthy()
    expect(screen.getByText('#102')).toBeTruthy()
    expect(screen.getByText('#103')).toBeTruthy()
    expect(screen.getAllByText('已实名')).toHaveLength(1)
    expect(screen.getAllByText('未实名')).toHaveLength(2)
  })

  it('collapses and expands a host group', async () => {
    await openPendingTab()

    const header = screen.getByRole('button', { name: /api\.alpha\.example/ })
    expect(header.getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByText('#101')).toBeTruthy()

    fireEvent.click(header)
    await waitFor(() => expect(header.getAttribute('aria-expanded')).toBe('false'))
    expect(screen.queryByText('#101')).toBeNull()

    fireEvent.click(header)
    await waitFor(() => expect(header.getAttribute('aria-expanded')).toBe('true'))
    expect(screen.getByText('#101')).toBeTruthy()
  })

  it('approves a pending listing', async () => {
    await openPendingTab()

    const approveButtons = screen.getAllByRole('button', { name: '通过' })
    fireEvent.click(approveButtons[0])

    const dialog = within(await screen.findByRole('dialog'))
    fireEvent.click(dialog.getByRole('button', { name: '通过' }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.url).toBe('/api/marketplace/admin/listings/101/approve')
  })

  it('rejects a pending listing back to draft', async () => {
    await openPendingTab()

    fireEvent.click(screen.getAllByRole('button', { name: '拒绝' })[0])
    const dialog = within(await screen.findByRole('dialog'))
    fireEvent.click(dialog.getByRole('button', { name: '拒绝' }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]?.url).toBe('/api/marketplace/admin/listings/101/reject')
    expect(calls[0]?.body).toMatchObject({ action: 'draft' })
  })

  it('rejects and bans a pending listing', async () => {
    await openPendingTab()

    fireEvent.click(screen.getAllByRole('button', { name: '拒绝并封禁' })[0])
    const dialog = within(await screen.findByRole('dialog'))
    fireEvent.click(dialog.getByRole('button', { name: '拒绝并封禁' }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]?.url).toBe('/api/marketplace/admin/listings/101/reject')
    expect(calls[0]?.body).toMatchObject({ action: 'ban' })
  })
})
