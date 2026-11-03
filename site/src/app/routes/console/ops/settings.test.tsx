import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/ui'
import { Route } from './settings'

vi.mock('@/features/admin/components', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/features/admin/components')>()
  return { ...actual, RootOnly: ({ children }: { children: React.ReactNode }) => <>{children}</> }
})

const SettingsComponent = Route.options.component as React.ComponentType

const options = [
  { key: 'ServerAddress', value: 'https://old.example.com' },
  { key: 'SystemName', value: '旧站名' },
  { key: 'Notice', value: '' },
  { key: 'Footer', value: '' },
  { key: 'RegisterEnabled', value: 'true' },
  { key: 'PasswordRegisterEnabled', value: 'true' },
  { key: 'EmailVerificationEnabled', value: 'false' },
  { key: 'EmailDomainRestrictionEnabled', value: 'false' },
  { key: 'EmailDomainWhitelist', value: 'example.com' },
  { key: 'TurnstileCheckEnabled', value: 'false' },
  { key: 'TurnstileSiteKey', value: '' },
  { key: 'SMTPServer', value: 'smtp.example.com' },
  { key: 'SMTPPort', value: '465' },
  { key: 'SMTPAccount', value: 'bot@example.com' },
  { key: 'SMTPFrom', value: 'bot@example.com' },
  { key: 'SMTPSSLEnabled', value: 'true' },
  { key: 'PayAddress', value: 'https://pay.example.com' },
  { key: 'EpayId', value: '1001' },
  { key: 'Price', value: '0.5' },
  { key: 'MinTopUp', value: '10' },
  { key: 'TopupGroupRatio', value: '{"default":1}' },
  { key: 'CustomCallbackAddress', value: '' },
  { key: 'QuotaForNewUser', value: '1000000' },
  { key: 'QuotaForInviter', value: '0' },
  { key: 'QuotaForInvitee', value: '0' },
]

let putBodies: Record<string, unknown>[] = []

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input)
  if (url.includes('/api/option/') && init?.method === 'PUT') {
    const body = JSON.parse(String(init.body)) as Record<string, unknown>
    putBodies.push(body)
    return jsonResponse({ success: true, message: '' })
  }
  if (url.includes('/api/option/')) return jsonResponse({ success: true, data: options })
  if (url.includes('/api/status')) {
    return jsonResponse({ success: true, data: { quota_per_unit: 1_000_000 } })
  }
  return jsonResponse({ success: false, message: 'not found' })
})

beforeEach(() => {
  putBodies = []
  fetchMock.mockClear()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderSettings() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <SettingsComponent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('ops settings page', () => {
  it('renders option values, converting quota to yuan', async () => {
    renderSettings()

    const address = (await screen.findByLabelText('站点地址')) as HTMLInputElement
    expect(address.value).toBe('https://old.example.com')
    expect((screen.getByLabelText('站点名称') as HTMLInputElement).value).toBe('旧站名')
    expect((screen.getByLabelText('SMTP 服务器') as HTMLInputElement).value).toBe('smtp.example.com')
    expect((screen.getByLabelText('邀请人奖励') as HTMLInputElement).value).toBe('0')
    expect((screen.getByLabelText('新用户赠送') as HTMLInputElement).value).toBe('1')
    // Secret fields are write-only: always rendered blank, never echoed back.
    expect((screen.getByLabelText('Turnstile Secret Key') as HTMLInputElement).value).toBe('')
  })

  it('saves only changed site keys', async () => {
    renderSettings()

    const name = (await screen.findByLabelText('站点名称')) as HTMLInputElement
    fireEvent.change(name, { target: { value: '新站名' } })
    fireEvent.click(screen.getAllByRole('button', { name: '保存' })[0])

    await waitFor(() => expect(putBodies.length).toBe(1))
    expect(putBodies[0]).toEqual({ key: 'SystemName', value: '新站名' })
  })

  it('converts yuan quota back to quota units', async () => {
    renderSettings()

    const invitee = (await screen.findByLabelText('被邀请人奖励')) as HTMLInputElement
    fireEvent.change(invitee, { target: { value: '2.5' } })
    const newUserSection = screen.getByRole('heading', { name: '新用户' }).closest('section')
    const saveButton = newUserSection?.querySelector('button') as HTMLButtonElement
    fireEvent.click(saveButton)

    await waitFor(() => expect(putBodies.length).toBe(1))
    expect(putBodies[0]).toEqual({ key: 'QuotaForInvitee', value: '2500000' })
  })
})
