import { Link, useRouterState } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

export interface ConsoleNavItem {
  to: string
  label: string
  exact?: boolean
  icon?: ReactNode
}

export interface ConsoleNavGroup {
  label: string
  items: ConsoleNavItem[]
}

function isActive(pathname: string, item: ConsoleNavItem): boolean {
  if (item.exact) return pathname === item.to || pathname === `${item.to}/`
  return pathname === item.to || pathname.startsWith(`${item.to}/`)
}

export function getConsoleNavIcon(to: string): ReactNode {
  const norm = to.replace(/\/+$/, '')

  switch (norm) {
    case '/console':
      // 概览: 4-grid dashboard layout
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="5.5" height="5.5" rx="1.5" />
          <rect x="11.5" y="3" width="5.5" height="5.5" rx="1.5" />
          <rect x="3" y="11.5" width="5.5" height="5.5" rx="1.5" />
          <rect x="11.5" y="11.5" width="5.5" height="5.5" rx="1.5" />
        </svg>
      )

    case '/console/keys':
      // 令牌: Key
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="7" cy="13" r="3.5" />
          <path d="M9.5 10.5 16 4" />
          <path d="m13.5 6.5 1.5 1.5" />
          <path d="m11.5 8.5 1.5 1.5" />
        </svg>
      )

    case '/console/records':
      // 调用日志: Document log lines
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12.5 2.5H5.5a1.5 1.5 0 0 0-1.5 1.5v12a1.5 1.5 0 0 0 1.5 1.5h9a1.5 1.5 0 0 0 1.5-1.5V6.5L12.5 2.5Z" />
          <polyline points="12.5 2.5 12.5 6.5 16 6.5" />
          <line x1="7" y1="10.5" x2="13" y2="10.5" />
          <line x1="7" y1="13.5" x2="11" y2="13.5" />
        </svg>
      )

    case '/console/wallet':
      // 钱包与额度: Wallet / Card
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <rect x="2.5" y="4.5" width="15" height="11" rx="2" />
          <line x1="2.5" y1="8.5" x2="17.5" y2="8.5" />
          <circle cx="5.5" cy="12" r="0.75" fill="currentColor" />
        </svg>
      )

    case '/console/listings':
      // 共享模型: Layers / Stack
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <polygon points="10 2.5 17 6.5 10 10.5 3 6.5 10 2.5" />
          <polyline points="3 10 10 14 17 10" />
          <polyline points="3 13.5 10 17.5 17 13.5" />
        </svg>
      )

    case '/console/earnings':
      // 收益与提现: Trending growth arrow
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="17 5.5 11 11.5 7.5 8 2.5 13" />
          <polyline points="12.5 5.5 17 5.5 17 10" />
        </svg>
      )

    case '/console/account':
      // 账户设置: User avatar
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="10" cy="6.5" r="3.5" />
          <path d="M3.5 16.5a6.5 6.5 0 0 1 13 0" />
        </svg>
      )

    case '/console/ops/models':
      // 模型目录: Database / Storage catalog
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <ellipse cx="10" cy="5" rx="6.5" ry="2.5" />
          <path d="M3.5 5v5c0 1.38 2.91 2.5 6.5 2.5s6.5-1.12 6.5-2.5V5" />
          <path d="M3.5 10v5c0 1.38 2.91 2.5 6.5 2.5s6.5-1.12 6.5-2.5V10" />
        </svg>
      )

    case '/console/ops/review':
      // 发布审核: Review check
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3.5" y="3" width="13" height="14" rx="2" />
          <path d="m7 10 2 2 4-4" />
        </svg>
      )

    case '/console/ops/metrics':
      // 平台大盘: Bar chart
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <line x1="4.5" y1="16.5" x2="4.5" y2="10" />
          <line x1="10" y1="16.5" x2="10" y2="4.5" />
          <line x1="15.5" y1="16.5" x2="15.5" y2="8" />
        </svg>
      )

    case '/console/ops/tools':
      // 工具与配置: Sliders
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <line x1="3" y1="6.5" x2="17" y2="6.5" />
          <line x1="3" y1="13.5" x2="17" y2="13.5" />
          <circle cx="7" cy="6.5" r="2" fill="currentColor" />
          <circle cx="13" cy="13.5" r="2" fill="currentColor" />
        </svg>
      )

    case '/console/ops/pricing':
      // 定价管理: Tag
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10.5 2.5H4a1.5 1.5 0 0 0-1.5 1.5v6.5a1.5 1.5 0 0 0 .44 1.06l6.5 6.5a1.5 1.5 0 0 0 2.12 0l5.5-5.5a1.5 1.5 0 0 0 0-2.12l-6.5-6.5A1.5 1.5 0 0 0 10.5 2.5Z" />
          <circle cx="6.5" cy="6.5" r="1" fill="currentColor" />
        </svg>
      )

    case '/console/ops/risk':
      // 风控: Shield alert
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10 2.5 3.5 5.5v5c0 4.25 2.75 8.25 6.5 9.5 3.75-1.25 6.5-5.25 6.5-9.5v-5L10 2.5Z" />
          <line x1="10" y1="7.5" x2="10" y2="11" />
          <circle cx="10" cy="13.5" r="0.75" fill="currentColor" />
        </svg>
      )

    case '/console/ops/trace':
      // 链路跟踪: Trace pulse
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="2.5 10 6 10 8.5 4.5 11.5 15.5 14 10 17.5 10" />
        </svg>
      )

    case '/console/ops/trust-pool':
      // 信任池: Shield check
      return (
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10 2.5 3.5 5.5v5c0 4.25 2.75 8.25 6.5 9.5 3.75-1.25 6.5-5.25 6.5-9.5v-5L10 2.5Z" />
          <path d="m7.5 10.5 2 2 3.5-3.5" />
        </svg>
      )

    default:
      return null
  }
}

/**
 * One grouped navigation for buyer, seller and operator views. The layout
 * decides which groups exist (seller/admin gating); this component only
 * renders them, so desktop and the mobile drawer stay identical.
 */
export function ConsoleSidebar({
  groups,
  onNavigate,
}: {
  groups: ConsoleNavGroup[]
  onNavigate?: () => void
}) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })

  return (
    <nav className="flex flex-col gap-6" aria-label="控制台导航">
      {groups.map((group) => (
        <section key={group.label}>
          <p className="px-3 text-caption font-medium uppercase tracking-[0.12em] text-ink-3">
            {group.label}
          </p>
          <div className="mt-1.5 flex flex-col gap-0.5">
            {group.items.map((item) => {
              const active = isActive(pathname, item)
              const icon = item.icon ?? getConsoleNavIcon(item.to)
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  onClick={onNavigate}
                  aria-current={active ? 'page' : undefined}
                  className={cn(
                    'group flex h-9 items-center gap-2.5 rounded-full px-3 text-body transition-[color,background-color]',
                    active
                      ? 'bg-brand/10 font-medium text-brand-ink'
                      : 'text-ink-2 hover:bg-white/60 hover:text-ink',
                  )}
                >
                  {icon ? (
                    <span
                      className={cn(
                        'grid h-4 w-4 place-items-center shrink-0 transition-colors',
                        active ? 'text-brand' : 'text-ink-3 group-hover:text-ink',
                      )}
                      aria-hidden="true"
                    >
                      {icon}
                    </span>
                  ) : null}
                  <span className="truncate">{item.label}</span>
                </Link>
              )
            })}
          </div>
        </section>
      ))}
    </nav>
  )
}
