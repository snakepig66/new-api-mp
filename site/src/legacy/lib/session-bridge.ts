import { setUser as setSiteUser, type AuthUser as SiteAuthUser } from '@/lib/session'

import type { AdminCapabilities } from '@/legacy/lib/admin-permissions'
import type { AuthUser, LoginSession } from '@/legacy/stores/auth-store'

type LegacyAuth = {
  setBundle: (bundle: {
    access_token: string
    token_type: string
    access_expires_at: number
    user: AuthUser
    session: LoginSession
  }) => void
  setUser: (user: AuthUser | null) => void
  setBootstrapState: (state: 'idle' | 'checking' | 'complete') => void
}

// The site backend exposes admin permission capabilities under this key.
const CAPABILITIES_KEY = 'admin_permissions'

function readCapabilities(user: SiteAuthUser): AdminCapabilities | undefined {
  const value = user[CAPABILITIES_KEY]
  if (value && typeof value === 'object') {
    return value as AdminCapabilities
  }
  return undefined
}

function toLegacyUser(user: SiteAuthUser): AuthUser {
  const capabilities = readCapabilities(user)
  const mapped: AuthUser = {
    ...(user as unknown as AuthUser),
    id: user.id,
    username: user.username,
    role: typeof user.role === 'number' ? user.role : 0,
  }
  if (capabilities) {
    mapped.permissions = {
      ...(mapped.permissions ?? {}),
      admin_permissions: capabilities,
    }
  }
  return mapped
}

function toLegacySession(sid: string, expiresAt: number): LoginSession {
  const now = Math.floor(Date.now() / 1000)
  return {
    sid: sid || `site-${now}`,
    current: true,
    login_method: 'site',
    ip: '',
    user_agent: '',
    created_at: now,
    last_active_at: now,
    expires_at: expiresAt || now,
  }
}

/**
 * Mirror the site session into the ported new-api auth store so ported pages
 * can read `auth.user` (role, permissions, ...) without owning the login flow.
 */
export function syncLegacyAuthFromSite(
  auth: LegacyAuth,
  site: {
    accessToken: string
    accessExpiresAt: number
    sid: string
    user: SiteAuthUser | null
  }
): void {
  if (!site.user) {
    auth.setUser(null)
    auth.setBootstrapState('complete')
    return
  }
  auth.setBundle({
    access_token: site.accessToken,
    token_type: 'Bearer',
    access_expires_at: site.accessExpiresAt,
    user: toLegacyUser(site.user),
    session: toLegacySession(site.sid, site.accessExpiresAt),
  })
}

/** Write a user edited in the ported UI back into the site session. */
export function pushLegacyUserToSite(user: AuthUser): void {
  const siteUser: SiteAuthUser = {
    ...(user as unknown as SiteAuthUser),
    id: user.id,
    username: user.username,
    role: user.role,
  }
  setSiteUser(siteUser)
}
