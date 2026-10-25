/**
 * Vendor display names and channel metadata for the square's vendor pills.
 *
 * The square API only carries a vendor slug. Display names come from the
 * public `/marketplace/vendors` catalogue when it is available; otherwise we
 * fall back to a small slug→label table, and finally to the raw slug so an
 * unknown vendor still renders.
 */

export interface VendorMeta {
  display_name: string
  /** Channel tier from the vendors catalogue, e.g. `official`. */
  tier: string
  /** Channel pool kind: official / plan / third_party. */
  kind?: string
  /** Root-relative mark shipped in site/public/vendors; empty means monogram. */
  logo_url?: string
}

/** A metadata lookup keyed by vendor slug (Map or plain object). */
export type VendorLookup = ReadonlyMap<string, VendorMeta> | Readonly<Record<string, VendorMeta>>

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

function metaOf(vendor: string, lookup?: VendorLookup): VendorMeta | undefined {
  if (!lookup) return undefined
  if (typeof (lookup as ReadonlyMap<string, VendorMeta>).get === 'function') {
    return (lookup as ReadonlyMap<string, VendorMeta>).get(vendor)
  }
  return (lookup as Readonly<Record<string, VendorMeta>>)[vendor]
}

export function vendorLabel(vendor: string, lookup?: VendorLookup): string {
  const meta = metaOf(vendor, lookup)
  if (meta?.display_name) return meta.display_name
  const hit = VENDOR_LABELS.find((entry) => entry.match.test(vendor))
  return hit ? hit.label : vendor
}

/**
 * Short brand name for the vendor pill. The catalogue keeps the official
 * display names (e.g. 阿里云百炼), but the square's pill row is narrow and
 * buyers recognize 千问/豆包 better.
 */
const VENDOR_SHORT_LABELS: { match: RegExp; label: string }[] = [
  { match: /^deepseek/i, label: 'DeepSeek' },
  { match: /^zhipu/i, label: '智谱' },
  { match: /^aliyun-bailian|^qwen/i, label: '千问' },
  { match: /^moonshot|^kimi/i, label: 'Kimi' },
  { match: /^minimax/i, label: 'MiniMax' },
  { match: /^volcengine-ark|^doubao|^bytedance/i, label: '豆包' },
]

export function vendorShortLabel(vendor: string, lookup?: VendorLookup): string {
  const hit = VENDOR_SHORT_LABELS.find((entry) => entry.match.test(vendor))
  return hit ? hit.label : vendorLabel(vendor, lookup)
}

/** Logo path from the catalogue; empty when the vendor has no shipped SVG. */
export function vendorLogo(vendor: string, lookup?: VendorLookup): string {
  return metaOf(vendor, lookup)?.logo_url?.trim() ?? ''
}

/** True when the catalogue classifies this vendor as an official channel. */
export function isOfficialVendor(vendor: string, lookup?: VendorLookup): boolean {
  const meta = metaOf(vendor, lookup)
  if (!meta) return false
  // Pooled vendors carry `kind`; the old catalogue only had `tier`.
  return meta.kind ? meta.kind === 'official' : meta.tier === 'official'
}

/** Build the id→{display_name,tier,kind} map used by the square and detail tags. */
export function toVendorLookup(
  vendors: Iterable<{
    id: string
    display_name: string
    tier: string
    kind?: string
    logo_url?: string
  }>,
): Map<string, VendorMeta> {
  const lookup = new Map<string, VendorMeta>()
  for (const vendor of vendors) {
    lookup.set(vendor.id, {
      display_name: vendor.display_name,
      tier: vendor.tier,
      kind: vendor.kind,
      logo_url: vendor.logo_url,
    })
  }
  return lookup
}

/** Distinct vendors in stable display order, labelled for the pill row. */
export function vendorOptions(
  vendors: Iterable<string>,
  lookup?: VendorLookup,
): { value: string; label: string }[] {
  const seen = new Set<string>()
  const options: { value: string; label: string }[] = []
  for (const vendor of vendors) {
    if (!vendor || seen.has(vendor)) continue
    seen.add(vendor)
    options.push({ value: vendor, label: vendorShortLabel(vendor, lookup) })
  }
  return options.sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
}
