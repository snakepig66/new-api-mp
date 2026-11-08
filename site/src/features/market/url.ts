/**
 * The origin the visitor is actually using. Code samples fill base_url with
 * this value so a copy-paste works against the deployment the reader sees
 * (including local dev through the Vite proxy).
 */
export function currentBaseUrl(): string {
  if (typeof window === 'undefined') return ''
  return window.location.origin
}
