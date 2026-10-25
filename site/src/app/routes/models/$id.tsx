import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import {
  squareModelQuery,
  type MarketTier,
  type SquareChannel,
  type SquareModelDetail,
  type SquareTierPrices,
} from '@/features/market/api'
import { AddToKeyMenu } from '@/features/market/AddToKeyMenu'
import { capabilitiesOf } from '@/features/market/capabilities'
import { CodeBlock } from '@/features/market/code-block'
import { CopyButton } from '@/features/market/copy-button'
import { PeakBar } from '@/features/market/peak-bar'
import { QueryError } from '@/features/market/query-error'
import { currentBaseUrl } from '@/features/market/url'
import { isOfficialVendor, toVendorLookup, vendorLabel } from '@/features/market/vendors'
import { vendorsQuery } from '@/features/seller/api'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { formatMultiplier } from '@/lib/multiplier'
import {
  Empty,
  Multiplier,
  PeakBadge,
  Price,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  buttonClasses,
} from '@/ui'

export const Route = createFileRoute('/models/$id')({
  component: ModelDetailPage,
})

type CodeTab = 'curl' | 'python' | 'node'

const PRICE_TIERS: MarketTier[] = ['peak', 'offpeak', 'flat']

function otherTierOf(tier: MarketTier): 'peak' | 'offpeak' | null {
  if (tier === 'peak') return 'offpeak'
  if (tier === 'offpeak') return 'peak'
  return null
}

function currentPrices(detail: SquareModelDetail): SquareTierPrices | undefined {
  return detail.prices[detail.tier] ?? detail.prices.peak ?? detail.prices.offpeak
}

/** 128000 -> "128K"; smaller values keep their digits. */
function formatContextLength(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return copy.common.none
  if (value >= 1_000_000) return `${Number((value / 1_000_000).toFixed(1))}M`
  if (value >= 1_000) return `${Number((value / 1_000).toFixed(1))}K`
  return String(value)
}

const VISION_KEYS = new Set(['vision', 'images', 'image', 'supports_vision', 'multimodal'])
const NO_IMAGE_KEYS = new Set(['no_images', 'no_vision', 'images_disabled'])

function differenceValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) return value.map((item) => differenceValue(item)).join('、')
  return JSON.stringify(value)
}

/**
 * Turn the backend's differences object into short human labels. Known keys
 * get dedicated copy; anything else is shown as `key: value` so a new
 * backend field is never silently hidden.
 */
function differenceNotes(differences: Record<string, unknown>): string[] {
  const labels = copy.pages.modelDetail.channels.differences
  const notes: string[] = []
  const context = differences.context_length
  if (typeof context === 'number') {
    notes.push(`${labels.contextLength} ${formatContextLength(context)}`)
  } else if (typeof context === 'string' && context.trim()) {
    notes.push(`${labels.contextLength} ${context.trim()}`)
  }

  let noVision = false
  for (const key of VISION_KEYS) {
    if (differences[key] === false) noVision = true
  }
  for (const key of NO_IMAGE_KEYS) {
    if (differences[key] === true) noVision = true
  }
  if (noVision) notes.push(labels.noVision)

  for (const [key, value] of Object.entries(differences)) {
    if (key === 'context_length' || VISION_KEYS.has(key) || NO_IMAGE_KEYS.has(key)) continue
    if (value === null || value === undefined || value === '') continue
    notes.push(`${key}: ${differenceValue(value)}`)
  }
  return notes
}

function ChannelKindTag({ channel }: { channel: SquareChannel }) {
  const kinds = copy.pages.modelDetail.channels.kind
  if (channel.kind === 'official') return <Tag tone="brand">{kinds.official}</Tag>
  if (channel.kind === 'plan') return <Tag>{kinds.plan}</Tag>
  return <Tag>{kinds.thirdParty}</Tag>
}

function DetailSkeleton() {
  return (
    <div className="space-y-6" aria-busy="true">
      <div className="h-8 w-64 animate-pulse rounded-sm bg-line" />
      <div className="h-4 w-full max-w-xl animate-pulse rounded-sm bg-line" />
      <div className="h-64 animate-pulse rounded-lg bg-bg-soft" />
      <div className="h-40 animate-pulse rounded-lg bg-bg-soft" />
    </div>
  )
}

function InfoRow({
  label,
  value,
  copyValue,
}: {
  label: string
  value: string
  copyValue?: string
}) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-line py-2.5 last:border-b-0">
      <span className="shrink-0 text-[13px] text-ink-3">{label}</span>
      <div className="flex min-w-0 items-center gap-1">
        <code className="truncate text-[13px] tabular-nums text-ink">{value}</code>
        <CopyButton text={copyValue ?? value} />
      </div>
    </div>
  )
}

export function ModelDetailPage() {
  const { id } = Route.useParams()
  const page = copy.pages.modelDetail
  const detail = useQuery(squareModelQuery(id))
  const vendors = useQuery(vendorsQuery())
  const vendorMeta = toVendorLookup(vendors.data ?? [])
  const baseUrl = currentBaseUrl()

  if (detail.isPending) {
    return (
      <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
        <p className="text-[13px] text-ink-3">{page.loading}</p>
        <div className="mt-8">
          <DetailSkeleton />
        </div>
      </div>
    )
  }

  if (detail.isError) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    return (
      <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
        <h1 className="text-[32px] font-semibold tracking-tight text-ink">{id}</h1>
        <div className="mt-8">
          {notFound ? (
            <Empty
              title={page.empty.title}
              reason={page.empty.reason(id)}
              next={page.empty.next}
              action={
                <Link to="/models" className={buttonClasses('secondary', 'md')}>
                  {copy.actions.backToModels}
                </Link>
              }
            />
          ) : (
            <QueryError
              title={page.error.title}
              reason={page.error.reason}
              onRetry={() => void detail.refetch()}
              retrying={detail.isFetching}
            />
          )}
        </div>
      </div>
    )
  }

  const data = detail.data
  const originVendor = data.origin_vendor || data.vendor
  const price = currentPrices(data)
  const otherTier = otherTierOf(data.tier)
  const otherPrice = otherTier ? data.prices[otherTier] : undefined
  const tiers = PRICE_TIERS.filter((tier) => data.official_prices[tier])
  const capabilities = capabilitiesOf(data.model)
  const officialOnly =
    data.buyer_price_source === 'official' || data.listing_count === 0
  const channels = data.channels ?? []
  const dimensions = [
    { key: 'input_micro_per_mtok', label: page.dimensions.input },
    { key: 'output_micro_per_mtok', label: page.dimensions.output },
    { key: 'cache_read_micro_per_mtok', label: page.dimensions.cacheRead },
    { key: 'cache_write_micro_per_mtok', label: page.dimensions.cacheWrite },
  ] as const

  const codeTabs = (Object.keys(page.code.tabs) as CodeTab[]).map((key) => ({
    key,
    label: page.code.tabs[key],
    code: page.code[key](baseUrl, data.model),
  }))

  return (
    <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
      <header>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1 className="text-[32px] font-semibold leading-tight tracking-tight text-ink">
            {data.model}
          </h1>
          <Tag>{vendorLabel(originVendor, vendorMeta)}</Tag>
          {isOfficialVendor(originVendor, vendorMeta) ? (
            <Tag tone="brand">{copy.pages.models.officialChannel}</Tag>
          ) : null}
        </div>
        {capabilities.length > 0 ? (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {capabilities.map((key) => (
              <Tag key={key} tone="brand">
                {copy.pages.models.capabilities[key]}
              </Tag>
            ))}
          </div>
        ) : null}
      </header>

      <div className="mt-10 grid gap-8 lg:grid-cols-[minmax(0,1fr)_360px] lg:items-start">
        <div className="min-w-0 space-y-8">
          <section className="rounded-lg border border-line p-6 sm:p-8">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div>
                <div className="text-[13px] text-ink-3">
                  {officialOnly ? page.officialPrice : page.lowestPrice}
                </div>
                {price ? (
                  <div className="mt-2 flex flex-wrap items-center gap-3">
                    <Price
                      value={price.input_micro_per_mtok}
                      tier={data.tier}
                      other={
                        otherPrice && otherTier
                          ? { value: otherPrice.input_micro_per_mtok, tier: otherTier }
                          : null
                      }
                      size="lg"
                      className="[&_span.tabular-nums]:text-[32px] [&_span.tabular-nums]:font-semibold"
                    />
                    {officialOnly ? (
                      <Tag tone="brand">{page.officialPrice}</Tag>
                    ) : data.multiplier_min_milli > 0 ? (
                      <Multiplier milli={data.multiplier_min_milli} />
                    ) : null}
                  </div>
                ) : (
                  <p className="mt-2 text-[15px] text-ink-3">{copy.common.none}</p>
                )}
                {price ? (
                  <p className="mt-2 text-[14px] text-ink-2">
                    {copy.pages.models.outputPrefix}{' '}
                    <span className="tabular-nums text-ink">
                      {formatYuan(price.output_micro_per_mtok)}
                    </span>
                    {otherPrice ? (
                      <span className="tabular-nums text-ink-3">
                        {' '}
                        / {formatYuan(otherPrice.output_micro_per_mtok)}
                      </span>
                    ) : null}
                  </p>
                ) : null}
              </div>
              {data.tier === 'flat' ? (
                <Tag>{copy.pages.models.flat}</Tag>
              ) : (
                <PeakBadge tier={data.tier} nextSwitchAt={data.next_switch} />
              )}
            </div>

            <div className="mt-8 border-t border-line pt-6">
              <h2 className="text-[16px] font-semibold tracking-tight text-ink">
                {page.sections.official}
              </h2>
              <p className="mt-1 text-[13px] text-ink-3">{page.tierNote}</p>

              {tiers.length === 0 ? (
                <div className="mt-3">
                  <Empty
                    title={page.noOfficial.title}
                    reason={page.noOfficial.reason}
                    next={page.noOfficial.next}
                  />
                </div>
              ) : (
                <div className="mt-4">
                  <Table minWidth={520} size="compact">
                    <TableHead>
                      <TableRow interactive={false}>
                        <TableHeaderCell>{page.officialColumns.dimension}</TableHeaderCell>
                        {tiers.map((tier) => (
                          <TableHeaderCell key={tier} numeric>
                            <span className="inline-flex items-center gap-1.5">
                              {tier === 'flat'
                                ? page.officialColumns.flat
                                : copy.tiers[tier]}
                              {tier === data.tier ? (
                                <span
                                  className="h-1.5 w-1.5 rounded-full bg-brand"
                                  aria-hidden="true"
                                />
                              ) : null}
                            </span>
                          </TableHeaderCell>
                        ))}
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {dimensions.map((dimension) => (
                        <TableRow key={dimension.key}>
                          <TableCell className="text-ink-2">{dimension.label}</TableCell>
                          {tiers.map((tier) => (
                            <TableCell
                              key={tier}
                              numeric
                              className={cn(
                                'tabular-nums',
                                tier === data.tier ? 'bg-brand-soft/50' : undefined,
                              )}
                            >
                              {formatYuan(data.official_prices[tier]![dimension.key])}
                            </TableCell>
                          ))}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              )}
            </div>

            {data.tier !== 'flat' ? (
              <div className="mt-8 border-t border-line pt-6">
                <h2 className="text-[16px] font-semibold tracking-tight text-ink">
                  {page.peakBar.title}
                </h2>
                <PeakBar nextSwitchAt={data.next_switch} className="mt-4" />
              </div>
            ) : null}
          </section>

          <section>
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h2 className="text-[24px] font-semibold tracking-tight text-ink">
                {page.sections.channels}
              </h2>
              <span className="text-[13px] text-ink-3">
                {page.channels.count(channels.length, data.listing_count)}
              </span>
            </div>
            <div className="mt-4">
              {channels.length === 0 ? (
                <Empty
                  title={page.channels.empty.title}
                  reason={page.channels.empty.reason}
                  next={page.channels.empty.next}
                />
              ) : (
                <Table minWidth={760} size="compact">
                  <TableHead>
                    <TableRow interactive={false}>
                      <TableHeaderCell>{page.channels.columns.channel}</TableHeaderCell>
                      <TableHeaderCell>{page.channels.columns.kind}</TableHeaderCell>
                      <TableHeaderCell>{page.channels.columns.verified}</TableHeaderCell>
                      <TableHeaderCell numeric>{page.channels.columns.coefficient}</TableHeaderCell>
                      <TableHeaderCell numeric>{page.channels.columns.listings}</TableHeaderCell>
                      <TableHeaderCell numeric>{page.channels.columns.multiplier}</TableHeaderCell>
                      <TableHeaderCell>{page.channels.columns.differences}</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {channels.map((channel) => {
                      const notes = differenceNotes(channel.differences ?? {})
                      return (
                        <TableRow key={channel.channel_slug}>
                          <TableCell>
                            <div className="font-medium text-ink">{channel.name}</div>
                            <div className="tabular-nums text-[13px] text-ink-3">
                              {channel.channel_slug}
                            </div>
                          </TableCell>
                          <TableCell>
                            <ChannelKindTag channel={channel} />
                          </TableCell>
                          <TableCell>
                            {channel.verified ? (
                              <Tag tone="brand" dot>
                                {page.channels.verified}
                              </Tag>
                            ) : (
                              <span className="text-[13px] text-ink-3">
                                {page.channels.unverified}
                              </span>
                            )}
                          </TableCell>
                          <TableCell numeric>{formatMultiplier(channel.coefficient_milli)}x</TableCell>
                          <TableCell numeric>{channel.listing_count}</TableCell>
                          <TableCell numeric>
                            {channel.listing_count > 0 ? (
                              <>
                                {formatMultiplier(channel.min_multiplier_milli)}x –{' '}
                                {formatMultiplier(channel.max_multiplier_milli)}x
                              </>
                            ) : (
                              <span className="text-ink-3">{copy.common.none}</span>
                            )}
                          </TableCell>
                          <TableCell>
                            {notes.length === 0 ? (
                              <span className="text-[13px] text-ink-3">
                                {page.channels.differences.none}
                              </span>
                            ) : (
                              <div className="flex flex-wrap gap-1.5">
                                {notes.map((note) => (
                                  <Tag key={note}>{note}</Tag>
                                ))}
                              </div>
                            )}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              )}
            </div>
            <p className="mt-3 text-[13px] leading-6 text-ink-3">{page.channels.note}</p>
          </section>
        </div>

        <aside className="lg:sticky lg:top-24">
          <div className="rounded-lg border border-line p-6">
            <h2 className="text-[16px] font-semibold tracking-tight text-ink">
              {page.access.title}
            </h2>
            <div className="mt-3">
              <InfoRow label={page.access.baseUrl} value={`${baseUrl}/v1`} />
              <InfoRow label={page.access.model} value={data.model} />
            </div>

            <div className="mt-5">
              <CodeBlock tabs={codeTabs} />
            </div>

            <p className="mt-3 text-[13px] leading-6 text-ink-3">{page.code.note}</p>

            <AddToKeyMenu
              model={data.model}
              hasSupply={data.listing_count > 0}
              size="lg"
              className="mt-5 w-full"
            />
          </div>
        </aside>
      </div>
    </div>
  )
}
