import * as DialogPrimitive from '@radix-ui/react-dialog'
import { useQuery } from '@tanstack/react-query'
import { Outlet, createFileRoute, redirect, useRouterState } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

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

  useEffect(() => {
    setMenuOpen(false)
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
    <div className="min-h-screen bg-bg-soft">
      <div className="mx-auto flex w-full max-w-[1440px] flex-col lg:flex-row">
        <aside className="hidden shrink-0 flex-col gap-6 border-r border-line bg-bg px-5 py-6 lg:sticky lg:top-16 lg:flex lg:h-[calc(100vh-4rem)] lg:w-[220px]">
          <ConsoleSidebar groups={groups} />
        </aside>

        <div className="sticky top-16 z-20 flex items-center gap-3 border-b border-line bg-bg px-4 py-2 lg:hidden">
          <Button variant="secondary" size="sm" onClick={() => setMenuOpen(true)}>
            {shell.menu}
          </Button>
          <span className="text-[14px] font-medium text-ink">{mobileLabel}</span>
        </div>

        <DialogPrimitive.Root open={menuOpen} onOpenChange={setMenuOpen}>
          <DialogPrimitive.Portal>
            <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-black/40 lg:hidden" />
            <DialogPrimitive.Content className="fixed inset-y-0 left-0 z-50 w-[280px] overflow-y-auto border-r border-line bg-bg p-5 lg:hidden">
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

        <main className="min-w-0 flex-1 px-5 py-8 sm:px-8 lg:px-10 lg:py-10">
          <div className="mx-auto w-full max-w-[1040px]">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
