import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, api } from '@/lib/api'

import { rememberOAuthRedirect } from './api'
import {
  completeOAuthBind,
  startGithubBind,
  takeOAuthBindCallback,
  unbindOAuthProvider,
} from './bindings'

vi.mock('./api', () => ({
  rememberOAuthRedirect: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      delete: vi.fn(),
    },
  }
})

const mockedGet = vi.mocked(api.get)
const mockedPost = vi.mocked(api.post)
const mockedDelete = vi.mocked(api.delete)

function mockGithubStatus() {
  mockedGet.mockImplementation(async (path: string) => {
    if (path === '/status') {
      return { github_oauth: true, github_client_id: 'client-123' } as never
    }
    if (path === '/verify/methods') {
      return {
        scope: 'account.binding.bind',
        methods: [{ method: 'password', available: true }],
      } as never
    }
    return undefined as never
  })
}

function mockProofAndFlow() {
  mockedPost.mockImplementation(async (path: string) => {
    if (path === '/verify') return { proof_token: 'proof-token' } as never
    if (path === '/oauth/state') return { flow_token: 'flow-token' } as never
    return undefined as never
  })
}

describe('account bindings', () => {
  const assign = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    window.sessionStorage.clear()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, origin: 'https://llmjar.com', assign },
    })
  })

  afterEach(() => {
    window.sessionStorage.clear()
  })

  it('requests a bind proof and starts the GitHub handshake with it', async () => {
    mockGithubStatus()
    mockProofAndFlow()

    await startGithubBind('secret-password')

    expect(mockedPost).toHaveBeenCalledWith('/verify', {
      method: 'password',
      scope: 'account.binding.bind',
      context: { provider: 'github' },
      password: 'secret-password',
    })
    expect(mockedPost).toHaveBeenCalledWith(
      '/oauth/state',
      { provider: 'github', intent: 'bind' },
      { headers: { 'X-Security-Proof': 'proof-token' } },
    )
    expect(rememberOAuthRedirect).toHaveBeenCalledWith('/console/account')

    const url = assign.mock.calls[0]?.[0] as string
    expect(url.startsWith('https://github.com/login/oauth/authorize?')).toBe(true)
    expect(url).toContain('state=flow-token')
    expect(url).toContain('redirect_uri=https%3A%2F%2Fllmjar.com%2Foauth%2Fgithub')

    // The marker only matches the flow that started it.
    expect(takeOAuthBindCallback('other-state')).toBeNull()
  })

  it('remembers the bind flow token so the callback can recognise it', async () => {
    mockGithubStatus()
    mockProofAndFlow()

    await startGithubBind('secret-password')

    expect(takeOAuthBindCallback('flow-token')).toBe('github')
    // Consumed once: a replay cannot turn a later login into a bind.
    expect(takeOAuthBindCallback('flow-token')).toBeNull()
  })

  it('reports a localised reason when the provider identity is already bound', async () => {
    mockedGet.mockRejectedValueOnce(
      new ApiError(200, undefined, 'This Github account has already been bound'),
    )

    await expect(completeOAuthBind('github', 'code', 'state')).rejects.toThrow(
      /已经绑定了其他站点账号/,
    )
  })

  it('accepts the bind callback action instead of a login bundle', async () => {
    mockedGet.mockResolvedValueOnce({ action: 'bind' } as never)

    await expect(completeOAuthBind('github', 'code', 'state')).resolves.toBeUndefined()
    expect(mockedGet).toHaveBeenCalledWith('/oauth/github', {
      query: { code: 'code', state: 'state' },
    })
  })

  it('unbinds with a proof bound to the provider id', async () => {
    mockedGet.mockResolvedValueOnce({
      scope: 'account.binding.unbind',
      methods: [{ method: 'password', available: true }],
    } as never)
    mockedPost.mockResolvedValueOnce({ proof_token: 'unbind-proof' } as never)
    mockedDelete.mockResolvedValueOnce(undefined as never)

    await unbindOAuthProvider(7, 'secret-password')

    expect(mockedPost).toHaveBeenCalledWith('/verify', {
      method: 'password',
      scope: 'account.binding.unbind',
      context: { provider_id: 7 },
      password: 'secret-password',
    })
    expect(mockedDelete).toHaveBeenCalledWith('/user/oauth/bindings/7', {
      headers: { 'X-Security-Proof': 'unbind-proof' },
    })
  })
})
