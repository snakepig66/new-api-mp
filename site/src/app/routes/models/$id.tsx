import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import {
  addModelToKey,
  buyerTokensQuery,
  squareModelQuery,
  type MarketTier,
  type SquareModelDetail,
  type SquareTierPrices,
} from '@/features/market/api'
import { capabilitiesOf } from '@/features/market/capabilities'
import { CodeBlock } from '@/features/market/code-block'
import { CopyButton } from '@/features/market/copy-button'
import { PeakBar } from '@/features/market/peak-bar'
import { QueryError } from '@/features/market/query-error'
import { currentBaseUrl } from '@/features/market/url'
import { vendorLabel } from '@/features/market/vendors'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { formatMultiplier } from '@/lib/multiplier'
import {
  Button,
  Dialog,
  DialogClose,
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
  useToast,
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

function addErrorCopy(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 404) return copy.errors.token_not_found
    return error.message
  }
  return copy.pages.modelDetail.addToKey.failed
}

function AddToKeyButton({ model, className }: { model: string; className?: string }) {
  const page = copy.pages.modelDetail.addToKey
  const { isLoggedIn } = useAuth()
  const navigate = useNavigate()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)

  const tokens = useQuery({ ...buyerTokensQuery(), enabled: isLoggedIn })
  const enabledTokens = (tokens.data?.items ?? []).filter((token) => token.status === 1)

  const mutation = useMutation({
    mutationFn: (tokenId: number) => addModelToKey(tokenId, model),
    onSuccess: (_data, tokenId) => {
      const token = enabledTokens.find((item) => item.id === tokenId)
      toast.show({
        title: page.success(token?.name || `#${tokenId}`),
        tone: 'success',
      })
      setOpen(false)
    },
    onError: (error) => {
      toast.show({ title: addErrorCopy(error), tone: 'error' })
    },
  })

  function submit(tokenId: number) {
    mutation.mutate(tokenId)
  }

  function handleClick() {
    if (!isLoggedIn) {
      navigate({
        to: '/login',
        search: {
          redirect: `${window.location.pathname}${window.location.search}`,
        },
      })
      return
    }
    if (tokens.isPending) return
    if (tokens.isError) {
      toast.show({ title: page.failed, description: copy.errors.UNKNOWN, tone: 'error' })
      return
    }
    if (enabledTokens.length === 0) {
      setOpen(true)
      return
    }
    if (enabledTokens.length === 1) {
      submit(enabledTokens[0].id)
      return
    }
    setSelected(enabledTokens[0].id)
    setOpen(true)
  }

  const loading = mutation.isPending || (isLoggedIn && tokens.isPending)

  return (
    <>
      <Button size="lg" onClick={handleClick} loading={loading} className={className}>
        {mutation.isPending ? page.adding : page.action}
      </Button>

      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={page.dialogTitle}
        description={page.dialogDescription}
        footer={
          enabledTokens.length === 0 ? (
            <>
              <DialogClose asChild>
                <Button variant="secondary">{copy.actions.close}</Button>
              </DialogClose>
              <Link to="/console" className={buttonClasses('primary', 'md')}>
                {page.goConsole}
              </Link>
            </>
          ) : (
            <>
              <DialogClose asChild>
                <Button variant="secondary">{copy.actions.close}</Button>
              </DialogClose>
              <Button
                loading={mutation.isPending}
                disabled={selected === null}
                onClick={() => {
                  if (selected !== null) submit(selected)
                }}
              >
                {page.choose}
              </Button>
            </>
          )
        }
      >
        {tokens.isPending ? (
          <p className="text-[14px] text-ink-2">{copy.common.loading}</p>
        ) : tokens.isError ? (
          <div>
            <p className="text-[14px] text-ink-2">{page.failed}</p>
            <Button
              variant="secondary"
              size="sm"
              className="mt-3"
              onClick={() => void tokens.refetch()}
            >
              {copy.actions.retry}
            </Button>
          </div>
        ) : enabledTokens.length === 0 ? (
          <Empty
            className="border-0 px-0 py-0"
            title={page.noKeysTitle}
            reason={page.noKeysReason}
            next={page.noKeysNext}
          />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {enabledTokens.map((token) => (
              <li key={token.id}>
                <label className="flex cursor-pointer items-center gap-3 py-3 text-[14px]">
                  <input
                    type="radio"
                    name="key-choice"
                    checked={selected === token.id}
                    onChange={() => setSelected(token.id)}
                    className="h-4 w-4 accent-brand"
                  />
                  <span className="min-w-0 flex-1 truncate text-ink">
                    {token.name || `Key #${token.id}`}
                  </span>
                  <span className="tabular-nums text-[13px] text-ink-3">#{token.id}</span>
                </label>
              </li>
            ))}
          </ul>
        )}
      </Dialog>
    </>
  )
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

function ModelDetailPage() {
  const { id } = Route.useParams()
  const page = copy.pages.modelDetail
  const detail = useQuery(squareModelQuery(id))
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
  const price = currentPrices(data)
  const otherTier = otherTierOf(data.tier)
  const otherPrice = otherTier ? data.prices[otherTier] : undefined
  const tiers = PRICE_TIERS.filter((tier) => data.official_prices[tier])
  const capabilities = capabilitiesOf(data.model)
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
          <Tag>{vendorLabel(data.vendor)}</Tag>
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
                <div className="text-[13px] text-ink-3">{page.lowestPrice}</div>
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
                    <Multiplier milli={data.multiplier_min_milli} />
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
            <h2 className="text-[24px] font-semibold tracking-tight text-ink">
              {page.sections.listings}
            </h2>
            <div className="mt-4">
              <Table minWidth={480} size="compact">
                <TableHead>
                  <TableRow interactive={false}>
                    <TableHeaderCell>指标</TableHeaderCell>
                    <TableHeaderCell numeric>数值</TableHeaderCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  <TableRow>
                    <TableCell className="text-ink-2">{page.listingAggregate.range}</TableCell>
                    <TableCell numeric>
                      {formatMultiplier(data.multiplier_min_milli)}x –{' '}
                      {formatMultiplier(data.multiplier_max_milli)}x
                    </TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell className="text-ink-2">{page.listingAggregate.min}</TableCell>
                    <TableCell numeric>
                      <Multiplier milli={data.multiplier_min_milli} />
                    </TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell className="text-ink-2">{page.listingAggregate.max}</TableCell>
                    <TableCell numeric>
                      {formatMultiplier(data.multiplier_max_milli)}x
                    </TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell className="text-ink-2">{page.meta.listings}</TableCell>
                    <TableCell numeric>
                      {data.listing_count}
                      <span className="ml-0.5 text-[13px] text-ink-3">
                        {copy.pages.models.sellerUnit}
                      </span>
                    </TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </div>
            <p className="mt-3 text-[13px] leading-6 text-ink-3">{page.listingAggregate.note}</p>
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

            <AddToKeyButton model={data.model} className="mt-5 w-full" />
          </div>
        </aside>
      </div>
    </div>
  )
}
