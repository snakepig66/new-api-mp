import { describe, expect, it } from 'vitest'

import { ApiError } from '@/lib/api'

import { duplicateKeyIssue, modelVerifyReason, sellerErrorMessage } from './errors'

describe('duplicateKeyIssue', () => {
  it('links the caller to their own listing', () => {
    const error = new ApiError(
      409,
      'DUPLICATE_KEY_OWN',
      'this api key is already listed by you',
      { listing_id: 42 },
    )
    expect(duplicateKeyIssue(error)).toEqual({ kind: 'own', listingId: 42 })
  })

  it('never exposes an identifier for another account', () => {
    const error = new ApiError(
      409,
      'DUPLICATE_KEY_OTHER',
      'this api key is already listed by another account',
      { listing_id: 7 },
    )
    expect(duplicateKeyIssue(error)).toEqual({ kind: 'other' })
  })

  it('accepts a string listing id and falls back to message matching', () => {
    expect(
      duplicateKeyIssue(
        new ApiError(409, 'DUPLICATE_KEY_OWN', 'x', { listing_id: '9' }),
      ),
    ).toEqual({ kind: 'own', listingId: 9 })
    expect(
      duplicateKeyIssue(new ApiError(409, undefined, 'this api key is already listed by you')),
    ).toEqual({ kind: 'own', listingId: undefined })
  })

  it('returns null for unrelated errors', () => {
    expect(duplicateKeyIssue(new ApiError(400, undefined, 'bad'))).toBeNull()
    expect(duplicateKeyIssue(new Error('boom'))).toBeNull()
  })
})

describe('sellerErrorMessage', () => {
  it('translates the plan-terms and verification codes', () => {
    expect(sellerErrorMessage(new ApiError(400, 'PLAN_TERMS_REQUIRED'))).toContain('套餐条款')
    expect(
      sellerErrorMessage(new ApiError(409, undefined, 'verification is already running')),
    ).toContain('检测')
  })
})

describe('modelVerifyReason', () => {
  it('explains a model missing from the key /models list', () => {
    expect(
      modelVerifyReason('该模型不在这个 Key 的可用模型列表中'),
    ).toContain('/models')
    expect(modelVerifyReason('model not in this key model list')).toContain('/models')
  })

  it('keeps other Chinese reasons readable', () => {
    expect(modelVerifyReason('上游余额不足')).toBe('上游余额不足')
    expect(modelVerifyReason('')).toBeTruthy()
  })
})
