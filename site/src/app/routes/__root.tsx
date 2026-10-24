import { Link, createRootRoute, Outlet } from '@tanstack/react-router'

import { copy } from '@/copy'
import { buttonClasses, Empty } from '@/ui'

import { AppShell } from '../shell/AppShell'

export const Route = createRootRoute({
  component: RootLayout,
  notFoundComponent: NotFoundPage,
})

function RootLayout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
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
