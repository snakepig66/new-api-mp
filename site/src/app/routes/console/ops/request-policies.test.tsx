import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'

import { Route } from './request-policies'

vi.mock('@/features/admin/components', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/features/admin/components')>()
  return {
    ...actual,
    RootOnly: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  }
})

const RequestPoliciesComponent = Route.options.component as React.ComponentType

const policy = {
  RetryTimes: '2',
  AutomaticRetryStatusCodes: '429,500-503',
}

let patchBodies: Record<string, unknown>[] = []

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.includes('/api/option/request_policy')) {
      if (init?.method === 'PATCH') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        patchBodies.push(body)
      }
      return jsonResponse({ success: true, data: { options: policy } })
    }
    return jsonResponse({ success: false, message: 'not found' })
  }
)

beforeEach(() => {
  patchBodies = []
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
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RequestPoliciesComponent />
      </ToastProvider>
    </QueryClientProvider>
  )
}

describe('ops request policies page', () => {
  it('saves retry times and normalizes status code ranges', async () => {
    renderPage()

    const retry = (await screen.findByLabelText(
      '最大重试次数'
    )) as HTMLInputElement
    expect(retry.value).toBe('2')

    fireEvent.change(retry, { target: { value: '5' } })
    const codes = screen.getByLabelText('自动重试状态码') as HTMLInputElement
    fireEvent.change(codes, { target: { value: '500-504, 429' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => expect(patchBodies.length).toBe(1))
    expect(patchBodies[0]).toEqual({
      options: {
        RetryTimes: '5',
        AutomaticRetryStatusCodes: '429,500-504',
      },
    })
  })
})
