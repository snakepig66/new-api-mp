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
import { CodeBlock } from '@/features/market/code-block'
import { QueryError } from '@/features/market/query-error'
import { currentBaseUrl } from '@/features/market/url'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { formatYuan } from '@/lib/money'
import { formatMultiplier } from '@/lib/multiplier'
import {
  Button,
  Dialog,
  DialogClose,
  Empty,
  Multiplier,
  PageHeader,
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

type CodeTab = 'openai' | 'anthropic' | 'curl'

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

function AddToKeyButton({ model }: { model: string }) {
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
      <Button onClick={handleClick} loading={loading}>
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
          <p className="text-[13px] text-muted">{copy.common.loading}</p>
        ) : tokens.isError ? (
          <div>
            <p className="text-[13px] text-muted">{page.failed}</p>
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
                <label className="flex cursor-pointer items-center gap-3 py-2.5 text-[13px]">
                  <input
                    type="radio"
                    name="key-choice"
                    checked={selected === token.id}
                    onChange={() => setSelected(token.id)}
                    className="h-3.5 w-3.5 accent-brand-dark"
                  />
                  <span className="min-w-0 flex-1 truncate text-ink">
                    {token.name || `Key #${token.id}`}
                  </span>
                  <span className="font-numeric text-[11.5px] text-muted">#{token.id}</span>
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
      <div className="h-8 w-64 animate-pulse rounded-[3px] bg-line" />
      <div className="h-4 w-full max-w-xl animate-pulse rounded-[3px] bg-line" />
      <div className="h-48 animate-pulse rounded-[3px] border border-line bg-wash" />
      <div className="h-32 animate-pulse rounded-[3px] border border-line bg-wash" />
    </div>
  )
}

function ModelDetailPage() {
  const { id } = Route.useParams()
  const page = copy.pages.modelDetail
  const detail = useQuery(squareModelQuery(id))
  const [tab, setTab] = useState<CodeTab>('openai')

  if (detail.isPending) {
    return (
      <div className="mx-auto max-w-[1180px] px-5 py-10">
        <PageHeader title={id} lead={page.loading} />
        <div className="mt-8">
          <DetailSkeleton />
        </div>
      </div>
    )
  }

  if (detail.isError) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    return (
      <div className="mx-auto max-w-[1180px] px-5 py-10">
        <PageHeader title={id} lead={page.lead} />
        <div className="mt-6">
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
  const baseUrl = currentBaseUrl()
  const codeFor = {
    openai: page.code.openai,
    anthropic: page.code.anthropic,
    curl: page.code.curl,
  }
  const tabLabel = page.code.tabs[tab]
  const dimensions = [
    { key: 'input_micro_per_mtok', label: page.dimensions.input },
    { key: 'output_micro_per_mtok', label: page.dimensions.output },
    { key: 'cache_read_micro_per_mtok', label: page.dimensions.cacheRead },
    { key: 'cache_write_micro_per_mtok', label: page.dimensions.cacheWrite },
  ] as const

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-10">
      <PageHeader
        title={data.model}
        lead={page.lead}
        actions={
          <>
            {data.tier === 'flat' ? (
              <Tag>{copy.pages.models.flat}</Tag>
            ) : (
              <PeakBadge tier={data.tier} nextSwitchAt={data.next_switch} />
            )}
            <AddToKeyButton model={data.model} />
          </>
        }
      />

      <div className="mt-5 flex flex-wrap items-center gap-x-6 gap-y-2 text-[12px] text-muted">
        <span>
          {page.meta.vendor} <span className="text-ink">{data.vendor}</span>
        </span>
        <span>
          {page.meta.listings}{' '}
          <span className="font-numeric text-ink">{data.listing_count}</span>
        </span>
        <span>
          {page.meta.tier}{' '}
          <span className="text-ink">
            {data.tier_label || copy.tiers[data.tier] || copy.common.none}
          </span>
        </span>
      </div>

      <div className="mt-8 grid gap-8 lg:grid-cols-[minmax(0,1fr)_280px] lg:items-start">
        <section>
          <h2 className="text-[15px] font-semibold tracking-tight text-ink">
            {page.sections.official}
          </h2>
          <p className="mt-1 text-[11.5px] text-muted">{page.tierNote}</p>

          {tiers.length === 0 ? (
            <div className="mt-3">
              <Empty
                title={page.noOfficial.title}
                reason={page.noOfficial.reason}
                next={page.noOfficial.next}
              />
            </div>
          ) : (
            <div className="mt-3">
              <Table minWidth={520}>
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
                            <span className="h-1.5 w-1.5 rounded-full bg-brand" aria-hidden="true" />
                          ) : null}
                        </span>
                      </TableHeaderCell>
                    ))}
                  </TableRow>
                </TableHead>
                <TableBody>
                  {dimensions.map((dimension) => (
                    <TableRow key={dimension.key}>
                      <TableCell className="text-muted">{dimension.label}</TableCell>
                      {tiers.map((tier) => (
                        <TableCell
                          key={tier}
                          numeric
                          className={tier === data.tier ? 'bg-brand-soft/40' : undefined}
                        >
                          <span className="font-numeric tabular-nums text-ink">
                            {formatYuan(data.official_prices[tier]![dimension.key])}
                          </span>
                        </TableCell>
                      ))}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </section>

        <aside className="border border-line bg-surface">
          <div className="border-b border-line px-4 py-3">
            <div className="text-[11.5px] text-muted">{page.buyerPrice}</div>
            {price ? (
              <div className="mt-1">
                <Price
                  value={price.input_micro_per_mtok}
                  tier={data.tier}
                  other={
                    otherPrice && otherTier
                      ? { value: otherPrice.input_micro_per_mtok, tier: otherTier }
                      : null
                  }
                  size="lg"
                />
                <p className="mt-1 text-[11.5px] text-muted">
                  {copy.pages.models.outputPrefix}{' '}
                  <span className="font-numeric tabular-nums text-ink">
                    {formatYuan(price.output_micro_per_mtok)}
                  </span>
                  {otherPrice ? (
                    <span className="font-numeric tabular-nums text-muted/80">
                      {' '}
                      / {formatYuan(otherPrice.output_micro_per_mtok)}
                    </span>
                  ) : null}
                </p>
              </div>
            ) : (
              <p className="mt-1 text-[13px] text-muted">{copy.common.none}</p>
            )}
          </div>

          <dl className="divide-y divide-line text-[12.5px]">
            <div className="flex items-baseline justify-between gap-3 px-4 py-2.5">
              <dt className="text-muted">{page.listingAggregate.min}</dt>
              <dd>
                <Multiplier milli={data.multiplier_min_milli} />
              </dd>
            </div>
            <div className="flex items-baseline justify-between gap-3 px-4 py-2.5">
              <dt className="text-muted">{page.listingAggregate.max}</dt>
              <dd>
                <Multiplier milli={data.multiplier_max_milli} showSaving={false} />
              </dd>
            </div>
            <div className="flex items-baseline justify-between gap-3 px-4 py-2.5">
              <dt className="text-muted">{page.meta.listings}</dt>
              <dd className="font-numeric tabular-nums text-ink">{data.listing_count}</dd>
            </div>
          </dl>
        </aside>
      </div>

      <section className="mt-10">
        <h2 className="text-[15px] font-semibold tracking-tight text-ink">
          {page.sections.listings}
        </h2>
        <div className="mt-3 grid border-y border-line text-[12.5px] sm:grid-cols-3 sm:divide-x sm:divide-line">
          <div className="py-3 sm:pr-5">
            <div className="text-[11.5px] text-muted">{page.listingAggregate.range}</div>
            <div className="mt-1 font-numeric text-[15px] tabular-nums text-ink">
              {formatMultiplier(data.multiplier_min_milli)}x –{' '}
              {formatMultiplier(data.multiplier_max_milli)}x
            </div>
          </div>
          <div className="border-t border-line py-3 sm:border-t-0 sm:px-5">
            <div className="text-[11.5px] text-muted">{page.meta.listings}</div>
            <div className="mt-1 font-numeric text-[15px] tabular-nums text-ink">
              {data.listing_count}
            </div>
          </div>
          <div className="border-t border-line py-3 sm:border-t-0 sm:pl-5">
            <div className="text-[11.5px] text-muted">{copy.pages.models.columns.tier}</div>
            <div className="mt-1">
              {data.tier === 'flat' ? (
                <Tag>{copy.pages.models.flat}</Tag>
              ) : (
                <PeakBadge tier={data.tier} nextSwitchAt={data.next_switch} />
              )}
            </div>
          </div>
        </div>
        <p className="mt-2 text-[11.5px] leading-5 text-muted">{page.listingAggregate.note}</p>
      </section>

      <section className="mt-10">
        <h2 className="text-[15px] font-semibold tracking-tight text-ink">{page.sections.code}</h2>
        <div className="mt-3" role="tablist" aria-label={page.code.aria}>
          {(Object.keys(page.code.tabs) as CodeTab[]).map((key) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={tab === key}
              onClick={() => setTab(key)}
              className={
                tab === key
                  ? 'border-b-2 border-brand-dark px-3 py-1.5 text-[12.5px] font-medium text-ink'
                  : 'border-b-2 border-transparent px-3 py-1.5 text-[12.5px] text-muted hover:text-ink'
              }
            >
              {page.code.tabs[key]}
            </button>
          ))}
        </div>
        <div className="mt-3 max-w-[820px]">
          <CodeBlock code={codeFor[tab](baseUrl, data.model)} label={tabLabel} />
          <p className="mt-2 text-[11.5px] leading-5 text-muted">{page.code.note}</p>
        </div>
      </section>
    </div>
  )
}
