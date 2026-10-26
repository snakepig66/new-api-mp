import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { shanghaiDateTimeToRfc3339 } from '@/features/admin/schemas'

import { TrustPoolPage } from './trust-pool'

const rows = [
  {
    id: 1,
    host: 'api.deepseek.com',
    host_pattern: '',
    path_prefix: '',
    key_prefix: '',
    vendor_slug: 'deepseek',
    kind: 'official',
    trust: 'trusted',
    endpoints: ['openai_chat', 'anthropic_messages'],
    allowed_models: ['deepseek-flash'],
    official_price_book_id: 0,
    quota_window_template: null,
    deny_key_prefixes: [],
    deny_paths: [],
    error_code_table: null,
    default_headers: null,
    account_fingerprint_rules: null,
    source: 'seed',
    notes: 'DeepSeek official API',
    expires_at: '2027-06-01T00:00:00+08:00',
    created_at: '2026-10-01T00:00:00+08:00',
    updated_at: '2026-10-01T00:00:00+08:00',
  },
  {
    id: 2,
    host: 'api.manual.example',
    host_pattern: '',
    path_prefix: '',
    key_prefix: '',
    vendor_slug: '',
    kind: 'third_party',
    trust: 'watch',
    endpoints: ['openai_chat'],
    allowed_models: [],
    official_price_book_id: 0,
    quota_window_template: null,
    deny_key_prefixes: [],
    deny_paths: [],
    error_code_table: null,
    default_headers: null,
    account_fingerprint_rules: null,
    source: 'manual',
    notes: 'manual row',
    expires_at: null,
    created_at: '2026-10-02T00:00:00+08:00',
    updated_at: '2026-10-02T00:00:00+08:00',
  },
]

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

let postBody: Record<string, unknown> | null = null
let putBody: Record<string, unknown> | null = null
let putURL = ''
let deleteURL = ''

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  const method = init?.method ?? 'GET'
  if (url.startsWith('/api/marketplace/admin/pool/channels')) {
    return jsonResponse({
      success: true,
      data: {
        items: [{ id: 1, slug: 'deepseek', name: 'DeepSeek', kind: 'official' }],
      },
    })
  }
  if (url === '/api/marketplace/admin/trust-pool' && method === 'POST') {
    postBody = JSON.parse(String(init?.body)) as Record<string, unknown>
    return jsonResponse({
      success: true,
      data: { ...rows[1], id: 3, ...postBody, source: 'manual' },
    })
  }
  if (url.startsWith('/api/marketplace/admin/trust-pool/') && method === 'PUT') {
    putURL = url
    putBody = JSON.parse(String(init?.body)) as Record<string, unknown>
    return jsonResponse({ success: true, data: { ...rows[1], ...putBody } })
  }
  if (url.startsWith('/api/marketplace/admin/trust-pool/') && method === 'DELETE') {
    deleteURL = url
    return jsonResponse({ success: true, data: { id: 2 } })
  }
  if (url.startsWith('/api/marketplace/admin/trust-pool/')) {
    const id = Number(url.split('/').pop())
    return jsonResponse({ success: true, data: rows.find((row) => row.id === id) ?? rows[0] })
  }
  if (url.startsWith('/api/marketplace/admin/trust-pool')) {
    return jsonResponse({
      success: true,
      data: { items: rows, total: rows.length, page: 1, page_size: 50 },
    })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  postBody = null
  putBody = null
  putURL = ''
  deleteURL = ''
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
        <TrustPoolPage />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

function formDialog(): HTMLElement {
  return screen.getByRole('dialog')
}

function submitForm() {
  const form = document.getElementById('trust-pool-form')
  expect(form).not.toBeNull()
  fireEvent.submit(form!)
}

async function openCreate() {
  renderPage()
  await screen.findByText('api.manual.example')
  fireEvent.click(screen.getByRole('button', { name: '新建认证池记录' }))
  await waitFor(() => expect(document.getElementById('trust-pool-form')).not.toBeNull())
}

async function openEditManual() {
  renderPage()
  await screen.findByText('api.manual.example')
  const editButtons = screen.getAllByRole('button', { name: '编辑' })
  fireEvent.click(editButtons[1])
  await waitFor(() => expect(document.getElementById('trust-pool-form')).not.toBeNull())
}

describe('trust pool admin page', () => {
  it('creates a manual watch row without ever sending a source', async () => {
    await openCreate()
    const dialog = within(formDialog())

    fireEvent.change(dialog.getByLabelText('精确主机'), {
      target: { value: 'api.new.example' },
    })
    fireEvent.change(dialog.getByLabelText('信任状态'), { target: { value: 'watch' } })
    submitForm()

    await waitFor(() => expect(postBody).not.toBeNull())
    expect(postBody).toMatchObject({
      host: 'api.new.example',
      host_pattern: '',
      kind: 'third_party',
      trust: 'watch',
      endpoints: ['openai_chat'],
    })
    expect(postBody).not.toHaveProperty('source')
  })

  it('requires an expiry for trusted and notes for blocked', async () => {
    await openCreate()
    const dialog = within(formDialog())
    fireEvent.change(dialog.getByLabelText('精确主机'), {
      target: { value: 'api.new.example' },
    })

    fireEvent.change(dialog.getByLabelText('信任状态'), { target: { value: 'trusted' } })
    submitForm()
    expect((await screen.findAllByText('trust=trusted 时必须填写有效期')).length).toBeGreaterThan(0)
    expect(postBody).toBeNull()

    fireEvent.change(dialog.getByLabelText('信任状态'), { target: { value: 'blocked' } })
    submitForm()
    expect(
      (await screen.findAllByText('trust=blocked 时必须填写备注')).length,
    ).toBeGreaterThan(0)
    expect(postBody).toBeNull()

    fireEvent.change(dialog.getByLabelText('备注'), {
      target: { value: 'resold plan key' },
    })
    submitForm()
    await waitFor(() => expect(postBody).not.toBeNull())
    expect(postBody).toMatchObject({ trust: 'blocked', notes: 'resold plan key' })
  })

  it('keeps official disabled for manual rows and sends expiry on update', async () => {
    await openEditManual()
    const dialog = within(formDialog())

    const official = dialog.getByRole('option', { name: '官方' }) as HTMLOptionElement
    expect(official.disabled).toBe(true)

    fireEvent.change(dialog.getByLabelText('信任状态'), { target: { value: 'trusted' } })
    submitForm()
    expect((await screen.findAllByText('trust=trusted 时必须填写有效期')).length).toBeGreaterThan(0)
    expect(putBody).toBeNull()

    fireEvent.change(dialog.getByLabelText('有效期至'), {
      target: { value: '2027-01-01T00:00' },
    })
    submitForm()
    await waitFor(() => expect(putBody).not.toBeNull())
    expect(putURL).toBe('/api/marketplace/admin/trust-pool/2')
    expect(putBody?.trust).toBe('trusted')
    expect(putBody?.expires_at).toBe(shanghaiDateTimeToRfc3339('2027-01-01T00:00'))
  })

  it('disables deleting a seeded row and confirms deleting a manual one', async () => {
    renderPage()
    await screen.findByText('api.manual.example')

    const deleteButtons = screen.getAllByRole('button', { name: '删除' })
    expect((deleteButtons[0] as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(deleteButtons[1])

    const confirm = within(await screen.findByRole('dialog'))
    const actions = confirm.getAllByRole('button', { name: '删除' })
    fireEvent.click(actions[actions.length - 1])

    await waitFor(() => expect(deleteURL).toBe('/api/marketplace/admin/trust-pool/2'))
  })
})
