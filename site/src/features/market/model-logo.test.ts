import { describe, expect, it } from 'vitest'

import { modelLogo, modelVendor, stripProvider } from './model-logo'

describe('modelLogo & modelVendor', () => {
  it('strips provider prefix before matching', () => {
    expect(stripProvider('openai/gpt-4o')).toBe('gpt-4o')
    expect(stripProvider('anthropic/claude-3-5-sonnet')).toBe('claude-3-5-sonnet')
    expect(stripProvider('deepseek-chat')).toBe('deepseek-chat')
    expect(modelLogo('openai/gpt-4o')).toBe('/vendors/openai.svg')
    expect(modelVendor('anthropic/claude-3-opus')).toBe('claude')
  })

  it('matches all 23 vendors case-insensitively', () => {
    // 1. deepseek
    expect(modelLogo('deepseek-chat')).toBe('/vendors/deepseek.svg')
    expect(modelLogo('DEEPSEEK-R1')).toBe('/vendors/deepseek.svg')

    // 2. zhipu (glm|zhipu)
    expect(modelLogo('glm-4-plus')).toBe('/vendors/zhipu.svg')
    expect(modelLogo('zhipu-v1')).toBe('/vendors/zhipu.svg')

    // 3. qwen (qwen|qwq)
    expect(modelLogo('qwen-max')).toBe('/vendors/qwen.svg')
    expect(modelLogo('qwq-32b-preview')).toBe('/vendors/qwen.svg')

    // 4. kimi (kimi|moonshot)
    expect(modelLogo('kimi-k1.5')).toBe('/vendors/kimi.svg')
    expect(modelLogo('moonshot-v1-8k')).toBe('/vendors/kimi.svg')

    // 5. minimax (minimax|abab)
    expect(modelLogo('minimax-text-01')).toBe('/vendors/minimax.svg')
    expect(modelLogo('abab6.5s-chat')).toBe('/vendors/minimax.svg')

    // 6. doubao (doubao|^seed)
    expect(modelLogo('doubao-pro-32k')).toBe('/vendors/doubao.svg')
    expect(modelLogo('seed-asr-2')).toBe('/vendors/doubao.svg')

    // 7. openai (gpt|^o[134](-|$)|chatgpt|codex|text-embedding)
    expect(modelLogo('gpt-4o')).toBe('/vendors/openai.svg')
    expect(modelLogo('o1')).toBe('/vendors/openai.svg')
    expect(modelLogo('o1-mini')).toBe('/vendors/openai.svg')
    expect(modelLogo('o3-mini')).toBe('/vendors/openai.svg')
    expect(modelLogo('o4-preview')).toBe('/vendors/openai.svg')
    expect(modelLogo('chatgpt-4o-latest')).toBe('/vendors/openai.svg')
    expect(modelLogo('codex-002')).toBe('/vendors/openai.svg')
    expect(modelLogo('text-embedding-3-small')).toBe('/vendors/openai.svg')

    // 8. claude
    expect(modelLogo('claude-3-7-sonnet')).toBe('/vendors/claude.svg')

    // 9. gemini (gemini|gemma)
    expect(modelLogo('gemini-2.0-flash')).toBe('/vendors/gemini.svg')
    expect(modelLogo('gemma-2-9b-it')).toBe('/vendors/gemini.svg')

    // 10. muse
    expect(modelLogo('muse-v1')).toBe('/vendors/muse.svg')

    // 11. grok
    expect(modelLogo('grok-2-1212')).toBe('/vendors/grok.svg')

    // 12. mistral (mistral|mixtral|codestral|devstral)
    expect(modelLogo('mistral-large')).toBe('/vendors/mistral.svg')
    expect(modelLogo('mixtral-8x7b')).toBe('/vendors/mistral.svg')
    expect(modelLogo('codestral-latest')).toBe('/vendors/mistral.svg')
    expect(modelLogo('devstral-small')).toBe('/vendors/mistral.svg')

    // 13. meta (llama)
    expect(modelLogo('llama-3.3-70b-instruct')).toBe('/vendors/meta.svg')

    // 14. hunyuan
    expect(modelLogo('hunyuan-standard')).toBe('/vendors/hunyuan.svg')

    // 15. wenxin (ernie)
    expect(modelLogo('ernie-4.0-8k')).toBe('/vendors/wenxin.svg')

    // 16. stepfun (^step)
    expect(modelLogo('step-1-32k')).toBe('/vendors/stepfun.svg')

    // 17. baichuan
    expect(modelLogo('baichuan4-air')).toBe('/vendors/baichuan.svg')

    // 18. yi (^yi-)
    expect(modelLogo('yi-large')).toBe('/vendors/yi.svg')

    // 19. spark
    expect(modelLogo('spark-max')).toBe('/vendors/spark.svg')

    // 20. longcat
    expect(modelLogo('longcat-chat')).toBe('/vendors/longcat.svg')

    // 21. cohere (command)
    expect(modelLogo('command-r-plus')).toBe('/vendors/cohere.svg')

    // 22. perplexity (sonar)
    expect(modelLogo('sonar-pro')).toBe('/vendors/perplexity.svg')

    // 23. nvidia (nemotron)
    expect(modelLogo('nemotron-4-340b')).toBe('/vendors/nvidia.svg')
  })

  it('returns empty string when no match is found', () => {
    expect(modelLogo('custom-finetune-model-xyz')).toBe('')
    expect(modelLogo('')).toBe('')
    expect(modelVendor('unknown-vendor-model')).toBe('')
  })
})
