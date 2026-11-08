import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * Private query data must not survive an identity change. Token refreshes for
 * the same user keep the cache; switching accounts or logging out clears it.
 */
describe('session identity cache', () => {
  beforeEach(() => {
    window.localStorage.clear()
    vi.resetModules()
  })

  it('clears private queries only when the user identity changes', async () => {
    const { queryClient } = await import('./query-client')
    const clear = vi.spyOn(queryClient, 'clear')
    const { applyAuthBundle, clearSession } = await import('./session')

    applyAuthBundle({
      access_token: 'token-a',
      user: { id: 1, username: 'alice', role: 1 },
    })
    expect(clear).toHaveBeenCalledTimes(1)

    // A refresh for the same account must not discard cached data.
    applyAuthBundle({
      access_token: 'token-a2',
      user: { id: 1, username: 'alice', role: 1 },
    })
    expect(clear).toHaveBeenCalledTimes(1)

    // Switching accounts in another tab (or after logout) drops the old data.
    applyAuthBundle({
      access_token: 'token-b',
      user: { id: 2, username: 'bob', role: 1 },
    })
    expect(clear).toHaveBeenCalledTimes(2)

    clearSession()
    expect(clear).toHaveBeenCalledTimes(3)
  })
})
