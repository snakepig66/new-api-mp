/**
 * 运营后台 · 广场渠道。
 *
 * 页面整体是 new-api 原版「渠道管理」的移植：代码位于
 * `site/src/legacy/features/channels`，这里只做站点外壳与鉴权/i18n 的桥接。
 */

import { QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { lazy, Suspense, useMemo, type ComponentType } from 'react'
import { I18nextProvider } from 'react-i18next'
import { Toaster } from 'sonner'
import z from 'zod'

import { ROLE_ADMIN_USER, getSession } from '@/lib/auth'

import { legacyI18n } from '@/legacy/i18n'
import { syncLegacyAuthFromSite } from '@/legacy/lib/session-bridge'
import { useAuthStore } from '@/legacy/stores/auth-store'

const channelsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(undefined),
  filter: z.string().optional().catch(''),
  status: z.array(z.string()).optional().catch([]),
  type: z.array(z.string()).optional().catch([]),
  group: z.array(z.string()).optional().catch([]),
  model: z.string().optional().catch(''),
})

const LegacyChannels = lazy(async () => {
  const mod = await import('@/legacy/features/channels')
  return { default: mod.Channels as ComponentType }
})

export const Route = createFileRoute('/console/ops/channels')({
  beforeLoad: () => {
    const role = getSession()?.user?.role ?? 0
    if (role < ROLE_ADMIN_USER) {
      throw redirect({ to: '/console', replace: true })
    }
  },
  validateSearch: channelsSearchSchema,
  component: ChannelsPage,
})

function ChannelsPage() {
  const queryClient = useQueryClient()
  const session = getSession()

  useMemo(() => {
    syncLegacyAuthFromSite(useAuthStore.getState().auth, {
      accessToken: session?.accessToken ?? '',
      accessExpiresAt: session?.accessExpiresAt ?? 0,
      sid: session?.sid ?? '',
      user: session?.user ?? null,
    })
  }, [session])

  return (
    <I18nextProvider i18n={legacyI18n}>
      <QueryClientProvider client={queryClient}>
        <div className='flex min-h-0 flex-1 flex-col'>
          <Suspense
            fallback={
              <div className='text-meta text-ink-3 p-6'>正在加载渠道管理…</div>
            }
          >
            <LegacyChannels />
          </Suspense>
        </div>
        <Toaster position='top-center' richColors closeButton />
      </QueryClientProvider>
    </I18nextProvider>
  )
}
