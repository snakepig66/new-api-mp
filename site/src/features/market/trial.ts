import { copy } from '@/copy'
import { ApiError, api } from '@/lib/api'

export interface TrialMessage {
  role: 'user' | 'assistant'
  content: string
}

const TRIAL_TIMEOUT_MS = 90_000

/**
 * One-shot playground call (POST /pg/chat/completions). The route is served
 * outside /api and authenticates with the browser session (middleware.UserAuth
 * + Distribute), so no key is involved: the reply is billed to the signed-in
 * user's balance at official prices. Only official (plaza) models expose this.
 */
export async function postTrial(
  model: string,
  messages: TrialMessage[],
): Promise<string> {
  const signal =
    typeof AbortSignal !== 'undefined' && typeof AbortSignal.timeout === 'function'
      ? AbortSignal.timeout(TRIAL_TIMEOUT_MS)
      : undefined
  const payload = await api.post<{
    choices?: Array<{ message?: { content?: string } }>
  }>(
    '/pg/chat/completions',
    { model, messages, max_tokens: 512, stream: false },
    { signal },
  )
  const content = payload?.choices?.[0]?.message?.content
  if (!content) throw new ApiError(200, undefined, copy.pages.modelDetail.trial.emptyReply)
  return content
}
