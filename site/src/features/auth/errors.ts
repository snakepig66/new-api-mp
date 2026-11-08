import { ApiError } from '@/lib/api'

/**
 * Upstream auth handlers answer English messages (controller/user.go). Showing
 * them raw to a Chinese UI is worse than a generic line, so known causes are
 * mapped to specific copy and everything unknown falls back to the caller's
 * generic message. Never surfaces raw backend text (P4: 错误提示具体化).
 */
const KNOWN: ReadonlyArray<readonly [RegExp, string]> = [
  [/banned/i, '账号已被封禁，请联系管理员。'],
  [/password login has been disabled/i, '管理员已关闭密码登录。'],
  [/new user registration has been disabled/i, '管理员已关闭新用户注册。'],
  [/password registration has been disabled/i, '管理员已关闭密码注册，请使用第三方账号登录。'],
  [/email verification is enabled/i, '请填写邮箱和邮箱验证码。'],
  [/verification code is incorrect or has expired/i, '验证码不正确或已过期。'],
  [/email address is already in use/i, '该邮箱已被注册。'],
  [/username already exists/i, '该用户名已被占用。'],
  [/database error/i, '服务暂时不可用，请稍后重试。'],
  [/timeout|timed out/i, '服务响应超时，请稍后重试。'],
  [/too many requests/i, '操作过于频繁，请稍后再试。'],
  [/verification failed|invalid code|code is invalid/i, '验证码不正确或已过期，请重试。'],
  [/flow.*(expired|invalid)|auth flow/i, '登录流程已过期，请返回重新登录。'],
]

export function authErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return error.message || fallback
    const message = error.message ?? ''
    for (const [pattern, text] of KNOWN) {
      if (pattern.test(message)) return text
    }
  }
  return fallback
}
