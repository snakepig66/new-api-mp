import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { writeClipboard } from '@/lib/clipboard'

import { CopyButton } from './copy-button'
import { ToastProvider } from './toast'

vi.mock('@/lib/clipboard', () => ({ writeClipboard: vi.fn(async () => true) }))

const mockedWrite = vi.mocked(writeClipboard)

afterEach(() => {
  cleanup()
  mockedWrite.mockClear()
})

function renderButton(ui: React.ReactNode) {
  return render(<ToastProvider>{ui}</ToastProvider>)
}

describe('CopyButton', () => {
  it('copies a plain string and flips to the copied label', async () => {
    renderButton(<CopyButton text="sk-test" label="复制" />)
    fireEvent.click(screen.getByRole('button', { name: '复制' }))
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledWith('sk-test'))
    expect(await screen.findByText('已复制')).toBeTruthy()
  })

  it('resolves a lazy text function before copying', async () => {
    const resolver = vi.fn(async () => 'revealed-key')
    renderButton(<CopyButton text={resolver} label="复制 Key" />)
    fireEvent.click(screen.getByRole('button', { name: '复制 Key' }))
    await waitFor(() => expect(resolver).toHaveBeenCalled())
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledWith('revealed-key'))
  })

  it('icon variant exposes the accessible label', () => {
    renderButton(<CopyButton variant="icon" text="x" label="复制 API Key" />)
    expect(screen.getByRole('button', { name: '复制 API Key' })).toBeTruthy()
  })
})
