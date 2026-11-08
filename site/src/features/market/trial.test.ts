import { describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { postTrial } from './trial'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: vi.fn(), post: vi.fn() },
  }
})

const mockedPost = vi.mocked(api.post)

describe('postTrial', () => {
  it('posts to the /pg relay path with the browser session and extracts the reply', async () => {
    mockedPost.mockResolvedValueOnce({
      choices: [{ message: { content: 'hello from the model' } }],
    } as never)
    const reply = await postTrial('gpt-4o', [{ role: 'user', content: 'hi' }])
    expect(reply).toBe('hello from the model')
    expect(mockedPost).toHaveBeenCalledWith(
      '/pg/chat/completions',
      {
        model: 'gpt-4o',
        messages: [{ role: 'user', content: 'hi' }],
        max_tokens: 512,
        stream: false,
      },
      expect.objectContaining({ signal: expect.anything() }),
    )
  })

  it('rejects an empty reply', async () => {
    mockedPost.mockResolvedValueOnce({ choices: [] } as never)
    await expect(postTrial('gpt-4o', [{ role: 'user', content: 'hi' }])).rejects.toThrow()
  })
})
