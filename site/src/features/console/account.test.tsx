import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  changePasswordByEmail,
  fetchSelfProfile,
  sendPasswordChangeEmailCode,
} from '@/features/auth/account'
import { ToastProvider } from '@/ui'

import { Route } from '@/app/routes/console/account'

const AccountComponent = Route.options.component as React.ComponentType

vi.mock('@/lib/session', () => ({
  useSession: () => ({
    user: { id: 1, username: 'tester', display_name: 'Test User' },
  }),
}))

vi.mock('@/features/auth/account', () => ({
  fetchSelfProfile: vi.fn(),
  changeAccountPassword: vi.fn().mockResolvedValue(undefined),
  requestPasswordChangeProof: vi.fn().mockResolvedValue('proof-token'),
  sendPasswordChangeEmailCode: vi.fn().mockResolvedValue(undefined),
  changePasswordByEmail: vi.fn().mockResolvedValue(undefined),
}))

afterEach(cleanup)

function renderAccount() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <AccountComponent />
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('Console Account page', () => {
  it('renders account identity and password mode switcher, and handles email password change', async () => {
    vi.mocked(fetchSelfProfile).mockResolvedValueOnce({
      id: 1,
      username: 'tester',
      display_name: 'Test User',
      email: 'tester@example.com',
      has_password: true,
    })

    renderAccount()

    expect(await screen.findByText('登录信息')).toBeTruthy()
    expect(await screen.findByText('tester')).toBeTruthy()
    expect(await screen.findByText('Test User')).toBeTruthy()
    expect(await screen.findByText('tester@example.com')).toBeTruthy()

    expect(screen.getByRole('button', { name: '使用原密码修改' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '通过邮箱验证码修改' })).toBeTruthy()

    // Default mode is password: shows current password field
    expect(screen.getByLabelText('当前密码')).toBeTruthy()

    // Switch to email mode
    fireEvent.click(screen.getByRole('button', { name: '通过邮箱验证码修改' }))
    expect(screen.getByText(/验证码将发送至已绑定邮箱：tester@example\.com/)).toBeTruthy()
    expect(screen.getByLabelText('邮箱验证码')).toBeTruthy()

    const sendCodeButton = screen.getByRole('button', { name: '获取验证码' })
    expect(sendCodeButton).toBeTruthy()
    fireEvent.click(sendCodeButton)

    await waitFor(() => {
      expect(sendPasswordChangeEmailCode).toHaveBeenCalledTimes(1)
    })

    // Fill out form
    fireEvent.change(screen.getByLabelText('邮箱验证码'), { target: { value: '123456' } })
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'newPassword123' } })
    fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: 'newPassword123' } })

    fireEvent.click(screen.getByRole('button', { name: '修改密码' }))

    await waitFor(() => {
      expect(changePasswordByEmail).toHaveBeenCalledWith('123456', 'newPassword123')
    })
  })

  it('shows no-email notice when user has not bound an email in email mode', async () => {
    vi.mocked(fetchSelfProfile).mockResolvedValueOnce({
      id: 2,
      username: 'noemail',
      display_name: 'No Email User',
      email: '',
      has_password: true,
    })

    renderAccount()

    expect(await screen.findByText('noemail')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '通过邮箱验证码修改' }))

    expect(await screen.findByText('当前账号尚未绑定邮箱')).toBeTruthy()
  })
})
