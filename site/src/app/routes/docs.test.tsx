import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DocsPage } from './docs'

function renderDocsPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const rootRoute = createRootRoute({ component: Outlet })
  const docsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/docs',
    component: DocsPage,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([docsRoute]),
    history: createMemoryHistory({ initialEntries: ['/docs'] }),
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const fetchMock = vi.fn(async () => {
  return new Response(JSON.stringify({ success: true, data: [] }), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
})

describe('docs navigation and scroll behavior', () => {
  const originalScrollTo = window.scrollTo
  const originalReplaceState = window.history.replaceState

  beforeEach(() => {
    fetchMock.mockClear()
    vi.stubGlobal('fetch', fetchMock)
    window.scrollTo = vi.fn()
    window.history.replaceState = vi.fn()
  })

  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    window.scrollTo = originalScrollTo
    window.history.replaceState = originalReplaceState
    vi.restoreAllMocks()
  })

  it('renders all table-of-contents links', async () => {
    renderDocsPage()

    expect(await screen.findByRole('heading', { level: 1, name: '文档' })).toBeTruthy()

    const navLinks = screen.getAllByRole('link', { name: '快速开始' })
    expect(navLinks.length).toBeGreaterThan(0)
    expect(screen.getAllByRole('link', { name: '计费说明' }).length).toBeGreaterThan(0)
  })

  it('smoothly scrolls to section and updates active state and URL hash without jumping', async () => {
    renderDocsPage()

    await screen.findByRole('heading', { level: 1, name: '文档' })

    // Find the desktop TOC link for billing
    const billingLinks = screen.getAllByRole('link', { name: '计费说明' })
    const targetLink = billingLinks[0]

    // Initially quickstart is active
    const quickstartLinks = screen.getAllByRole('link', { name: '快速开始' })
    expect(quickstartLinks[0].getAttribute('aria-current')).toBe('location')
    expect(targetLink.getAttribute('aria-current')).toBeNull()

    // Click on billing section link
    fireEvent.click(targetLink)

    // Active state updates immediately to billing
    expect(targetLink.getAttribute('aria-current')).toBe('location')
    expect(quickstartLinks[0].getAttribute('aria-current')).toBeNull()

    // Smooth scroll was called with smooth behavior
    expect(window.scrollTo).toHaveBeenCalledWith(
      expect.objectContaining({
        behavior: 'smooth',
      }),
    )

    // URL hash was updated using replaceState
    expect(window.history.replaceState).toHaveBeenCalledWith(null, '', '#billing')
  })

  it('filters TOC navigation and content when searching', async () => {
    renderDocsPage()

    await screen.findByRole('heading', { level: 1, name: '文档' })

    const searchInput = screen.getByRole('searchbox')
    fireEvent.change(searchInput, { target: { value: '向量' } })

    // Non-matching links should be filtered out
    expect(screen.queryByRole('link', { name: '图像' })).toBeNull()
    // Matching link should remain visible
    expect(screen.getAllByRole('link', { name: '向量 / Embeddings' }).length).toBeGreaterThan(0)
  })
})
