/**
 * Account security calls used by the console account page.
 *
 * Changing a password is a two-step operation on the backend:
 *  1. POST /api/verify with the current password issues a one-use proof bound
 *     to the `account.password.change` scope (GET /api/verify/methods lists the
 *     accepted methods).
 *  2. PUT /api/user/self consumes the proof, checks original_password again and
 *     rotates the login session, returning a fresh access bundle.
 *
 * Both steps are required; sending the password without the proof answers
 * SECURITY_PROOF_REQUIRED.
 */

import { ApiError, api } from '@/lib/api'
import { applyAuthBundle, type AuthBundlePayload } from '@/lib/session'

import { copy } from '@/copy'

const page = copy.pages.consoleAccount

export const PASSWORD_CHANGE_SCOPE = 'account.password.change'

export interface SelfProfile {
  id: number
  username: string
  display_name?: string
  has_password?: boolean
  role?: number
  email?: string
}

interface VerificationMethodOption {
  method: string
  available: boolean
  reason?: string
}

interface VerificationRequirements {
  scope: string
  methods: VerificationMethodOption[]
  password_encryption_enabled?: boolean
}

interface SecurityProof {
  proof_token: string
  expires_at: number
  method: string
  scope: string
}

export async function fetchSelfProfile(): Promise<SelfProfile> {
  return api.get<SelfProfile>('/user/self')
}

/** Ask the backend for a one-use password-change proof. */
export async function requestPasswordChangeProof(currentPassword: string): Promise<string> {
  const requirements = await api.get<VerificationRequirements>('/verify/methods', {
    query: { scope: PASSWORD_CHANGE_SCOPE },
  })
  if (requirements.password_encryption_enabled) {
    throw new ApiError(400, 'PASSWORD_ENCRYPTION_UNSUPPORTED', page.password.encryptionUnsupported)
  }
  const passwordMethod = requirements.methods.find((method) => method.method === 'password')
  if (!passwordMethod?.available) {
    throw new ApiError(
      400,
      'PASSWORD_VERIFY_UNAVAILABLE',
      passwordMethod?.reason ?? page.password.verifyUnavailable,
    )
  }
  const proof = await api.post<SecurityProof>('/verify', {
    method: 'password',
    scope: PASSWORD_CHANGE_SCOPE,
    password: currentPassword,
  })
  if (!proof?.proof_token) {
    throw new ApiError(400, undefined, page.password.failed)
  }
  return proof.proof_token
}

/** Consume the proof and change the password, then keep the new session. */
export async function changeAccountPassword(
  currentPassword: string,
  newPassword: string,
  proofToken: string,
): Promise<void> {
  const bundle = await api.put<AuthBundlePayload>('/user/self', {
    password: newPassword,
    original_password: currentPassword,
  }, {
    headers: { 'X-Security-Proof': proofToken },
  })
  if (bundle?.access_token) applyAuthBundle(bundle)
}

/** Request a 6-digit email verification code sent to the logged-in user's email. */
export async function sendPasswordChangeEmailCode(): Promise<void> {
  await api.post('/user/password/email/code')
}

/** Reset account password using email verification code and keep the rotated session. */
export async function changePasswordByEmail(
  code: string,
  newPassword: string,
): Promise<void> {
  const bundle = await api.post<AuthBundlePayload>('/user/password/email/reset', {
    code,
    new_password: newPassword,
  })
  if (bundle?.access_token) applyAuthBundle(bundle)
}
