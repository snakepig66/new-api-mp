import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatYuan } from '@/lib/money'
import { formatCountdown } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  Input,
  Multiplier,
  PageHeader,
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
  pauseListing,
  replaceListingKey,
  resumeListing,
  sellerListingDetailQuery,
  sellerListingsQuery,
  updateListingMultiplier,
  vendorName,
  type SellerListing,
} from '@/features/seller/api'
import { AsyncError, Field, notify, PageSkeleton, Skeleton } from '@/features/seller/components'
import {
  markMultiplierChanged,
  readCooldownUntil,
  remainingSeconds,
} from '@/features/seller/cooldown'
import { sellerErrorMessage } from '@/features/seller/errors'
import {
  isListingKeyReplaceable,
  isListingPausable,
  isListingResumable,
  listingStatusView,
  pauseReasonView,
} from '@/features/seller/status'

export const Route = createFileRoute('/seller/listings/')({
  component: SellerListingsPage,
})

const page = copy.pages.sellerListings
const common = copy.pages.sellerCommon

function isZeroMicro(value: number | string): boolean {
  return String(value) === '0'
}

function limitsText(listing: SellerListing): string {
  const rpm = listing.rpm_limit > 0 ? String(listing.rpm_limit) : common.unlimited
  const cap = isZeroMicro(listing.daily_cap_micro)
    ? common.unlimited
    : formatYuan(listing.daily_cap_micro)
  return `${listing.concurrency_limit} / ${rpm} / ${cap}`
}

function PauseReason({ id }: { id: number }) {
  const { data, isPending, isError } = useQuery(sellerListingDetailQuery(id))
  if (isPending) return <Skeleton className="h-3 w-20" />
  if (isError) return <span className="text-muted">—</span>
  return <span className="text-[12px] text-warning">{pauseReasonView(data.pause_reason)}</span>
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
          <span className="text-[12px] text-muted">{vendorName(listing.vendor)}</span>
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
              className="h-1.5 w-full cursor-pointer accent-[var(--color-brand-dark)] disabled:cursor-not-allowed"
            />
            <Input
              aria-label={page.multiplierDialog.label}
              className="w-24 shrink-0 text-right"
              mono
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
          <p className="text-[12px] text-warning" role="status">
            {page.multiplierDialog.cooldown(formatCountdown(cooldown.remaining))}
          </p>
        ) : null}

        {error ? (
          <p className="text-[12px] text-danger" role="alert">
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
          <Input
            id="replacement-key"
            type="password"
            mono
            autoComplete="new-password"
            spellCheck={false}
            placeholder={page.keyDialog.placeholder}
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>

        <div className="border border-line bg-wash px-3 py-2">
          <div className="text-[11px] text-muted">{common.keyPreviewHint}</div>
          <div className="mt-1 font-numeric text-[12.5px] text-ink">{preview}</div>
        </div>
      </div>
    </Dialog>
  )
}

function SellerListingsPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const listings = useQuery(sellerListingsQuery())
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
              to="/seller/listings/new"
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
              to="/seller/listings/new"
              className={buttonClasses('primary', 'md')}
            >
              {page.actions.newListing}
            </Link>
          }
        />
      ) : (
        <>
          <p className="mt-3 text-[11.5px] text-muted">{page.pausedNote}</p>
          <div className="mt-4">
            <Table minWidth={1040}>
              <TableHead>
                <TableRow>
                  <TableHeaderCell>{page.columns.listing}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.models}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.multiplier}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.limits}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.pauseReason}</TableHeaderCell>
                  <TableHeaderCell className="text-right">
                    {page.columns.actions}
                  </TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {rows.map((listing) => {
                  const view = listingStatusView(listing.status)
                  const pausingThis = pause.isPending && pause.variables === listing.id
                  const resumingThis = resume.isPending && resume.variables === listing.id
                  return (
                    <TableRow key={listing.id}>
                      <TableCell>
                        <div className="font-medium text-ink">{vendorName(listing.vendor)}</div>
                        <div className="font-numeric text-[11px] text-muted">
                          #{listing.id} · {common.keyFingerprint}{' '}
                          {listing.key_fingerprint8 || '—'}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex max-w-[220px] flex-wrap gap-1">
                          {listing.models.map((model) => (
                            <Tag key={model}>{model}</Tag>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Tag tone={view.tone} dot>
                          {view.label}
                        </Tag>
                      </TableCell>
                      <TableCell numeric>
                        <Multiplier milli={listing.multiplier_milli} />
                      </TableCell>
                      <TableCell numeric className="whitespace-nowrap text-muted">
                        {limitsText(listing)}
                      </TableCell>
                      <TableCell>
                        {listing.status === 'paused' ? (
                          <PauseReason id={listing.id} />
                        ) : (
                          <span className="text-muted">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-1.5">
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
                          {isListingResumable(listing.status) ? (
                            <Button
                              variant="primary"
                              size="sm"
                              loading={resumingThis}
                              onClick={() => resume.mutate(listing.id)}
                            >
                              {page.actions.resume}
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
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
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
