import { describe, expect, it } from 'vitest'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'

import { adminErrorMessage } from './errors'

const errors = copy.pages.adminCommon.errors

describe('adminErrorMessage', () => {
  it('explains a model that is still referenced by a channel model', () => {
    const error = new ApiError(409, undefined, 'model is referenced by a channel model')
    expect(adminErrorMessage(error)).toBe(errors.modelInUse)
  })

  it('keeps the generic wording for other conflicts', () => {
    const error = new ApiError(409, undefined, 'version mismatch')
    expect(adminErrorMessage(error)).toBe(errors.conflict)
  })
})
