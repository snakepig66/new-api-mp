const countFormatter = new Intl.NumberFormat('zh-CN')

/** Group a plain count (requests, listings); never used for money. */
export function formatCount(value: number | string): string {
  const number = typeof value === 'string' ? Number(value) : value
  if (!Number.isFinite(number)) return '—'
  return countFormatter.format(number)
}
