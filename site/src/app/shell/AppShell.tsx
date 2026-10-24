import { Link, useNavigate, useRouterState } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { logout, useAuth } from '@/lib/auth'
import { cn } from '@/lib/cn'
import { Button, Logo } from '@/ui'

type NavTo = '/' | '/models' | '/docs' | '/seller' | '/console' | '/admin'

const navItems: { to: NavTo; label: string; exact?: boolean }[] = [
  { to: '/', label: copy.nav.home, exact: true },
  { to: '/models', label: copy.nav.models },
  { to: '/docs', label: copy.nav.docs },
  { to: '/seller', label: copy.nav.seller },
]

function NavItem({
  to,
  label,
  exact = false,
  className,
}: {
  to: NavTo
  label: string
  exact?: boolean
  className?: string
}) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const active = exact ? pathname === to : pathname === to || pathname.startsWith(`${to}/`)

  return (
    <Link
      to={to}
      className={cn(
        'rounded-[4px] px-2 py-1 text-[12.5px] leading-6 text-muted transition-colors hover:bg-wash hover:text-ink',
        active && 'bg-wash font-medium text-ink',
        className,
      )}
    >
      {label}
    </Link>
  )
}

function Divider() {
  return <span className="h-3 w-px bg-line-strong" aria-hidden="true" />
}

export function AppShell({ children }: { children: ReactNode }) {
  const { user, isLoggedIn, isAdmin } = useAuth()
  const navigate = useNavigate()
  const [signingOut, setSigningOut] = useState(false)

  async function handleLogout() {
    setSigningOut(true)
    try {
      await logout()
      await navigate({ to: '/' })
    } finally {
      setSigningOut(false)
    }
  }

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="sticky top-0 z-30 border-b border-line bg-paper/95 backdrop-blur supports-[backdrop-filter]:bg-paper/80">
        <div className="mx-auto flex h-14 max-w-[1180px] items-center gap-5 px-4 sm:px-5">
          <Link to="/" aria-label={copy.brand.name} className="shrink-0">
            <Logo />
          </Link>

          <nav className="hidden items-center gap-0.5 sm:flex" aria-label={copy.nav.ariaMain}>
            {navItems.map((item) => (
              <NavItem key={item.to} {...item} />
            ))}
          </nav>

          <div className="ml-auto flex items-center gap-2">
            {isLoggedIn ? (
              <>
                {isAdmin ? (
                  <NavItem to="/admin" label={copy.nav.admin} className="hidden md:inline-flex" />
                ) : null}
                <NavItem to="/console" label={copy.nav.console} />
                <span className="hidden max-w-[120px] truncate text-[12px] text-muted md:inline">
                  {user?.display_name || user?.username}
                </span>
                <Button
                  variant="ghost"
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
                  className="rounded-[4px] px-2 py-1 text-[12.5px] text-muted transition-colors hover:bg-wash hover:text-ink"
                >
                  {copy.nav.login}
                </Link>
                <Link
                  to="/register"
                  className="inline-flex h-8 items-center rounded-[4px] bg-brand-dark px-3 text-[12.5px] font-medium text-white transition-colors hover:bg-[#065d54]"
                >
                  {copy.nav.register}
                </Link>
              </>
            )}
          </div>
        </div>

        <nav
          className="flex items-center gap-1 overflow-x-auto border-t border-line px-4 py-1.5 sm:hidden"
          aria-label={copy.nav.ariaMobile}
        >
          {navItems.map((item) => (
            <NavItem key={item.to} {...item} className="whitespace-nowrap" />
          ))}
          {isAdmin ? (
            <NavItem to="/admin" label={copy.nav.admin} className="whitespace-nowrap" />
          ) : null}
        </nav>
      </header>

      <main className="flex-1 overflow-x-clip">{children}</main>

      <footer className="border-t border-line">
        <div className="mx-auto flex max-w-[1180px] flex-col gap-3 px-5 py-6 text-[11.5px] text-muted sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>{copy.footer.copyright}</span>
            <Divider />
            <span>{copy.footer.company}</span>
            <Divider />
            <span>{copy.footer.icp}</span>
          </div>
          <div className="flex items-center gap-3">
            <Link to="/docs" hash="terms" className="hover:text-ink">
              {copy.footer.terms}
            </Link>
            <Divider />
            <Link to="/docs" hash="privacy" className="hover:text-ink">
              {copy.footer.privacy}
            </Link>
          </div>
        </div>
      </footer>
    </div>
  )
}
