import { describe, expect, it } from 'vitest'

import { copy } from '@/copy'
import { consoleGroups } from './console-nav'

function labels(groups: ReturnType<typeof consoleGroups>): string[] {
  return groups.flatMap((group) => group.items.map((item) => item.label))
}

describe('console navigation', () => {
  it('shows only usage, funds and account for a buyer', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: false })
    expect(groups.map((group) => group.label)).toEqual([
      copy.consoleShell.groups.usage,
      copy.consoleShell.groups.funds,
      copy.consoleShell.groups.account,
    ])
    expect(labels(groups)).not.toContain(copy.pages.sellerCommon.nav.listings)
    expect(labels(groups)).not.toContain(copy.consoleShell.ops.pricing)
  })

  it('adds the share group only after onboarding', () => {
    const groups = consoleGroups({ sellerActive: true, isAdmin: false })
    expect(labels(groups)).toEqual(
      expect.arrayContaining([
        copy.pages.sellerCommon.nav.listings,
        copy.pages.sellerCommon.nav.payouts,
      ]),
    )
    expect(groups.map((group) => group.label)).toContain(copy.consoleShell.groups.share)
    // 开通引导 stays out of the sidebar; it is reached from the overview card.
    expect(labels(groups)).not.toContain(copy.pages.sellerCommon.nav.onboarding)
  })

  it('adds the operator group only for admins', () => {
    const groups = consoleGroups({ sellerActive: false, isAdmin: true })
    expect(groups.map((group) => group.label)).toContain(copy.consoleShell.groups.ops)
    expect(labels(groups)).toEqual(
      expect.arrayContaining([
        copy.consoleShell.ops.models,
        copy.consoleShell.ops.review,
        copy.consoleShell.ops.metrics,
        copy.consoleShell.ops.tools,
      ]),
    )
    expect(labels(groups)).not.toContain(copy.consoleShell.ops.pricing)
    expect(labels(groups)).not.toContain(copy.consoleShell.ops.trustPool)
  })
})
