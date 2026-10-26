import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Empty,
  Multiplier,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  useToast,
} from '@/ui'

import {
  sellerListingDetailQuery,
  verifyListing,
  vendorName,
  type SellerListingDetail,
} from '@/features/seller/api'
import { AsyncError, notify, PageSkeleton } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import {
  isListingReverifiable,
  listingStatusView,
  pauseReasonView,
} from '@/features/seller/status'
import { VerificationSummary } from '@/features/seller/verification'

export const Route = createFileRoute('/console/listings/$id')({
  component: SellerListingDetailPage,
})

const page = copy.pages.sellerListingDetail
const common = copy.pages.sellerCommon
const verificationCopy = copy.pages.sellerVerification

function isZeroMicro(value: number | string): boolean {
  return String(value) === '0'
}

function limitsText(listing: SellerListingDetail): string {
  const rpm = listing.rpm_limit > 0 ? String(listing.rpm_limit) : common.unlimited
  const cap = isZeroMicro(listing.daily_cap_micro)
    ? common.unlimited
    : formatYuan(listing.daily_cap_micro)
  return `${listing.concurrency_limit} / ${rpm} / ${cap}`
}

function hoursText(hours: string): string {
  if (!hours.trim()) return page.fields.allDay
  try {
    const windows = JSON.parse(hours) as { start?: string; end?: string }[]
    if (!Array.isArray(windows) || windows.length === 0) return page.fields.allDay
    return windows
      .filter((window) => window.start && window.end)
      .map((window) => `${window.start}–${window.end}`)
      .join('、')
  } catch {
    return hours
  }
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="rounded-md bg-bg-soft px-4 py-3">
      <dt className="text-[13px] text-ink-3">{label}</dt>
      <dd className="mt-1 text-[14px] text-ink">{children}</dd>
    </div>
  )
}

function SellerListingDetailPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const params = Route.useParams()
  const id = Number(params.id)
  const validId = Number.isInteger(id) && id > 0
  const [recheckError, setRecheckError] = useState<string | null>(null)
  const detail = useQuery({
    ...sellerListingDetailQuery(id),
    enabled: validId,
  })

  const verify = useMutation({
    mutationFn: () => verifyListing(id),
    onSuccess: () => {
      setRecheckError(null)
      // Optimistically mark the detail as verifying so its refetchInterval
      // keeps polling even if an immediate refetch still sees draft.
      queryClient.setQueryData<SellerListingDetail>(
        sellerListingDetailQuery(id).queryKey,
        (row) => (row ? { ...row, status: 'verifying' } : row),
      )
      notify(toast, { title: common.toast.verifyStarted, tone: 'success' })
    },
    onError: (error) => setRecheckError(sellerErrorMessage(error)),
  })

  if (!validId) {
    return (
      <AsyncError
        title={page.loadError}
        reason={common.errors.listingNotFound}
        retryLabel={copy.actions.retry}
      />
    )
  }

  if (detail.isPending) return <PageSkeleton />

  if (detail.isError) {
    return (
      <AsyncError
        title={page.loadError}
        reason={sellerErrorMessage(detail.error)}
        onRetry={() => void detail.refetch()}
        retryLabel={copy.actions.retry}
      />
    )
  }

  const listing = detail.data
  const status = listingStatusView(listing.status)
  const verifying = listing.status === 'verifying'
  const canRecheck = isListingReverifiable(listing)

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <>
            <Link
              to="/console/listings"
              className="inline-flex h-9 items-center rounded-md border border-line bg-bg px-3.5 text-[13px] font-medium text-ink transition-colors hover:bg-bg-soft"
            >
              {page.back}
            </Link>
            <Button variant="secondary" size="sm" onClick={() => void detail.refetch()}>
              {page.refresh}
            </Button>
            {canRecheck || verifying ? (
              <Button
                size="sm"
                loading={verify.isPending}
                disabled={verifying}
                onClick={() => verify.mutate()}
              >
                {verify.isPending
                  ? page.rechecking
                  : verifying
                    ? common.status.verifying
                    : page.recheck}
              </Button>
            ) : null}
          </>
        }
      />

      <section className="mt-6 rounded-md border border-line bg-bg p-6">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
          <div>
            <h2 className="text-[16px] font-semibold tracking-tight text-ink">
              {vendorName(listing.vendor)} · #{listing.id}
            </h2>
            <p className="mt-1 text-[13px] text-ink-3">
              {common.keyFingerprint} {listing.key_fingerprint8 || '—'}
            </p>
          </div>
          <Tag tone={status.tone} dot>
            {status.label}
          </Tag>
        </div>

        <dl className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <Field label={page.fields.vendor}>{vendorName(listing.vendor)}</Field>
          <Field label={page.fields.multiplier}>
            <Multiplier milli={listing.multiplier_milli} />
          </Field>
          <Field label={page.fields.limits}>{limitsText(listing)}</Field>
          <Field label={page.fields.hours}>{hoursText(listing.hours)}</Field>
          <Field label={page.fields.created}>{formatDateTime(listing.created_at)}</Field>
          <Field label={page.fields.fingerprint}>{listing.key_fingerprint8 || '—'}</Field>
        </dl>

        <div className="mt-4 flex flex-wrap gap-1.5">
          {listing.models.map((model) => (
            <Tag key={model}>{model}</Tag>
          ))}
        </div>
      </section>

      <section className="mt-6 rounded-md border border-line bg-bg p-6">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">
          {verificationCopy.title}
        </h2>
        <p className="mt-1 text-[13px] leading-5 text-ink-3">{page.verificationHint}</p>
        <div className="mt-4">
          {verifying ? (
            <p className="text-[13px] text-ink-3">{verificationCopy.pending}</p>
          ) : listing.verification ? (
            <VerificationSummary verification={listing.verification} />
          ) : (
            <p className="text-[13px] text-ink-3">{page.verificationEmpty}</p>
          )}
        </div>
        {recheckError ? (
          <p className="mt-3 text-[13px] text-danger" role="alert">
            {recheckError}
          </p>
        ) : null}
      </section>

      <section className="mt-6 rounded-md border border-line bg-bg p-6">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">
          {page.pauseTitle}
        </h2>
        {listing.status === 'paused' ? (
          <dl className="mt-4 grid gap-3 sm:grid-cols-2">
            <Field label={page.pauseReason}>
              <span className="text-warn">
                {pauseReasonView(listing.pause_reason || 'manual')}
              </span>
            </Field>
            <Field label={page.resumeAt}>
              {listing.resume_at ? formatDateTime(listing.resume_at) : '—'}
            </Field>
          </dl>
        ) : (
          <p className="mt-3 text-[13px] text-ink-3">{page.notPaused}</p>
        )}
      </section>

      <section className="mt-6 rounded-md border border-line bg-bg p-6">
        <h2 className="text-[16px] font-semibold tracking-tight text-ink">
          {page.historyTitle}
        </h2>
        <div className="mt-4">
          {listing.probes.length === 0 ? (
            <Empty title={page.historyEmpty} reason={page.verificationEmpty} />
          ) : (
            <Table minWidth={640}>
              <TableHead>
                <TableRow>
                  <TableHeaderCell>{page.historyColumns.time}</TableHeaderCell>
                  <TableHeaderCell>{page.historyColumns.type}</TableHeaderCell>
                  <TableHeaderCell>{page.historyColumns.result}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.historyColumns.latency}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {listing.probes.map((probe) => (
                  <TableRow key={probe.id}>
                    <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                      {formatDateTime(probe.created_at)}
                    </TableCell>
                    <TableCell className="text-[13px] text-ink-3">{probe.type}</TableCell>
                    <TableCell>
                      <Tag tone={probe.ok ? 'brand' : 'danger'} dot>
                        {probe.ok ? page.passed : page.failed}
                      </Tag>
                    </TableCell>
                    <TableCell numeric className="tabular-nums text-ink-3">
                      {probe.latency_ms} ms
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      </section>
    </>
  )
}
