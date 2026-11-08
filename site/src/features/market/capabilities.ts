/**
 * Capability filters for the model square.
 *
 * The square API exposes no capability metadata (only model, vendor, price,
 * tier and listing count), so the sidebar groups models by name heuristics.
 * These tags are a filter aid, NOT a supplier promise: the hint under the
 * filter group says so. Replace this module with price-book metadata as soon
 * as the backend publishes capability flags.
 */

export const CAPABILITY_KEYS = ['reasoning', 'vision', 'tools', 'search'] as const

export type CapabilityKey = (typeof CAPABILITY_KEYS)[number]

interface CapabilityRule {
  key: CapabilityKey
  test: RegExp
}

const RULES: CapabilityRule[] = [
  // Reasoning families: model names that advertise a thinking/reasoning mode.
  { key: 'reasoning', test: /(^|[-_./])(r1|o1|o3|o4|reasoner|reasoning|thinking|qwq|qvq)([-_./]|$)|deepseek-r|qwen3/i },
  // Vision / multimodal families.
  { key: 'vision', test: /(vl|vision|omni|multimodal|image|gpt-4o|gpt-4\.1|claude-(3|4)|gemini)/i },
  // Chat families that support tool/function calling in practice.
  { key: 'tools', test: /(gpt-|claude|qwen|glm|deepseek|kimi|moonshot|mistral|llama|command|gemini|grok|doubao|minimax|yi-)/i },
  // Models with a built-in search / online mode.
  { key: 'search', test: /(search|sonar|perplexity|online|web)/i },
]

/** Capability tags inferred from a model id. */
export function capabilitiesOf(model: string): CapabilityKey[] {
  return RULES.filter((rule) => rule.test.test(model)).map((rule) => rule.key)
}

/** Keep models matching any selected capability (empty selection keeps all). */
export function matchesCapabilities(model: string, selected: readonly CapabilityKey[]): boolean {
  if (selected.length === 0) return true
  const tags = capabilitiesOf(model)
  return selected.some((key) => tags.includes(key))
}
