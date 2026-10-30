import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ConsoleSidebar, getConsoleNavIcon } from './ConsoleSidebar'

// Mock TanStack Router
vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    className,
    onClick,
    'aria-current': ariaCurrent,
  }: {
    to: string
    children: React.ReactNode
    className?: string
    onClick?: () => void
    'aria-current'?: string
  }) => (
    <a href={to} className={className} onClick={onClick} aria-current={ariaCurrent}>
      {children}
    </a>
  ),
  useRouterState: () => '/console/keys',
}))

describe('ConsoleSidebar', () => {
  afterEach(() => {
    cleanup()
  })

  const mockGroups = [
    {
      label: '用量',
      items: [
        { to: '/console', label: '概览', exact: true },
        { to: '/console/keys', label: '令牌' },
        { to: '/console/records', label: '调用日志' },
      ],
    },
    {
      label: '资金',
      items: [{ to: '/console/wallet', label: '钱包与额度' }],
    },
  ]

  it('renders groups and all nav items with labels', () => {
    render(<ConsoleSidebar groups={mockGroups} />)

    expect(screen.getByText('用量')).toBeDefined()
    expect(screen.getByText('资金')).toBeDefined()
    expect(screen.getByText('概览')).toBeDefined()
    expect(screen.getByText('令牌')).toBeDefined()
    expect(screen.getByText('调用日志')).toBeDefined()
    expect(screen.getByText('钱包与额度')).toBeDefined()
  })

  it('marks active item correctly based on pathname', () => {
    render(<ConsoleSidebar groups={mockGroups} />)

    const keysLink = screen.getByText('令牌').closest('a')
    expect(keysLink).not.toBeNull()
    expect(keysLink?.getAttribute('aria-current')).toBe('page')
    expect(keysLink?.className).toContain('text-brand-ink')

    const overviewLink = screen.getByText('概览').closest('a')
    expect(overviewLink?.getAttribute('aria-current')).toBeNull()
  })

  it('renders icons for each known route', () => {
    const routes = [
      '/console',
      '/console/keys',
      '/console/records',
      '/console/wallet',
      '/console/listings',
      '/console/earnings',
      '/console/account',
      '/console/ops/models',
      '/console/ops/review',
      '/console/ops/metrics',
      '/console/ops/tools',
      '/console/ops/pricing',
      '/console/ops/risk',
      '/console/ops/trace',
      '/console/ops/trust-pool',
    ]

    for (const route of routes) {
      const icon = getConsoleNavIcon(route)
      expect(icon).not.toBeNull()
    }
  })
})
