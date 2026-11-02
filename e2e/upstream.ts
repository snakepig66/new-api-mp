// e2e/upstream.ts — 假 OpenAI / Anthropic 兼容上游服务
// 端口 3202，支持 /v1/chat/completions（含 stream）、/v1/messages、/v1/models
// 返回的 model 字段故意设定为不同的内部名称以检验隐藏上游名。

const PORT = Number(process.env.UPSTREAM_PORT || 3202)

const HIDDEN_OPENAI_MODEL = 'hidden-upstream-openai-v1'
const HIDDEN_ANTHROPIC_MODEL = 'hidden-upstream-claude-v1'

function checkAuth(req: Request): { error?: { status: number; body: object } } {
  const authHeader = req.headers.get('authorization') || ''
  const xApiKey = req.headers.get('x-api-key') || ''
  const key = (authHeader.replace(/^Bearer\s+/i, '') || xApiKey).trim()

  if (key.includes('invalid') || key.includes('401') || key.includes('badkey')) {
    return {
      error: {
        status: 401,
        body: {
          error: {
            message: 'Incorrect API key provided: ' + key,
            type: 'invalid_request_error',
            code: 'invalid_api_key',
          },
        },
      },
    }
  }

  if (key.includes('402')) {
    return {
      error: {
        status: 402,
        body: {
          error: {
            message: 'Your account balance is insufficient for this operation.',
            type: 'insufficient_balance',
            code: 'insufficient_balance',
          },
        },
      },
    }
  }

  if (key.includes('429') || key.includes('quota') || key.includes('arrears')) {
    return {
      error: {
        status: 429,
        body: {
          error: {
            message: 'You exceeded your current quota, please check your plan and billing details.',
            type: 'insufficient_quota',
            code: 'insufficient_quota',
          },
        },
      },
    }
  }

  return {}
}

const server = Bun.serve({
  port: PORT,
  async fetch(req) {
    const url = new URL(req.url)
    const path = url.pathname.replace(/^\/v1/, '') // normalize /v1/xxx and /xxx

    // CORS preflight
    if (req.method === 'OPTIONS') {
      return new Response(null, {
        headers: {
          'Access-Control-Allow-Origin': '*',
          'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
          'Access-Control-Allow-Headers': '*',
        },
      })
    }

    // Health check
    if (path === '/healthz' || path === '/ping') {
      return new Response('OK', { status: 200 })
    }

    // Auth check
    const auth = checkAuth(req)
    if (auth.error) {
      return new Response(JSON.stringify(auth.error.body), {
        status: auth.error.status,
        headers: { 'Content-Type': 'application/json' },
      })
    }

    // 1. /models
    if (req.method === 'GET' && path === '/models') {
      const payload = {
        object: 'list',
        data: [
          { id: 'deepseek-chat', object: 'model', created: 1700000000, owned_by: 'custom-upstream' },
          { id: 'deepseek-flash', object: 'model', created: 1700000000, owned_by: 'custom-upstream' },
          { id: 'gpt-4o-mini', object: 'model', created: 1700000000, owned_by: 'custom-upstream' },
          { id: 'claude-3-5-sonnet-20241022', object: 'model', created: 1700000000, owned_by: 'custom-upstream' },
        ],
      }
      return new Response(JSON.stringify(payload), {
        headers: { 'Content-Type': 'application/json' },
      })
    }

    // 2. /user/balance (DeepSeek balance probe format)
    if (req.method === 'GET' && (path === '/user/balance' || path === '/balance')) {
      return new Response(
        JSON.stringify({
          balance_infos: [{ currency: 'CNY', total_balance: '888.88' }],
        }),
        { headers: { 'Content-Type': 'application/json' } },
      )
    }

    // 3. /chat/completions (OpenAI format)
    if (req.method === 'POST' && path === '/chat/completions') {
      let body: any = {}
      try {
        body = await req.json()
      } catch {
        body = {}
      }

      const isStream = Boolean(body.stream)

      if (isStream) {
        const stream = new ReadableStream({
          start(controller) {
            const encoder = new TextEncoder()
            const chunk1 = {
              id: 'chatcmpl-e2e-1',
              object: 'chat.completion.chunk',
              created: 1700000000,
              model: HIDDEN_OPENAI_MODEL,
              choices: [{ index: 0, delta: { role: 'assistant', content: '你好！' }, finish_reason: null }],
            }
            const chunk2 = {
              id: 'chatcmpl-e2e-1',
              object: 'chat.completion.chunk',
              created: 1700000000,
              model: HIDDEN_OPENAI_MODEL,
              choices: [{ index: 0, delta: { content: '这是来自真实假上游的回复。' }, finish_reason: null }],
            }
            const chunk3 = {
              id: 'chatcmpl-e2e-1',
              object: 'chat.completion.chunk',
              created: 1700000000,
              model: HIDDEN_OPENAI_MODEL,
              choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
              usage: {
                prompt_tokens: 12,
                completion_tokens: 8,
                total_tokens: 20,
              },
            }

            controller.enqueue(encoder.encode(`data: ${JSON.stringify(chunk1)}\n\n`))
            controller.enqueue(encoder.encode(`data: ${JSON.stringify(chunk2)}\n\n`))
            controller.enqueue(encoder.encode(`data: ${JSON.stringify(chunk3)}\n\n`))
            controller.enqueue(encoder.encode('data: [DONE]\n\n'))
            controller.close()
          },
        })

        return new Response(stream, {
          headers: {
            'Content-Type': 'text/event-stream; charset=utf-8',
            'Cache-Control': 'no-cache',
            Connection: 'keep-alive',
          },
        })
      }

      // Non-stream
      return new Response(
        JSON.stringify({
          id: 'chatcmpl-e2e-1',
          object: 'chat.completion',
          created: 1700000000,
          model: HIDDEN_OPENAI_MODEL,
          choices: [
            {
              index: 0,
              message: {
                role: 'assistant',
                content: '你好！这是来自真实假上游的非流式回复。',
              },
              finish_reason: 'stop',
            },
          ],
          usage: {
            prompt_tokens: 12,
            completion_tokens: 8,
            total_tokens: 20,
            prompt_cache_hit_tokens: 0,
            prompt_cache_miss_tokens: 12,
          },
        }),
        { headers: { 'Content-Type': 'application/json' } },
      )
    }

    // 4. /messages (Anthropic format)
    if (req.method === 'POST' && path === '/messages') {
      let body: any = {}
      try {
        body = await req.json()
      } catch {
        body = {}
      }

      const isStream = Boolean(body.stream)

      if (isStream) {
        const stream = new ReadableStream({
          start(controller) {
            const encoder = new TextEncoder()
            const e1 = `event: message_start\ndata: ${JSON.stringify({
              type: 'message_start',
              message: {
                id: 'msg_e2e_1',
                type: 'message',
                role: 'assistant',
                content: [],
                model: HIDDEN_ANTHROPIC_MODEL,
                stop_reason: null,
                stop_sequence: null,
                usage: { input_tokens: 10, output_tokens: 0 },
              },
            })}\n\n`
            const e2 = `event: content_block_start\ndata: ${JSON.stringify({
              type: 'content_block_start',
              index: 0,
              content_block: { type: 'text', text: '' },
            })}\n\n`
            const e3 = `event: content_block_delta\ndata: ${JSON.stringify({
              type: 'content_block_delta',
              index: 0,
              delta: { type: 'text_delta', text: 'Hello from Anthropic fake upstream!' },
            })}\n\n`
            const e4 = `event: content_block_stop\ndata: ${JSON.stringify({
              type: 'content_block_stop',
              index: 0,
            })}\n\n`
            const e5 = `event: message_delta\ndata: ${JSON.stringify({
              type: 'message_delta',
              delta: { stop_reason: 'end_turn', stop_sequence: null },
              usage: { output_tokens: 6 },
            })}\n\n`
            const e6 = 'event: message_stop\ndata: {"type":"message_stop"}\n\n'

            controller.enqueue(encoder.encode(e1 + e2 + e3 + e4 + e5 + e6))
            controller.close()
          },
        })

        return new Response(stream, {
          headers: {
            'Content-Type': 'text/event-stream; charset=utf-8',
            'Cache-Control': 'no-cache',
            Connection: 'keep-alive',
          },
        })
      }

      return new Response(
        JSON.stringify({
          id: 'msg_e2e_1',
          type: 'message',
          role: 'assistant',
          content: [
            {
              type: 'text',
              text: 'Hello from Anthropic fake upstream!',
            },
          ],
          model: HIDDEN_ANTHROPIC_MODEL,
          stop_reason: 'end_turn',
          stop_sequence: null,
          usage: {
            input_tokens: 10,
            output_tokens: 6,
          },
        }),
        { headers: { 'Content-Type': 'application/json' } },
      )
    }

    return new Response(JSON.stringify({ error: 'not found' }), {
      status: 404,
      headers: { 'Content-Type': 'application/json' },
    })
  },
})

console.log(`[fake-upstream] running on http://127.0.0.1:${server.port}`)
