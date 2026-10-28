import { Link, useRouterState } from '@tanstack/react-router'

import { cn } from '@/lib/cn'

export interface ConsoleNavItem {
  to: string
  label: string
  exact?: boolean
}

export interface ConsoleNavGroup {
  label: string
  items: ConsoleNavItem[]
}

function isActive(pathname: string, item: ConsoleNavItem): boolean {
  if (item.exact) return pathname === item.to || pathname === `${item.to}/`
  return pathname === item.to || pathname.startsWith(`${item.to}/`)
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
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  onClick={onNavigate}
                  aria-current={active ? 'page' : undefined}
                  className={cn(
                    'inline-flex h-9 items-center rounded-full px-3 text-body transition-[color,background-color]',
                    active
                      ? 'bg-brand/10 font-medium text-brand-ink'
                      : 'text-ink-2 hover:bg-white/60 hover:text-ink',
                  )}
                >
                  {item.label}
                </Link>
              )
            })}
          </div>
        </section>
      ))}
    </nav>
  )
}
