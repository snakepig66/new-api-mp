import {
  Link,
  Outlet,
  createRootRoute,
  useRouterState,
  type ErrorComponentProps,
} from '@tanstack/react-router'
import { useEffect } from 'react'

import { copy } from '@/copy'
import { errorMessage, isChunkLoadError, shouldAutoReload } from '@/lib/route-error'
import { Button, buttonClasses, Empty } from '@/ui'

import { AppShell } from '../shell/AppShell'

export const Route = createRootRoute({
  component: RootLayout,
  notFoundComponent: NotFoundPage,
  errorComponent: RouteErrorComponent,
})

/**
 * Auth screens own the whole viewport: no site nav, no footer. They are the
 * only public routes that opt out of AppShell.
 */
function isStandaloneAuth(pathname: string): boolean {
  return pathname === '/login' || pathname === '/register'
}

function RootLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })

  if (isStandaloneAuth(pathname)) {
    return <Outlet />
  }

  return (
    <AppShell>
      <Outlet />
    </AppShell>
  )
}

interface ErrorPageProps {
  title: string
  detail: string
  actionLabel: string
}

function ErrorPage({ title, detail, actionLabel }: ErrorPageProps) {
  return (
    <div className="mx-auto flex min-h-[60vh] max-w-2xl flex-col justify-center px-5 py-16">
      <h1 className="text-h1 font-semibold leading-tight tracking-tight text-ink">{title}</h1>
      <p className="mt-3 max-w-xl break-words text-meta leading-6 text-ink-3">{detail}</p>
      <div className="mt-6 flex flex-wrap items-center gap-3">
        <Button onClick={() => window.location.reload()}>{actionLabel}</Button>
        <Link to="/" className={buttonClasses('secondary', 'md')}>
          {copy.actions.backHome}
        </Link>
      </div>
    </div>
  )
}

/**
 * Root error boundary (also used as the router-wide default).
 *
 * A deploy rotates hashed chunks, so an old tab that lazy-loads a removed
 * chunk gets "Failed to fetch dynamically imported module". Reload once to
 * pick up the new index.html; if it fails again, show the Chinese error page
 * with a manual refresh instead of looping. Other errors use the same page.
 */
export function RouteErrorComponent({ error }: ErrorComponentProps) {
  const chunkError = isChunkLoadError(error)

  useEffect(() => {
    if (!isChunkLoadError(error)) return
    let reload = false
    try {
      reload = shouldAutoReload(error, window.location.pathname, window.sessionStorage)
    } catch {
      // Storage can be unavailable (private mode); fall through to the page.
      reload = false
    }
    if (reload) window.location.reload()
  }, [error])

  if (chunkError) {
    return (
      <ErrorPage
        title={copy.pages.errorPage.chunkTitle}
        detail={copy.pages.errorPage.chunkReason}
        actionLabel={copy.pages.errorPage.refresh}
      />
    )
  }

  return (
    <ErrorPage
      title={copy.pages.errorPage.title}
      detail={`${copy.pages.errorPage.detailLabel}：${errorMessage(error) || copy.errors.UNKNOWN}`}
      actionLabel={copy.actions.retry}
    />
  )
}

function NotFoundPage() {
  return (
    <div className="mx-auto max-w-[1180px] px-5 py-16">
      <Empty
        title={copy.pages.notFound.title}
        reason={copy.pages.notFound.reason}
        next={copy.pages.notFound.next}
        action={
          <Link to="/" className={buttonClasses('primary', 'md')}>
            {copy.actions.backHome}
          </Link>
        }
      />
    </div>
  )
}
