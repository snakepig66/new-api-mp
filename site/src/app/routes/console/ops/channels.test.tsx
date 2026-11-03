import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'
import { Route } from './channels'

const ChannelsComponent = Route.options.component as React.ComponentType

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
    base_url: null,
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

let postBodies: { url: string; body: Record<string, unknown> }[] = []

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.includes('/api/channel/default_base_urls')) {
    return jsonResponse({ success: true, data: { 1: 'https://api.openai.com' } })
  }
  if (url.includes('/api/marketplace/admin/pool/models')) {
    return jsonResponse({ success: true, data: { items: [{ name: 'gpt-4o' }] } })
  }
  if (url.includes('/api/channel/search')) {
    return jsonResponse({ success: true, data: { items: [channels[0]], total: 1 } })
  }
  if (url.includes('/api/channel/') && init?.method === 'POST') {
    postBodies.push({ url, body: JSON.parse(String(init.body)) as Record<string, unknown> })
    return jsonResponse({ success: true, data: {} })
  }
  if (url.includes('/api/channel/')) {
    return jsonResponse({ success: true, data: { items: channels, total: channels.length } })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  postBodies = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderChannels() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ChannelsComponent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('plaza channels management', () => {
  it('renders channel rows with name, type and status', async () => {
    renderChannels()

    expect(await screen.findByText('OpenAI 主渠道')).toBeTruthy()
    expect(screen.getByText('Claude 备用')).toBeTruthy()
    expect(screen.getByText('OpenAI 兼容')).toBeTruthy()
    expect(screen.getByText('Anthropic (Claude)')).toBeTruthy()
    expect(screen.getByText('启用')).toBeTruthy()
    expect(screen.getByText('自动禁用')).toBeTruthy()
    expect(screen.getAllByText('321ms').length).toBeGreaterThan(0)
  })

  it('creates a channel through POST /api/channel', async () => {
    renderChannels()

    fireEvent.click(await screen.findByRole('button', { name: '+ 添加渠道' }))

    const nameInput = (await screen.findByLabelText('名称')) as HTMLInputElement
    fireEvent.change(nameInput, { target: { value: '新渠道' } })

    const keyInput = screen.getByLabelText('Key') as HTMLTextAreaElement
    fireEvent.change(keyInput, { target: { value: 'sk-test' } })

    const modelsInput = screen.getByLabelText('支持的模型') as HTMLTextAreaElement
    fireEvent.change(modelsInput, { target: { value: 'gpt-4o' } })

    fireEvent.click(screen.getByRole('button', { name: '保存渠道' }))

    await waitFor(() => expect(postBodies.length).toBe(1))
    const { body } = postBodies[0]
    expect(body.mode).toBe('single')
    const channel = body.channel as Record<string, unknown>
    expect(channel.name).toBe('新渠道')
    expect(channel.key).toBe('sk-test')
    expect(channel.models).toBe('gpt-4o')
    expect(channel.group).toBe('default')
    expect(channel.type).toBe(1)
  })
})
