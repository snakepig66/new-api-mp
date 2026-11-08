import { describe, expect, it } from 'vitest'

import { ApiError } from '@/lib/api'

import { authErrorMessage } from './errors'

describe('authErrorMessage', () => {
  it('maps known backend causes to specific Chinese copy', () => {
    expect(
      authErrorMessage(
        new ApiError(400, undefined, 'Username or password is incorrect, or user has been banned'),
        'fallback',
      ),
    ).toBe('账号已被封禁，请联系管理员。')
    expect(
      authErrorMessage(
        new ApiError(400, undefined, 'Email address is already in use'),
        'fallback',
      ),
    ).toBe('该邮箱已被注册。')
    expect(
      authErrorMessage(
        new ApiError(400, undefined, 'Verification code is incorrect or has expired'),
        'fallback',
      ),
    ).toBe('验证码不正确或已过期。')
  })

  it('keeps network messages and falls back for unknown causes', () => {
    expect(authErrorMessage(new ApiError(0, undefined, '网络异常'), 'fallback')).toBe('网络异常')
    expect(authErrorMessage(new ApiError(500, undefined, 'boom'), 'fallback')).toBe('fallback')
    expect(authErrorMessage(new Error('boom'), 'fallback')).toBe('fallback')
  })
})
