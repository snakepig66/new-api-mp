import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatCountdown, formatDateTime } from '@/lib/time'
import {
  Button,
  Dialog,
  DropdownMenu,
  Empty,
  Input,
  Multiplier,
  PageHeader,
  Pagination,
  PasswordInput,
  Price,
  Segmented,
  Switch,
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

import {
  deleteListing,
  pauseListing,
  replaceListingKey,
  resumeListing,
  sellerChannelKind,
  sellerListingsHealthQuery,
  sellerListingsQuery,
  updateListingMultiplier,
  vendorsQuery,
  verifyListing,
  vendorName,
  type SellerChannelKind,
  type SellerListing,
  type SellerListingHealth,
} from '@/features/seller/api'
import { AsyncError, Field, notify, PageSkeleton } from '@/features/seller/components'
import {
  markMultiplierChanged,
  readCooldownUntil,
  remainingSeconds,
} from '@/features/seller/cooldown'
import { sellerErrorMessage } from '@/features/seller/errors'
import { ListingBatchBar } from '@/features/seller/ListingBatchBar'
import { ListingEditDrawer } from '@/features/seller/ListingEditDrawer'
import { ListingHealthCard } from '@/features/seller/ListingHealthCard'
import {
  isListingKeyReplaceable,
  isListingPausable,
  isListingReverifiable,
  isListingSellerResumable,
  listingStatusView,
  pauseReasonView,
} from '@/features/seller/status'

export const Route = createFileRoute('/console/listings/')({
  validateSearch: (search: Record<string, unknown>): { edit?: number } => {
    const parsed = Number(search.edit)
    return { edit: Number.isInteger(parsed) && parsed > 0 ? parsed : undefined }
  },
  component: SellerListingsPage,
})

const page = copy.pages.sellerListings
const common = copy.pages.sellerCommon

const PAGE_SIZE = 20

type ListingFilter = 'all' | 'running' | 'cooldown' | 'review' | 'abnormal'
type ListingView = 'table' | 'cards'

/**
 * 异常渠道：在售但没有任何启用的模型，或存在被自动停用的模型。
 * 后者同时在「模型」列上标红点。
 */
function isListingAbnormal(listing: SellerListing): boolean {
  if ((listing.auto_disabled_model_count ?? 0) > 0) return true
  return listing.status === 'live' && listing.enabled_model_count === 0
}

function matchesFilter(listing: SellerListing, filter: ListingFilter): boolean {
  switch (filter) {
    case 'running':
      return listing.status === 'live' && !isListingAbnormal(listing)
    case 'cooldown':
      return listing.status === 'paused'
    case 'review':
      return (
        listing.status === 'draft' ||
        listing.status === 'verifying' ||
        listing.status === 'canary'
      )
    case 'abnormal':
      return isListingAbnormal(listing)
    default:
      return true
  }
}

/** 滚动窗口成功率：-1（无样本）或请求数不足 5 时显示「—」。 */
function windowRate(milli: number | undefined, requests: number | undefined): string {
  if (milli === undefined || milli < 0) return '—'
  if (requests === undefined || requests < 5) return '—'
  const percent = milli / 10
  return `${Number.isInteger(percent) ? percent : percent.toFixed(1)}%`
}

function channelName(listing: SellerListing): string {
  return listing.label?.trim() || vendorName(listing.vendor)
}

function pauseTitle(listing: SellerListing): string {
  const reason = pauseReasonView(listing.pause_reason)
  return listing.resume_at
    ? `${reason} · ${page.resumeAt(formatDateTime(listing.resume_at))}`
    : reason
}

function useCooldown(listingId: number) {
  const [until, setUntil] = useState(() => readCooldownUntil(listingId))
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (until <= Date.now()) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [until])

  return {
    remaining: remainingSeconds(until, now),
    start() {
      const next = markMultiplierChanged(listingId)
      setUntil(next)
      setNow(Date.now())
    },
  }
}

function MultiplierDialog({
  listing,
  open,
  onClose,
}: {
  listing: SellerListing
  open: boolean
  onClose: () => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const cooldown = useCooldown(listing.id)
  const [milli, setMilli] = useState(listing.multiplier_milli)
  const [milliText, setMilliText] = useState((listing.multiplier_milli / 1000).toFixed(3))
  const [error, setError] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: (next: number) => updateListingMultiplier(listing.id, next),
    onSuccess: () => {
      cooldown.start()
      setError(null)
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
      notify(toast, { title: common.toast.multiplierUpdated, tone: 'success' })
      onClose()
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409) cooldown.start()
      setError(sellerErrorMessage(err))
    },
  })

  useEffect(() => {
    if (open) {
      setMilli(listing.multiplier_milli)
      setMilliText((listing.multiplier_milli / 1000).toFixed(3))
      setError(null)
    }
  }, [open, listing.multiplier_milli])

  const clamped = Number.isInteger(milli) ? Math.min(1000, Math.max(1, milli)) : 1
  const locked = cooldown.remaining > 0

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={page.multiplierDialog.title}
      description={page.multiplierDialog.lead}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {copy.actions.close}
          </Button>
          <Button
            disabled={locked}
            loading={mutation.isPending}
            onClick={() => mutation.mutate(clamped)}
          >
            {mutation.isPending
              ? page.multiplierDialog.submitting
              : page.multiplierDialog.submit}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex items-baseline justify-between gap-3 border-b border-line pb-3">
          <span className="text-[13px] text-ink-3">{vendorName(listing.vendor)}</span>
          <Multiplier milli={listing.multiplier_milli} />
        </div>

        <Field
          label={page.multiplierDialog.label}
          htmlFor="multiplier-input"
          hint={page.multiplierDialog.hint}
        >
          <div className="flex items-center gap-3">
            <input
              id="multiplier-input"
              type="range"
              min={1}
              max={1000}
              step={1}
              value={clamped}
              disabled={locked}
              onChange={(event) => {
                const next = Number(event.target.value)
                setMilli(next)
                setMilliText((next / 1000).toFixed(3))
              }}
              className="h-1.5 w-full cursor-pointer accent-brand disabled:cursor-not-allowed"
            />
            <Input
              aria-label={page.multiplierDialog.label}
              className="w-24 shrink-0 text-right"
              inputMode="decimal"
              disabled={locked}
              value={milliText}
              onChange={(event) => {
                const text = event.target.value
                setMilliText(text)
                const next = Number(text)
                if (Number.isFinite(next)) setMilli(Math.round(next * 1000))
              }}
            />
          </div>
        </Field>

        {locked ? (
          <p className="text-[13px] text-warn" role="status">
            {page.multiplierDialog.cooldown(formatCountdown(cooldown.remaining))}
          </p>
        ) : null}

        {error ? (
          <p className="text-[13px] text-danger" role="alert">
            {error}
          </p>
        ) : null}
      </div>
    </Dialog>
  )
}

function KeyDialog({
  listing,
  open,
  onClose,
}: {
  listing: SellerListing
  open: boolean
  onClose: () => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [apiKey, setApiKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: (key: string) => replaceListingKey(listing.id, key),
    onSuccess: () => {
      setApiKey('')
      setError(null)
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
      notify(toast, { title: common.toast.keyUpdated, tone: 'success' })
      onClose()
    },
    onError: (err) => setError(sellerErrorMessage(err)),
  })

  useEffect(() => {
    if (open) {
      setApiKey('')
      setError(null)
    }
  }, [open])

  const trimmed = apiKey.trim()
  const tail = trimmed.length >= 4 ? trimmed.slice(-4) : ''
  const preview = tail
    ? page.keyDialog.preview(tail)
    : page.keyDialog.previewEmpty

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={page.keyDialog.title}
      description={page.keyDialog.lead}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {copy.actions.close}
          </Button>
          <Button
            disabled={trimmed.length < 8}
            loading={mutation.isPending}
            onClick={() => mutation.mutate(trimmed)}
          >
            {mutation.isPending ? page.keyDialog.submitting : page.keyDialog.submit}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field
          label={page.keyDialog.label}
          htmlFor="replacement-key"
          hint={page.keyDialog.hint}
          error={error ?? undefined}
        >
          <PasswordInput
            id="replacement-key"
            autoComplete="new-password"
            spellCheck={false}
            placeholder={page.keyDialog.placeholder}
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>

        <div className="glass-inset px-4 py-3">
          <div className="text-[13px] text-ink-3">{common.keyPreviewHint}</div>
          <div className="mt-1 tabular-nums text-[14px] text-ink">{preview}</div>
        </div>
      </div>
    </Dialog>
  )
}

/** 状态筛选 chip：label + 数量，点击切换筛选。 */
function FilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string
  count: number
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={
        active
          ? 'inline-flex h-8 shrink-0 items-center gap-1.5 rounded-full bg-brand-soft px-3.5 text-[13px] font-medium text-brand-ink'
          : 'inline-flex h-8 shrink-0 items-center gap-1.5 rounded-full px-3.5 text-[13px] text-ink-2 transition-colors hover:bg-brand/[0.06] hover:text-ink'
      }
    >
      <span>{label}</span>
      <span className="tabular-nums">{count}</span>
    </button>
  )
}

function SuccessRateCell({ health }: { health: SellerListingHealth | null | undefined }) {
  const windows = [
    {
      label: page.windows.h1,
      value: windowRate(health?.success_rate_1h_milli, health?.requests_1h),
    },
    {
      label: page.windows.h24,
      value: windowRate(health?.success_rate_24h_milli, health?.requests_24h),
    },
    {
      label: page.windows.d7,
      value: windowRate(health?.success_rate_7d_milli, health?.requests_7d),
    },
  ]
  return (
    <div className="flex items-center gap-5">
      {windows.map((rate) => (
        <span key={rate.label} className="block min-w-[2.75rem]">
          <span className="block text-[12px] leading-4 text-ink-3">{rate.label}</span>
          <span className="block tabular-nums leading-5 text-ink">{rate.value}</span>
        </span>
      ))}
    </div>
  )
}

function ModelsCell({ listing }: { listing: SellerListing }) {
  const total = listing.model_count ?? listing.models.length
  const enabled = listing.enabled_model_count ?? total
  const disabled = listing.auto_disabled_model_count ?? 0
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      {disabled > 0 ? (
        <span
          role="img"
          aria-label={page.modelsCell.disabledHint(disabled)}
          title={page.modelsCell.disabledHint(disabled)}
          className="h-1.5 w-1.5 shrink-0 rounded-full bg-danger"
        />
      ) : null}
      <span className="tabular-nums">
        {enabled} / {total}
      </span>
    </span>
  )
}

function MultiplierCell({ listing }: { listing: SellerListing }) {
  const min = listing.multiplier_min_milli ?? listing.multiplier_milli
  const max = listing.multiplier_max_milli ?? listing.multiplier_milli
  if (min === max) return <Multiplier milli={min} />
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap">
      <Multiplier milli={min} showSaving={false} />
      <span className="text-ink-3">–</span>
      <Multiplier milli={max} showSaving={false} />
    </span>
  )
}

function KindTag({ kind }: { kind: SellerChannelKind }) {
  return <Tag tone={kind === 'official' ? 'brand' : 'neutral'}>{page.kinds[kind]}</Tag>
}

export function SellerListingsPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const navigate = Route.useNavigate()
  const { edit } = Route.useSearch()
  const listings = useQuery(sellerListingsQuery())
  const health = useQuery(sellerListingsHealthQuery())
  const vendors = useQuery(vendorsQuery())
  const [view, setView] = useState<ListingView>('table')
  const [filter, setFilter] = useState<ListingFilter>('all')
  const [pageNumber, setPageNumber] = useState(1)
  const [selectedIds, setSelectedIds] = useState<ReadonlySet<number>>(() => new Set<number>())
  const [multiplierTarget, setMultiplierTarget] = useState<SellerListing | null>(null)
  const [keyTarget, setKeyTarget] = useState<SellerListing | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SellerListing | null>(null)

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
  }

  function selectFilter(next: ListingFilter) {
    setFilter(next)
    setPageNumber(1)
  }

  const pause = useMutation({
    mutationFn: (id: number) => pauseListing(id),
    onSuccess: () => {
      refresh()
      notify(toast, { title: common.toast.paused, tone: 'success' })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })
  const resume = useMutation({
    mutationFn: (id: number) => resumeListing(id),
    onSuccess: () => {
      refresh()
      notify(toast, { title: common.toast.resumed, tone: 'success' })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })
  const verify = useMutation({
    mutationFn: (id: number) => verifyListing(id),
    onSuccess: (_data, id) => {
      // Flip the row to verifying in the cache right away: the background
      // goroutine starts a few milliseconds later, and this keeps the 3s
      // refetchInterval alive even if an immediate refetch still saw draft.
      queryClient.setQueryData<SellerListing[]>(
        sellerListingsQuery().queryKey,
        (rows) => rows?.map((row) => (row.id === id ? { ...row, status: 'verifying' } : row)),
      )
      // No immediate invalidate: a refetch that lands before the background
      // goroutine flips the row could stop the 3s polling. The interval does
      // the first refresh.
      notify(toast, { title: common.toast.verifyStarted, tone: 'success' })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })
  const remove = useMutation({
    mutationFn: (id: number) => deleteListing(id),
    onSuccess: (_data, id) => {
      refresh()
      setSelectedIds((current) => {
        const next = new Set(current)
        next.delete(id)
        return next
      })
      setDeleteTarget(null)
      notify(toast, { title: page.deleteDialog.done, tone: 'success' })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })

  /**
   * 复制配置：渠道向导目前不读取预填参数，这里把可复用的配置（模型、
   * 倍率、限制、时段）复制成 JSON 摘要，Key 与请求头绝不进入剪贴板。
   */
  async function copyListingConfig(listing: SellerListing): Promise<void> {
    const summary = {
      vendor: listing.vendor,
      models: listing.models,
      multiplier_milli: listing.multiplier_milli,
      multiplier_min_milli: listing.multiplier_min_milli,
      multiplier_max_milli: listing.multiplier_max_milli,
      concurrency_limit: listing.concurrency_limit,
      rpm_limit: listing.rpm_limit,
      daily_cap_micro: listing.daily_cap_micro,
      hours: listing.hours,
    }
    try {
      await navigator.clipboard.writeText(JSON.stringify(summary, null, 2))
      notify(toast, { title: page.copyConfig.toast, tone: 'success' })
    } catch {
      notify(toast, { title: page.copyConfig.failed, tone: 'error' })
    }
  }

  if (listings.isPending) return <PageSkeleton />

  if (listings.isError) {
    return (
      <AsyncError
        title={page.loadError}
        reason={sellerErrorMessage(listings.error)}
        onRetry={() => void listings.refetch()}
        retryLabel={copy.actions.retry}
      />
    )
  }

  const rows = listings.data ?? []
  const healthById = new Map((health.data ?? []).map((row) => [row.listing_id, row]))
  const kindByVendor = new Map<string, SellerChannelKind>()
  for (const vendor of vendors.data ?? []) kindByVendor.set(vendor.id, sellerChannelKind(vendor))
  const kindOf = (listing: SellerListing): SellerChannelKind =>
    kindByVendor.get(listing.vendor) ?? 'third_party'
  const kindById = new Map<number, SellerChannelKind>(rows.map((row) => [row.id, kindOf(row)]))

  const counts: Record<ListingFilter, number> = {
    all: rows.length,
    running: rows.filter((row) => matchesFilter(row, 'running')).length,
    cooldown: rows.filter((row) => matchesFilter(row, 'cooldown')).length,
    review: rows.filter((row) => matchesFilter(row, 'review')).length,
    abnormal: rows.filter((row) => matchesFilter(row, 'abnormal')).length,
  }
  const filtered = filter === 'all' ? rows : rows.filter((row) => matchesFilter(row, filter))
  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(pageNumber, totalPages)
  const paged = filtered.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE)
  const selected = rows.filter((row) => selectedIds.has(row.id))

  function toggleSelected(id: number, next: boolean) {
    setSelectedIds((current) => {
      const set = new Set(current)
      if (next) set.add(id)
      else set.delete(id)
      return set
    })
  }

  const allPagedSelected =
    paged.length > 0 && paged.every((row) => selectedIds.has(row.id))

  function toggleAllPaged(next: boolean) {
    setSelectedIds((current) => {
      const set = new Set(current)
      for (const row of paged) {
        if (next) set.add(row.id)
        else set.delete(row.id)
      }
      return set
    })
  }

  const chips: Array<{ key: ListingFilter; label: string }> = [
    { key: 'all', label: page.chips.all },
    { key: 'running', label: page.chips.running },
    { key: 'cooldown', label: page.chips.cooldown },
    { key: 'review', label: page.chips.review },
    { key: 'abnormal', label: page.chips.abnormal },
  ]

  function cardActions(listing: SellerListing) {
    const pausingThis = pause.isPending && pause.variables === listing.id
    const resumingThis = resume.isPending && resume.variables === listing.id
    const verifyingThis = verify.isPending && verify.variables === listing.id
    const verifying = listing.status === 'verifying'
    return (
      <>
        {isListingPausable(listing.status) ? (
          <Button
            variant="secondary"
            size="sm"
            loading={pausingThis}
            onClick={() => pause.mutate(listing.id)}
          >
            {page.actions.pause}
          </Button>
        ) : null}
        {isListingSellerResumable(listing) ? (
          <Button
            variant="primary"
            size="sm"
            loading={resumingThis}
            onClick={() => resume.mutate(listing.id)}
          >
            {page.actions.resume}
          </Button>
        ) : null}
        {isListingReverifiable(listing) || verifying ? (
          <Button
            variant="secondary"
            size="sm"
            loading={verifyingThis}
            disabled={verifying}
            title={verifying ? page.actions.rechecking : page.recheckHint}
            onClick={() => verify.mutate(listing.id)}
          >
            {verifyingThis ? page.actions.rechecking : page.actions.recheck}
          </Button>
        ) : null}
        <Button
          variant="ghost"
          size="sm"
          disabled={listing.status === 'banned'}
          onClick={() => setMultiplierTarget(listing)}
        >
          {page.actions.multiplier}
        </Button>
        {isListingKeyReplaceable(listing.status) ? (
          <Button variant="ghost" size="sm" onClick={() => setKeyTarget(listing)}>
            {page.actions.replaceKey}
          </Button>
        ) : null}
        <Link
          to="/console/listings/$id"
          params={{ id: String(listing.id) }}
          className="inline-flex h-8 items-center rounded-md px-3 text-[13px] font-medium text-brand-ink transition-colors hover:bg-brand-soft"
        >
          {page.health.detailLink}
        </Link>
      </>
    )
  }

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={refresh}>
              {page.actions.refresh}
            </Button>
            <Link
              to="/console/listings/new"
              className={buttonClasses('primary', 'sm')}
            >
              {page.actions.newListing}
            </Link>
          </>
        }
      />

      {rows.length === 0 ? (
        <Empty
          className="mt-6"
          title={page.emptyTitle}
          reason={page.emptyReason}
          next={page.emptyNext}
          action={
            <Link
              to="/console/listings/new"
              className={buttonClasses('primary', 'md')}
            >
              {page.actions.newListing}
            </Link>
          }
        />
      ) : (
        <>
          <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
            <nav
              className="flex items-center gap-1.5 overflow-x-auto"
              aria-label={page.chips.ariaLabel}
            >
              {chips.map((chip) => (
                <FilterChip
                  key={chip.key}
                  label={chip.label}
                  count={counts[chip.key]}
                  active={filter === chip.key}
                  onClick={() => selectFilter(chip.key)}
                />
              ))}
            </nav>
            <Segmented
              size="sm"
              ariaLabel={page.view.ariaLabel}
              value={view}
              onChange={(next) => setView(next === 'cards' ? 'cards' : 'table')}
              options={[
                { value: 'table', label: page.view.table },
                { value: 'cards', label: page.view.cards },
              ]}
            />
          </div>

          <p className="mt-4 text-[13px] text-ink-3">{page.health.successRateHint}</p>
          {health.isError ? (
            <p className="mt-2 text-[13px] text-ink-3">{sellerErrorMessage(health.error)}</p>
          ) : null}

          {filtered.length === 0 ? (
            <p className="mt-6 text-[13px] text-ink-3" role="status">
              {page.filterEmpty}
            </p>
          ) : view === 'table' ? (
            <div className="mt-4">
              <Table minWidth={980} size="compact">
                <TableHead>
                  <TableRow interactive={false}>
                    <TableHeaderCell className="w-10">
                      <input
                        type="checkbox"
                        aria-label={page.columns.selectAll}
                        checked={allPagedSelected}
                        onChange={(event) => toggleAllPaged(event.target.checked)}
                        className="h-4 w-4 accent-brand"
                      />
                    </TableHeaderCell>
                    <TableHeaderCell>{page.columns.listing}</TableHeaderCell>
                    <TableHeaderCell>{page.columns.successRate}</TableHeaderCell>
                    <TableHeaderCell>{page.columns.models}</TableHeaderCell>
                    <TableHeaderCell>{page.columns.multiplier}</TableHeaderCell>
                    <TableHeaderCell numeric>{page.columns.todayIncome}</TableHeaderCell>
                    <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                    <TableHeaderCell className="text-right">
                      {page.columns.actions}
                    </TableHeaderCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {paged.map((listing) => {
                    const listingHealth = healthById.get(listing.id)
                    const status = listingStatusView(listing.status)
                    const pausable = isListingPausable(listing.status)
                    const resumable = isListingSellerResumable(listing)
                    const canToggle = pausable || resumable
                    const pausingThis = pause.isPending && pause.variables === listing.id
                    const resumingThis = resume.isPending && resume.variables === listing.id
                    const reverifiable =
                      isListingReverifiable(listing) || listing.status === 'verifying'
                    return (
                      <TableRow key={listing.id}>
                        <TableCell>
                          <input
                            type="checkbox"
                            aria-label={`${page.columns.select} ${channelName(listing)} #${listing.id}`}
                            checked={selectedIds.has(listing.id)}
                            onChange={(event) =>
                              toggleSelected(listing.id, event.target.checked)
                            }
                            className="h-4 w-4 accent-brand"
                          />
                        </TableCell>
                        <TableCell>
                          <div className="flex min-w-0 flex-wrap items-center gap-2">
                            <Link
                              to="/console/listings/$id"
                              params={{ id: String(listing.id) }}
                              className="font-medium text-ink hover:text-brand-ink"
                            >
                              {channelName(listing)}
                            </Link>
                            <span className="tabular-nums text-[13px] text-ink-3">
                              #{listing.id}
                            </span>
                            <KindTag kind={kindOf(listing)} />
                          </div>
                        </TableCell>
                        <TableCell>
                          <SuccessRateCell health={listingHealth} />
                        </TableCell>
                        <TableCell>
                          <ModelsCell listing={listing} />
                        </TableCell>
                        <TableCell>
                          <MultiplierCell listing={listing} />
                        </TableCell>
                        <TableCell numeric>
                          <Price
                            value={listingHealth?.today_income_micro ?? 0}
                            unit={null}
                            size="sm"
                          />
                        </TableCell>
                        <TableCell>
                          <Tag tone={status.tone} dot>
                            {status.label}
                          </Tag>
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-2">
                            <Switch
                              checked={listing.status === 'live'}
                              loading={pausingThis || resumingThis}
                              disabled={!canToggle}
                              title={
                                canToggle
                                  ? undefined
                                  : listing.status === 'paused'
                                    ? pauseTitle(listing)
                                    : page.switch.lockedHint
                              }
                              aria-label={`${page.switch.ariaLabel} ${channelName(listing)} #${listing.id}`}
                              onCheckedChange={(next) =>
                                next ? resume.mutate(listing.id) : pause.mutate(listing.id)
                              }
                            />
                            <Button
                              variant="secondary"
                              size="sm"
                              onClick={() =>
                                void navigate({ search: { edit: listing.id } })
                              }
                            >
                              {page.actions.edit}
                            </Button>
                            <DropdownMenu
                              align="end"
                              menuLabel={page.menu.label}
                              trigger={
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  aria-label={page.menu.label}
                                  className="px-2"
                                >
                                  ⋯
                                </Button>
                              }
                              items={[
                                {
                                  key: 'reverify',
                                  label: page.menu.reverify,
                                  disabled: !reverifiable,
                                  onSelect: () => verify.mutate(listing.id),
                                },
                                {
                                  key: 'copy-config',
                                  label: page.menu.copyConfig,
                                  onSelect: () => void copyListingConfig(listing),
                                },
                                {
                                  key: 'delete',
                                  label: page.menu.delete,
                                  danger: true,
                                  onSelect: () => setDeleteTarget(listing),
                                },
                              ]}
                            />
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>

              {filtered.length > PAGE_SIZE ? (
                <div className="mt-4 border-t border-line pt-3">
                  <Pagination
                    page={currentPage}
                    pageSize={PAGE_SIZE}
                    total={filtered.length}
                    onPageChange={setPageNumber}
                    showSummary
                    ariaLabel={page.paginationAria}
                  />
                </div>
              ) : null}
            </div>
          ) : (
            <div className="mt-4 grid gap-4 xl:grid-cols-2">
              {filtered.map((listing) => (
                <ListingHealthCard
                  key={listing.id}
                  listing={listing}
                  health={healthById.get(listing.id)}
                  actions={cardActions(listing)}
                />
              ))}
            </div>
          )}
          <p className="mt-3 text-[13px] text-ink-3">{page.pausedNote}</p>
        </>
      )}

      {selected.length > 0 ? (
        <ListingBatchBar
          listings={selected}
          kindById={kindById}
          onClear={() => setSelectedIds(new Set())}
        />
      ) : null}

      {multiplierTarget ? (
        <MultiplierDialog
          listing={multiplierTarget}
          open
          onClose={() => setMultiplierTarget(null)}
        />
      ) : null}

      {keyTarget ? (
        <KeyDialog listing={keyTarget} open onClose={() => setKeyTarget(null)} />
      ) : null}

      {deleteTarget ? (
        <Dialog
          open
          onOpenChange={(next) => {
            if (!next) setDeleteTarget(null)
          }}
          title={page.deleteDialog.title}
          description={page.deleteDialog.lead(channelName(deleteTarget))}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={remove.isPending}
                onClick={() => setDeleteTarget(null)}
              >
                {page.deleteDialog.cancel}
              </Button>
              <Button
                variant="danger"
                loading={remove.isPending}
                onClick={() => remove.mutate(deleteTarget.id)}
              >
                {page.deleteDialog.confirm}
              </Button>
            </>
          }
        />
      ) : null}

      {edit ? (
        <ListingEditDrawer
          listingId={edit}
          onClose={() => void navigate({ search: { edit: undefined }, replace: true })}
        />
      ) : null}
    </>
  )
}
