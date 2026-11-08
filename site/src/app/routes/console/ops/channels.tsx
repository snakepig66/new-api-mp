/**
 * 运营后台 · 广场渠道。
 *
 * 页面整体是 new-api 原版「渠道管理」的移植：代码位于
 * `site/src/legacy/features/channels`，这里只做站点外壳与鉴权/i18n 的桥接。
 */

import { QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import {
  Component,
  lazy,
  Suspense,
  useMemo,
  type ComponentType,
  type ReactNode,
} from 'react'
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

function loadLegacyChannels() {
  return Promise.all([
    import('@/legacy/features/channels'),
    // Sync the display currency (CNY on this site) before the first render,
    // otherwise legacy amounts fall back to the USD "$" default.
    import('@/legacy/lib/status-query')
      .then((m) => m.fetchStatus())
      .catch(() => null),
  ]).then(([mod]) => ({ default: mod.Channels as ComponentType }))
}

/** Retry once on a failed chunk load (e.g. right after a deploy). */
const LegacyChannels = lazy(() =>
  loadLegacyChannels().catch(
    () =>
      new Promise<{ default: ComponentType }>((resolve, reject) => {
        setTimeout(() => loadLegacyChannels().then(resolve, reject), 1500)
      })
  )
)

class LegacyErrorBoundary extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  render() {
    if (this.state.failed) {
      return (
        <div className='text-meta text-ink-3 p-6'>
          渠道管理加载失败。
          <button
            type='button'
            className='text-brand ml-2 underline'
            onClick={() => window.location.reload()}
          >
            重新加载
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

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
          <LegacyErrorBoundary>
            <Suspense
              fallback={
                <div className='text-meta text-ink-3 p-6'>
                  正在加载渠道管理…
                </div>
              }
            >
              <LegacyChannels />
            </Suspense>
          </LegacyErrorBoundary>
        </div>
        <Toaster position='top-center' richColors closeButton />
      </QueryClientProvider>
    </I18nextProvider>
  )
}
