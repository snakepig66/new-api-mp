import * as DialogPrimitive from '@radix-ui/react-dialog'
import { useQuery } from '@tanstack/react-query'
import { Outlet, createFileRoute, redirect, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'

import { ConsoleSidebar } from '@/app/shell/ConsoleSidebar'
import { consoleGroups } from '@/app/shell/console-nav'
import { copy } from '@/copy'
import { ensureAuth, useAuth } from '@/lib/auth'
import { meQuery } from '@/features/seller/api'
import { isSellerActive } from '@/features/seller/status'
import { Button } from '@/ui'

export const Route = createFileRoute('/console')({
  beforeLoad: async ({ location }) => {
    const authenticated = await ensureAuth()
    if (!authenticated) {
      throw redirect({
        to: '/login',
        search: { redirect: location.href },
        replace: true,
      })
    }
  },
  component: ConsoleLayout,
})

const shell = copy.consoleShell

function ConsoleLayout() {
  const { isAdmin, isLoggedIn } = useAuth()
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const seller = useQuery({ ...meQuery(), enabled: isLoggedIn })
  const [menuOpen, setMenuOpen] = useState(false)
  const mainRef = useRef<HTMLElement>(null)

  useEffect(() => {
    setMenuOpen(false)
  }, [pathname])

  // The console is a viewport-height shell whose main column scrolls
  // internally, so the sidebar never moves. The window scroll position the
  // router restores is therefore always 0; reset the inner column instead so
  // every navigation lands at the top of the new page.
  useEffect(() => {
    mainRef.current?.scrollTo({ top: 0 })
  }, [pathname])

  const sellerActive = seller.data ? isSellerActive(seller.data.seller) : false

  const groups = consoleGroups({ sellerActive, isAdmin })
  const mobileLabel =
    groups
      .flatMap((group) => group.items)
      .find((item) =>
        item.exact
          ? pathname === item.to || pathname === `${item.to}/`
          : pathname === item.to || pathname.startsWith(`${item.to}/`),
      )?.label ?? copy.pages.console.nav.overview

  return (
    <div className="min-h-screen lg:h-[calc(100dvh-4rem)] lg:min-h-0">
      <div className="mx-auto flex w-full max-w-[1440px] flex-col lg:h-full lg:flex-row">
        <aside className="glass hidden shrink-0 flex-col gap-6 rounded-none border-y-0 border-l-0 px-5 py-6 lg:flex lg:h-full lg:w-[220px] lg:overflow-y-auto">
          <ConsoleSidebar groups={groups} />
        </aside>

        <div className="glass-bar sticky top-16 z-20 flex items-center gap-3 border-b border-white/60 px-4 py-2 lg:hidden">
          <Button variant="secondary" size="sm" onClick={() => setMenuOpen(true)}>
            {shell.menu}
          </Button>
          <span className="text-body font-medium text-ink">{mobileLabel}</span>
        </div>

        <DialogPrimitive.Root open={menuOpen} onOpenChange={setMenuOpen}>
          <DialogPrimitive.Portal>
            <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-[rgba(30,45,110,0.28)] backdrop-blur-[3px] lg:hidden" />
            <DialogPrimitive.Content className="glass-strong fixed inset-y-0 left-0 z-50 w-[280px] overflow-y-auto rounded-none border-y-0 border-l-0 p-5 lg:hidden">
              <DialogPrimitive.Title className="sr-only">{shell.menuTitle}</DialogPrimitive.Title>
              <DialogPrimitive.Description className="sr-only">
                {shell.menuTitle}
              </DialogPrimitive.Description>
              <ConsoleSidebar groups={groups} onNavigate={() => setMenuOpen(false)} />
              <DialogPrimitive.Close asChild>
                <Button variant="secondary" size="sm" className="mt-6 w-full">
                  {copy.actions.close}
                </Button>
              </DialogPrimitive.Close>
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        </DialogPrimitive.Root>

        <main
          ref={mainRef}
          className="min-w-0 flex-1 px-5 py-8 sm:px-8 lg:h-full lg:overflow-y-auto lg:overscroll-contain [scrollbar-gutter:stable] lg:px-10 lg:py-10"
        >
          <div className="mx-auto w-full max-w-[1040px]">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
