import { describe, expect, it } from 'vitest'

import {
  CHUNK_ERROR_PATTERNS,
  chunkReloadKey,
  errorMessage,
  hasChunkReloadMarker,
  isChunkLoadError,
  shouldAutoReload,
} from './route-error'

function memoryStorage() {
  const values = new Map<string, string>()
  return {
    values,
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value)
    },
  }
}

describe('isChunkLoadError', () => {
  it('recognises both browser chunk failure messages', () => {
    for (const pattern of CHUNK_ERROR_PATTERNS) {
      expect(isChunkLoadError(new Error(`${pattern}: /assets/index-abc.js`))).toBe(true)
    }
  })

  it('accepts strings and nested causes, rejects ordinary errors', () => {
    expect(isChunkLoadError('Importing a module script failed.')).toBe(true)
    expect(
      isChunkLoadError(new Error('outer', { cause: new Error('Failed to fetch dynamically imported module') })),
    ).toBe(true)
    expect(isChunkLoadError(new Error('Request failed with status 500'))).toBe(false)
    expect(isChunkLoadError(null)).toBe(false)
  })

  it('extracts the message for display', () => {
    expect(errorMessage(new Error('boom'))).toBe('boom')
    expect(errorMessage('plain')).toBe('plain')
    expect(errorMessage({ message: 'object error' })).toBe('object error')
    expect(errorMessage(undefined)).toBe('')
  })
})

describe('shouldAutoReload', () => {
  it('allows one reload per path and then leaves a marker', () => {
    const storage = memoryStorage()
    const error = new Error('Failed to fetch dynamically imported module')

    expect(shouldAutoReload(error, '/models/deepseek-v3', storage)).toBe(true)
    expect(storage.getItem(chunkReloadKey('/models/deepseek-v3'))).toBe('1')
    expect(hasChunkReloadMarker('/models/deepseek-v3', storage)).toBe(true)

    // Second failure after the reload must not reload again.
    expect(shouldAutoReload(error, '/models/deepseek-v3', storage)).toBe(false)

    // Another path still gets its own one-shot reload.
    expect(shouldAutoReload(error, '/models/other', storage)).toBe(true)
  })

  it('never reloads for non-chunk errors and writes no marker', () => {
    const storage = memoryStorage()
    expect(shouldAutoReload(new Error('validation failed'), '/docs', storage)).toBe(false)
    expect(storage.values.size).toBe(0)
  })

  it('uses the pathname in the sessionStorage key', () => {
    expect(chunkReloadKey('/models/123')).toBe('mp.chunk-reloaded./models/123')
    expect(chunkReloadKey('')).toBe('mp.chunk-reloaded./')
  })
})
