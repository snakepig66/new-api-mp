import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, api, setUnauthorizedHandler } from './api'
import { applyAuthBundle, clearSession, getSession } from './session'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const fetchMock = vi.fn()

beforeEach(() => {
  window.localStorage.clear()
  applyAuthBundle({
    access_token: 'access-token-1',
    access_expires_at: Math.floor(Date.now() / 1000) + 900,
    session: { sid: 'sid-1' },
    user: { id: 1, username: 'buyer', role: 1 },
  })
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  clearSession()
  vi.unstubAllGlobals()
})

describe('api 401 handling', () => {
  it('refreshes once, then redirects to /login and clears the session', async () => {
    const handler = vi.fn()
    const previous = setUnauthorizedHandler(handler)

    fetchMock
      .mockResolvedValueOnce(
        jsonResponse(401, { success: false, code: 'AUTH_TOKEN_EXPIRED', message: 'expired' }),
      )
      .mockResolvedValueOnce(
        jsonResponse(401, { success: false, code: 'AUTH_TOKEN_EXPIRED', message: 'expired' }),
      )

    await expect(api.get('/user/self')).rejects.toMatchObject({
      status: 401,
      code: 'AUTH_TOKEN_EXPIRED',
    })

    // First request carries the bearer token, second one is the refresh call.
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const [firstUrl, firstInit] = fetchMock.mock.calls[0]
    expect(firstUrl).toBe('/api/user/self')
    expect((firstInit.headers as Headers).get('authorization')).toBe('Bearer access-token-1')
    expect(firstInit.credentials).toBe('include')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/user/auth/refresh')

    expect(handler).toHaveBeenCalledTimes(1)
    const info = handler.mock.calls[0][0] as { path: string; redirectTo: string }
    expect(info.path).toBe('/user/self')
    expect(info.redirectTo).toMatch(/^\/login\?redirect=/)
    expect(getSession()).toBeNull()

    setUnauthorizedHandler(previous)
  })

  it('does not redirect when the login endpoint answers 401', async () => {
    const handler = vi.fn()
    const previous = setUnauthorizedHandler(handler)

    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { success: false, message: 'bad credentials' }),
    )

    await expect(api.post('/user/login', { username: 'a', password: 'b' })).rejects.toBeInstanceOf(
      ApiError,
    )

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(handler).not.toHaveBeenCalled()
    // The existing session survives a failed login attempt.
    expect(getSession()?.accessToken).toBe('access-token-1')

    setUnauthorizedHandler(previous)
  })

  it('does not redirect when the caller handles the 401 itself', async () => {
    const handler = vi.fn()
    const previous = setUnauthorizedHandler(handler)

    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { success: false, code: 'AUTH_UNAUTHORIZED' }),
    )

    await expect(
      api.get('/user/self', { skipAuthRetry: true, skipUnauthorizedHandler: true }),
    ).rejects.toMatchObject({ status: 401 })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(handler).not.toHaveBeenCalled()

    setUnauthorizedHandler(previous)
  })
})

describe('api envelopes', () => {
  it('unwraps { success, data }', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { success: true, message: '', data: [{ model: 'demo' }] }),
    )

    await expect(api.get('/marketplace/square/models')).resolves.toEqual([{ model: 'demo' }])
    expect(fetchMock.mock.calls[0][0]).toBe('/api/marketplace/square/models')
  })

  it('turns a 200 + success:false envelope into an ApiError with a Chinese message', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { success: false, code: 'insufficient_balance', message: 'Insufficient' }),
    )

    await expect(api.get('/marketplace/square/models')).rejects.toMatchObject({
      status: 200,
      code: 'insufficient_balance',
      message: '余额不足，请充值后重试。',
    })
  })

  it('maps OpenAI-style { error } payloads', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(503, {
        error: { message: 'No supplier', type: 'server_error', code: 'no_supplier_available' },
      }),
    )

    await expect(api.get('/marketplace/status')).rejects.toMatchObject({
      status: 503,
      code: 'no_supplier_available',
      message: '暂无可用供应商，请稍后重试。',
    })
  })
})
