import { describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { isLoginChallenge, loginUser, verifyLoginCode } from './api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
    },
  }
})

const mockedPost = vi.mocked(api.post)

const challenge = {
  require_verification: true as const,
  flow_token: 'flow-1',
  expires_at: 9_999_999_999,
  methods: [{ method: '2fa' as const, available: true }],
}

describe('login verification challenge', () => {
  it('recognises the 2FA challenge shape', () => {
    expect(isLoginChallenge(challenge)).toBe(true)
    expect(isLoginChallenge({ require_verification: false })).toBe(false)
    expect(isLoginChallenge({ require_verification: true, flow_token: '' })).toBe(false)
    expect(isLoginChallenge(null)).toBe(false)
  })

  it('returns a challenge outcome instead of throwing when 2FA is on', async () => {
    mockedPost.mockResolvedValueOnce(challenge as never)
    const outcome = await loginUser('alice', 'secret')
    expect(outcome.kind).toBe('challenge')
    if (outcome.kind === 'challenge') {
      expect(outcome.challenge.flow_token).toBe('flow-1')
    }
  })

  it('returns a session outcome when the bundle is issued directly', async () => {
    mockedPost.mockResolvedValueOnce({
      access_token: 'tok',
      user: { id: 1, username: 'alice', role: 1 },
    } as never)
    const outcome = await loginUser('alice', 'secret')
    expect(outcome.kind).toBe('session')
    if (outcome.kind === 'session') {
      expect(outcome.user?.username).toBe('alice')
    }
  })

  it('exchanges the flow token plus code and applies the bundle', async () => {
    mockedPost.mockResolvedValueOnce({ access_token: 'tok-2' } as never)
    await verifyLoginCode('flow-1', '123456')
    expect(mockedPost).toHaveBeenCalledWith(
      '/user/login/verify',
      { flow_token: 'flow-1', method: '2fa', code: '123456' },
      { skipAuthRetry: true },
    )
  })
})
