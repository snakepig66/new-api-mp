// e2e/fake_upstream.ts
// Fake OpenAI / Anthropic upstream for E2E testing
const PORT = Number(process.env.FAKE_UPSTREAM_PORT || 3203);

const server = Bun.serve({
  port: PORT,
  async fetch(req) {
    const url = new URL(req.url);
    console.log(`[fake-upstream] ${req.method} ${url.pathname}`);

    // GET /v1/models
    if (req.method === 'GET' && (url.pathname === '/v1/models' || url.pathname === '/models')) {
      return Response.json({
        object: 'list',
        data: [
          { id: 'gpt-4o', object: 'model', created: 1700000000, owned_by: 'fake-vendor' },
          { id: 'claude-3-5-sonnet', object: 'model', created: 1700000000, owned_by: 'fake-vendor' },
          { id: 'deepseek-chat', object: 'model', created: 1700000000, owned_by: 'fake-vendor' },
        ],
      });
    }

    // POST /v1/chat/completions
    if (req.method === 'POST' && (url.pathname === '/v1/chat/completions' || url.pathname === '/chat/completions')) {
      let body: any = {};
      try {
        body = await req.json();
      } catch (e) {}

      const isStream = Boolean(body.stream);
      // Notice requirement: 故意让返回的 model 字段是另一个名字以检验隐藏上游名
      const upstreamModelName = "upstream-secret-raw-model";

      if (!isStream) {
        return Response.json({
          id: 'chatcmpl-' + Math.random().toString(36).slice(2),
          object: 'chat.completion',
          created: Math.floor(Date.now() / 1000),
          model: upstreamModelName,
          choices: [
            {
              index: 0,
              message: {
                role: 'assistant',
                content: 'Hello from fake upstream! Received prompt for model ' + (body.model || 'unknown'),
              },
              finish_reason: 'stop',
            },
          ],
          usage: {
            prompt_tokens: 12,
            completion_tokens: 8,
            total_tokens: 20,
          },
        });
      }

      // Stream response
      const stream = new ReadableStream({
        async start(controller) {
          const enc = new TextEncoder();
          const chunk1 = {
            id: 'chatcmpl-' + Math.random().toString(36).slice(2),
            object: 'chat.completion.chunk',
            created: Math.floor(Date.now() / 1000),
            model: upstreamModelName,
            choices: [{ index: 0, delta: { role: 'assistant', content: 'Hello ' }, finish_reason: null }],
          };
          const chunk2 = {
            id: 'chatcmpl-' + Math.random().toString(36).slice(2),
            object: 'chat.completion.chunk',
            created: Math.floor(Date.now() / 1000),
            model: upstreamModelName,
            choices: [{ index: 0, delta: { content: 'stream from fake upstream!' }, finish_reason: null }],
          };
          const chunk3 = {
            id: 'chatcmpl-' + Math.random().toString(36).slice(2),
            object: 'chat.completion.chunk',
            created: Math.floor(Date.now() / 1000),
            model: upstreamModelName,
            choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
            usage: {
              prompt_tokens: 12,
              completion_tokens: 8,
              total_tokens: 20,
            },
          };

          controller.enqueue(enc.encode(`data: ${JSON.stringify(chunk1)}\n\n`));
          await Bun.sleep(10);
          controller.enqueue(enc.encode(`data: ${JSON.stringify(chunk2)}\n\n`));
          await Bun.sleep(10);
          controller.enqueue(enc.encode(`data: ${JSON.stringify(chunk3)}\n\n`));
          controller.enqueue(enc.encode(`data: [DONE]\n\n`));
          controller.close();
        },
      });

      return new Response(stream, {
        headers: {
          'Content-Type': 'text/event-stream; charset=utf-8',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        },
      });
    }

    // POST /v1/messages (Anthropic)
    if (req.method === 'POST' && (url.pathname === '/v1/messages' || url.pathname === '/messages')) {
      let body: any = {};
      try {
        body = await req.json();
      } catch (e) {}

      const isStream = Boolean(body.stream);
      const upstreamModelName = "upstream-secret-raw-claude";

      if (!isStream) {
        return Response.json({
          id: 'msg_' + Math.random().toString(36).slice(2),
          type: 'message',
          role: 'assistant',
          content: [
            {
              type: 'text',
              text: 'Hello from fake upstream Claude! Model requested: ' + (body.model || 'unknown'),
            },
          ],
          model: upstreamModelName,
          stop_reason: 'end_turn',
          usage: {
            input_tokens: 15,
            output_tokens: 10,
          },
        });
      }

      // Stream response for Anthropic
      const stream = new ReadableStream({
        async start(controller) {
          const enc = new TextEncoder();
          controller.enqueue(enc.encode(`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"${upstreamModelName}","stop_reason":null,"usage":{"input_tokens":15,"output_tokens":1}}}\n\n`));
          await Bun.sleep(10);
          controller.enqueue(enc.encode(`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n`));
          await Bun.sleep(10);
          controller.enqueue(enc.encode(`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello Claude stream from fake upstream!"}}\n\n`));
          await Bun.sleep(10);
          controller.enqueue(enc.encode(`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n`));
          await Bun.sleep(10);
          controller.enqueue(enc.encode(`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}\n\n`));
          controller.enqueue(enc.encode(`event: message_stop\ndata: {"type":"message_stop"}\n\n`));
          controller.close();
        },
      });

      return new Response(stream, {
        headers: {
          'Content-Type': 'text/event-stream; charset=utf-8',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        },
      });
    }

    return new Response(JSON.stringify({ error: 'not found', path: url.pathname }), { status: 404 });
  },
});

console.log(`Fake upstream listening on http://127.0.0.1:${server.port}`);
