/**
 * Vendor display names for the square's vendor pills.
 *
 * The API only carries a vendor slug. The square generates its pills from the
 * distinct slugs it actually receives, so an unknown vendor still renders —
 * just with its raw slug instead of a Chinese label.
 */

const VENDOR_LABELS: { match: RegExp; label: string }[] = [
  { match: /deepseek/i, label: 'DeepSeek' },
  { match: /qwen|alibaba|dashscope/i, label: '阿里云' },
  { match: /zhipu|bigmodel|glm/i, label: '智谱' },
  { match: /openai/i, label: 'OpenAI' },
  { match: /anthropic|claude/i, label: 'Anthropic' },
  { match: /google|gemini/i, label: 'Google' },
  { match: /moonshot|kimi/i, label: '月之暗面' },
  { match: /minimax/i, label: 'MiniMax' },
  { match: /baidu|ernie/i, label: '百度' },
  { match: /bytedance|doubao|volc/i, label: '字节跳动' },
  { match: /xai|grok/i, label: 'xAI' },
  { match: /01ai|lingyi|yi-/i, label: '零一万物' },
]

export function vendorLabel(vendor: string): string {
  const hit = VENDOR_LABELS.find((entry) => entry.match.test(vendor))
  return hit ? hit.label : vendor
}

/** Distinct vendors in stable display order, labelled for the pill row. */
export function vendorOptions(vendors: Iterable<string>): { value: string; label: string }[] {
  const seen = new Set<string>()
  const options: { value: string; label: string }[] = []
  for (const vendor of vendors) {
    if (!vendor || seen.has(vendor)) continue
    seen.add(vendor)
    options.push({ value: vendor, label: vendorLabel(vendor) })
  }
  return options.sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
}
