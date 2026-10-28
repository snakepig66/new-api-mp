/**
 * Resolves brand logos for AI model identifiers.
 *
 * Strips any `provider/` prefix first, then matches the model name
 * case-insensitively against vendor rules to locate shipped brand marks in
 * `site/public/vendors/*.svg`.
 */

const RULES: { match: RegExp; vendor: string }[] = [
  { match: /deepseek/i, vendor: 'deepseek' },
  { match: /glm|zhipu/i, vendor: 'zhipu' },
  { match: /qwen|qwq/i, vendor: 'qwen' },
  { match: /kimi|moonshot/i, vendor: 'kimi' },
  { match: /minimax|abab/i, vendor: 'minimax' },
  { match: /doubao|^seed/i, vendor: 'doubao' },
  { match: /gpt|^o[134](?:-|$)|chatgpt|codex|text-embedding/i, vendor: 'openai' },
  { match: /claude/i, vendor: 'claude' },
  { match: /gemini|gemma/i, vendor: 'gemini' },
  { match: /muse/i, vendor: 'muse' },
  { match: /grok/i, vendor: 'grok' },
  { match: /mistral|mixtral|codestral|devstral/i, vendor: 'mistral' },
  { match: /llama/i, vendor: 'meta' },
  { match: /hunyuan/i, vendor: 'hunyuan' },
  { match: /ernie/i, vendor: 'wenxin' },
  { match: /^step/i, vendor: 'stepfun' },
  { match: /baichuan/i, vendor: 'baichuan' },
  { match: /^yi-/i, vendor: 'yi' },
  { match: /spark/i, vendor: 'spark' },
  { match: /longcat/i, vendor: 'longcat' },
  { match: /command/i, vendor: 'cohere' },
  { match: /sonar/i, vendor: 'perplexity' },
  { match: /nemotron/i, vendor: 'nvidia' },
]

export function stripProvider(model: string): string {
  if (!model) return ''
  const slash = model.lastIndexOf('/')
  return slash >= 0 ? model.slice(slash + 1) : model
}

export function modelVendor(model: string): string {
  const stripped = stripProvider(model)
  if (!stripped) return ''
  const hit = RULES.find((entry) => entry.match.test(stripped))
  return hit ? hit.vendor : ''
}

/** Root-relative path to the vendor SVG, or empty string when unmatched. */
export function modelLogo(model: string): string {
  const vendor = modelVendor(model)
  return vendor ? `/vendors/${vendor}.svg` : ''
}
