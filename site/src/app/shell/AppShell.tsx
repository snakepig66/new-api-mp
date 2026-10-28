import { Link, useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { NotificationBell } from '@/features/notifications/NotificationBell'
import { logout, useAuth } from '@/lib/auth'
import { cn } from '@/lib/cn'
import { Button, Logo, buttonClasses } from '@/ui'

type NavTo = '/' | '/models' | '/docs' | '/leaderboard' | '/share' | '/console'

const navItems: { to: NavTo; label: string; exact?: boolean }[] = [
  { to: '/models', label: copy.nav.models },
  { to: '/leaderboard', label: copy.nav.leaderboard },
  { to: '/docs', label: copy.nav.docs },
  { to: '/share', label: copy.nav.seller },
]

function useIsActive(to: NavTo, exact = false): boolean {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  return exact ? pathname === to : pathname === to || pathname.startsWith(`${to}/`)
}

/** Desktop nav item: 14px, bottom-aligned 2px blue rule when active. */
function NavItem({ to, label, exact = false }: { to: NavTo; label: string; exact?: boolean }) {
  const active = useIsActive(to, exact)

  return (
    <Link
      to={to}
      className={cn(
        'relative inline-flex h-16 items-center px-3 text-body font-medium transition-colors',
        active ? 'text-brand-ink' : 'text-ink-2 hover:text-ink',
      )}
    >
      {label}
      {active ? (
        <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-brand" aria-hidden="true" />
      ) : null}
    </Link>
  )
}

/** Right-hand utility link (console/admin when signed in). */
function UtilityLink({ to, label, exact = false }: { to: NavTo; label: string; exact?: boolean }) {
  const active = useIsActive(to, exact)

  return (
    <Link
      to={to}
      className={cn(
        'hidden h-9 items-center rounded-full px-3 text-body transition-colors md:inline-flex',
        active ? 'bg-brand-soft/70 text-brand-ink' : 'text-ink-2 hover:text-ink',
      )}
    >
      {label}
    </Link>
  )
}

/** Mobile menu row: 44px tap target, soft pill when active. */
function MobileNavItem({
  to,
  label,
  exact = false,
  onNavigate,
}: {
  to: NavTo
  label: string
  exact?: boolean
  onNavigate: () => void
}) {
  const active = useIsActive(to, exact)

  return (
    <Link
      to={to}
      onClick={onNavigate}
      className={cn(
        'flex h-11 items-center rounded-full px-4 text-body transition-colors',
        active ? 'bg-brand-soft/70 font-medium text-brand-ink' : 'text-ink-2 hover:bg-white/60 hover:text-ink',
      )}
    >
      {label}
    </Link>
  )
}

/**
 * Footers must not carry placeholder copy (§0). A company/ICP line only
 * renders when copy.ts holds a real value; the current 待定/待填 strings are
 * treated as empty until the operator fills them in.
 */
function realFooterValue(value: string | undefined): value is string {
  if (!value) return false
  const trimmed = value.trim()
  return trimmed.length > 0 && !/待定|待填/.test(trimmed)
}

export function AppShell({ children }: { children: ReactNode }) {
  const { user, isLoggedIn } = useAuth()
  const navigate = useNavigate()
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const [signingOut, setSigningOut] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)

  // The console is a viewport-height app shell with its own internal
  // scrolling; the marketing footer would sit below the fold and push the
  // shell around, so app routes drop it. Its links live on the public pages.
  const isAppArea = pathname === '/console' || pathname.startsWith('/console/')

  useEffect(() => {
    setMenuOpen(false)
  }, [pathname])

  async function handleLogout() {
    setSigningOut(true)
    try {
      await logout()
      await navigate({ to: '/' })
    } finally {
      setSigningOut(false)
    }
  }

  const company = realFooterValue(copy.footer.company)
  const icp = realFooterValue(copy.footer.icp)

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="glass-bar sticky top-0 z-30 border-b border-white/60 supports-[backdrop-filter]:bg-white/60">
        <div className="mx-auto flex h-16 max-w-[1200px] items-center gap-6 px-5 sm:px-6">
          <Link to="/" aria-label={copy.brand.name} className="shrink-0">
            <Logo />
          </Link>

          <nav className="hidden h-16 items-stretch gap-1 md:flex" aria-label={copy.nav.ariaMain}>
            {navItems.map((item) => (
              <NavItem key={item.to} {...item} />
            ))}
          </nav>

          {/* TODO: ⌘K search pill goes here once the command palette ships (§3). */}

          <div className="ml-auto flex items-center gap-2 sm:gap-3">
            {isLoggedIn ? (
              <>
                <NotificationBell />
                <UtilityLink to="/console" label={copy.nav.console} />
                <span className="hidden max-w-[120px] truncate text-meta text-ink-3 lg:inline">
                  {user?.display_name || user?.username}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="hidden md:inline-flex"
                  loading={signingOut}
                  onClick={() => void handleLogout()}
                >
                  {copy.nav.logout}
                </Button>
              </>
            ) : (
              <div className="hidden items-center gap-1 md:flex">
                <Link
                  to="/login"
                  className="inline-flex h-9 items-center rounded-full px-3 text-body text-ink-2 transition-colors hover:text-ink"
                >
                  {copy.nav.login}
                </Link>
                <Link to="/register" className={buttonClasses('primary', 'sm')}>
                  {copy.nav.register}
                </Link>
              </div>
            )}

            <button
              type="button"
              aria-label={copy.nav.ariaMobile}
              aria-expanded={menuOpen}
              aria-controls="mobile-nav"
              onClick={() => setMenuOpen((open) => !open)}
              className="grid h-10 w-10 place-items-center rounded-full text-ink-2 transition-colors hover:bg-white/60 hover:text-ink md:hidden"
            >
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
                {menuOpen ? (
                  <path d="M4 4l12 12M16 4L4 16" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                ) : (
                  <path d="M3 6h14M3 10h14M3 14h14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                )}
              </svg>
            </button>
          </div>
        </div>

        {menuOpen ? (
          <div id="mobile-nav" className="glass-bar border-t border-white/60 md:hidden">
            <nav className="mx-auto flex max-w-[1200px] flex-col gap-1 px-4 py-3" aria-label={copy.nav.ariaMobile}>
              {navItems.map((item) => (
                <MobileNavItem key={item.to} {...item} onNavigate={() => setMenuOpen(false)} />
              ))}
              {isLoggedIn ? (
                <MobileNavItem to="/console" label={copy.nav.console} onNavigate={() => setMenuOpen(false)} />
              ) : null}

              <div className="mt-2 flex items-center gap-2 border-t border-line px-1 pt-3">
                {isLoggedIn ? (
                  <>
                    <span className="min-w-0 flex-1 truncate text-meta text-ink-3">
                      {user?.display_name || user?.username}
                    </span>
                    <Button
                      variant="secondary"
                      size="sm"
                      loading={signingOut}
                      onClick={() => void handleLogout()}
                    >
                      {copy.nav.logout}
                    </Button>
                  </>
                ) : (
                  <>
                    <Link
                      to="/login"
                      onClick={() => setMenuOpen(false)}
                      className="inline-flex h-9 flex-1 items-center justify-center rounded-full px-3 text-body text-ink-2 transition-colors hover:bg-white/60 hover:text-ink"
                    >
                      {copy.nav.login}
                    </Link>
                    <Link
                      to="/register"
                      onClick={() => setMenuOpen(false)}
                      className={buttonClasses('primary', 'sm', 'flex-1')}
                    >
                      {copy.nav.register}
                    </Link>
                  </>
                )}
              </div>
            </nav>
          </div>
        ) : null}
      </header>

      <main className="flex-1 overflow-x-clip">{children}</main>

      {isAppArea ? null : (
        <footer className="border-t border-white/60">
          <div className="mx-auto flex max-w-[1200px] flex-col gap-3 px-5 py-8 text-meta text-ink-3 sm:flex-row sm:items-center sm:justify-between sm:px-6">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
              <span>{copy.footer.copyright}</span>
              {company ? <span>{copy.footer.company}</span> : null}
              {icp ? <span>{copy.footer.icp}</span> : null}
            </div>
            <div className="flex items-center gap-5">
              <Link to="/agreement" className="text-ink-2 transition-colors hover:text-ink">
                {copy.footer.terms}
              </Link>
              <Link to="/privacy" className="text-ink-2 transition-colors hover:text-ink">
                {copy.footer.privacy}
              </Link>
            </div>
          </div>
        </footer>
      )}
    </div>
  )
}
