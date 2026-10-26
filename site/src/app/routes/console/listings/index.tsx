import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatCountdown } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  Input,
  Multiplier,
  PageHeader,
  PasswordInput,
  buttonClasses,
  useToast,
} from '@/ui'

import {
  pauseListing,
  replaceListingKey,
  resumeListing,
  sellerListingsHealthQuery,
  sellerListingsQuery,
  updateListingMultiplier,
  verifyListing,
  vendorName,
  type SellerListing,
} from '@/features/seller/api'
import { AsyncError, Field, notify, PageSkeleton } from '@/features/seller/components'
import {
  markMultiplierChanged,
  readCooldownUntil,
  remainingSeconds,
} from '@/features/seller/cooldown'
import { sellerErrorMessage } from '@/features/seller/errors'
import { ListingHealthCard } from '@/features/seller/ListingHealthCard'
import {
  isListingKeyReplaceable,
  isListingPausable,
  isListingReverifiable,
  isListingSellerResumable,
} from '@/features/seller/status'

export const Route = createFileRoute('/console/listings/')({
  component: SellerListingsPage,
})

const page = copy.pages.sellerListings
const common = copy.pages.sellerCommon

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

        <div className="rounded-md bg-bg-soft px-4 py-3">
          <div className="text-[13px] text-ink-3">{common.keyPreviewHint}</div>
          <div className="mt-1 tabular-nums text-[14px] text-ink">{preview}</div>
        </div>
      </div>
    </Dialog>
  )
}

export function SellerListingsPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const listings = useQuery(sellerListingsQuery())
  const health = useQuery(sellerListingsHealthQuery())
  const [multiplierTarget, setMultiplierTarget] = useState<SellerListing | null>(null)
  const [keyTarget, setKeyTarget] = useState<SellerListing | null>(null)

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
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
          <p className="mt-4 text-[13px] text-ink-3">{page.health.successRateHint}</p>
          {health.isError ? (
            <p className="mt-2 text-[13px] text-ink-3">{sellerErrorMessage(health.error)}</p>
          ) : null}
          <div className="mt-4 grid gap-4 xl:grid-cols-2">
            {rows.map((listing) => {
              const pausingThis = pause.isPending && pause.variables === listing.id
              const resumingThis = resume.isPending && resume.variables === listing.id
              const verifyingThis = verify.isPending && verify.variables === listing.id
              const verifying = listing.status === 'verifying'
              return (
                <ListingHealthCard
                  key={listing.id}
                  listing={listing}
                  health={healthById.get(listing.id)}
                  actions={
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
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setKeyTarget(listing)}
                        >
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
                  }
                />
              )
            })}
          </div>
          <p className="mt-3 text-[13px] text-ink-3">{page.pausedNote}</p>
        </>
      )}

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
    </>
  )
}
