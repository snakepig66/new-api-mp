/**
 * Same-origin redirect handling. Never send the user to another host based on
 * a query string.
 */
export function safeRedirect(raw: string | undefined, fallback: string): string {
  if (!raw) return fallback
  try {
    const origin =
      typeof window === 'undefined' ? 'http://localhost' : window.location.origin
    const url = new URL(raw, origin)
    const expected = new URL(origin)
    if (url.origin !== expected.origin) return fallback
    return `${url.pathname}${url.search}${url.hash}`
  } catch {
    return fallback
  }
}
