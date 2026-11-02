import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { navigateToOpsLegacy } from './ops-legacy'

describe('navigateToOpsLegacy', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    // Redefine window.location.href
    delete (window as any).location
    ;(window as any).location = { href: '' }
  })

  it('obtains a ticket from the ticket endpoint and navigates with ?ticket=', async () => {
    vi.spyOn(api, 'post').mockResolvedValueOnce({ ticket: 'sec-ticket-xyz' })
    const preventDefault = vi.fn()

    await navigateToOpsLegacy({ preventDefault } as any)

    expect(preventDefault).toHaveBeenCalled()
    expect(api.post).toHaveBeenCalledWith('/ops-legacy/ticket')
    expect(window.location.href).toBe('/ops-legacy?ticket=sec-ticket-xyz')
  })

  it('falls back to direct /ops-legacy when ticket request fails', async () => {
    vi.spyOn(api, 'post').mockRejectedValueOnce(new Error('unauthorized'))

    await navigateToOpsLegacy()

    expect(window.location.href).toBe('/ops-legacy')
  })
})
