import { createFileRoute } from '@tanstack/react-router'

/**
 * Legacy `/seller/*` catch-all.
 *
 * The parent `/seller` route (`seller.tsx`) is a redirect shell that rewrites
 * every old seller URL to its current `/console/*` equivalent. That redirect
 * only runs for a `/seller/*` path the router can actually match, and there is
 * no real page at `/seller/listings` (or `/seller/listings/new`, etc.) — those
 * pages now live under `/console/listings`. Without this splat those legacy
 * bookmarks fall through to the global not-found instead of being redirected.
 *
 * This route renders nothing; the parent's `beforeLoad` performs the redirect.
 */
export const Route = createFileRoute('/seller/$')({
  component: () => null,
})
