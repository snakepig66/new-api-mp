import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Empty,
  Multiplier,
  PageHeader,
  SellerId,
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
  adminListingsQuery,
  approveListing,
  banAdminListing,
  pendingListingsQuery,
  rejectListing,
  unbanAdminListing,
  type AdminListing,
  type AdminPendingHostGroup,
  type AdminPendingListing,
  type ListingRejectAction,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  FilterTabs,
  Notice,
  PageSkeleton,
  RootOnly,
  type FilterTabOption,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { adminListingStatus } from '@/features/admin/status'

export const Route = createFileRoute('/console/ops/review/')({
  component: AdminListingsPage,
})

const page = copy.pages.adminListings

type ListingFilter = 'all' | 'pending' | 'canary' | 'live' | 'paused' | 'banned'

type PendingReviewAction = 'approve' | ListingRejectAction

const filters: readonly FilterTabOption<ListingFilter>[] = [
  { value: 'all', label: page.tabs.all },
  { value: 'pending', label: page.tabs.pending },
  { value: 'canary', label: page.tabs.canary },
  { value: 'live', label: page.tabs.live },
  { value: 'paused', label: page.tabs.paused },
  { value: 'banned', label: page.tabs.banned },
]

const BANNABLE = new Set(['live', 'paused', 'canary', 'verifying'])

/** Collapse key for the bucket without a seller-defined host. */
const PLATFORM_HOST_KEY = '__platform__'

function AdminListingsPage() {
  return (
    <RootOnly>
      <ListingsContent />
    </RootOnly>
  )
}

function RealNameTag({ status }: { status: string }) {
  return status === 'verified' ? (
    <Tag tone="brand" dot>
      {page.pending.realNameVerified}
    </Tag>
  ) : (
    <Tag dot>{page.pending.realNameUnverified}</Tag>
  )
}

function PendingHostGroups({
  groups,
  collapsed,
  onToggle,
  onAction,
}: {
  groups: AdminPendingHostGroup[]
  collapsed: ReadonlySet<string>
  onToggle: (key: string) => void
  onAction: (listing: AdminPendingListing, action: PendingReviewAction) => void
}) {
  return (
    <div className="space-y-3">
      {groups.map((group) => {
        const key = group.host || PLATFORM_HOST_KEY
        const open = !collapsed.has(key)
        return (
          <section key={key} className="rounded-md border border-line bg-bg">
            <button
              type="button"
              aria-expanded={open}
              onClick={() => onToggle(key)}
              className="flex w-full items-center justify-between gap-4 px-5 py-4 text-left transition-colors hover:bg-bg-soft"
            >
              <span className="min-w-0 truncate text-[15px] font-medium text-ink">
                {group.host || page.pending.platformHost}
              </span>
              <span className="flex shrink-0 items-center gap-3">
                <Tag>{page.pending.groupCount(group.count)}</Tag>
                <span aria-hidden="true" className="text-[18px] leading-none text-ink-3">
                  {open ? '−' : '+'}
                </span>
              </span>
            </button>
            {open ? (
              <div className="border-t border-line px-5 pb-2 pt-1">
                <Table minWidth={980} size="compact">
                  <TableHead>
                    <TableRow interactive={false}>
                      <TableHeaderCell>{page.pending.columns.id}</TableHeaderCell>
                      <TableHeaderCell>{page.pending.columns.seller}</TableHeaderCell>
                      <TableHeaderCell>{page.pending.columns.models}</TableHeaderCell>
                      <TableHeaderCell>{page.pending.columns.createdAt}</TableHeaderCell>
                      <TableHeaderCell>{page.pending.columns.realName}</TableHeaderCell>
                      <TableHeaderCell className="text-right">
                        {page.pending.columns.actions}
                      </TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {group.listings.map((listing) => (
                      <TableRow key={listing.id}>
                        <TableCell className="tabular-nums text-[13px] text-ink-3">
                          #{listing.id}
                        </TableCell>
                        <TableCell>
                          <SellerId id={listing.seller_id} />
                        </TableCell>
                        <TableCell>
                          <div className="flex max-w-[240px] flex-wrap gap-1">
                            {listing.models.slice(0, 3).map((model) => (
                              <Tag key={model}>{model}</Tag>
                            ))}
                            {listing.models.length > 3 ? (
                              <Tag>+{listing.models.length - 3}</Tag>
                            ) : null}
                          </div>
                        </TableCell>
                        <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                          {formatDateTime(listing.created_at)}
                        </TableCell>
                        <TableCell>
                          <RealNameTag status={listing.seller_real_name_status} />
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex flex-wrap justify-end gap-2">
                            <Button
                              variant="primary"
                              size="sm"
                              onClick={() => onAction(listing, 'approve')}
                            >
                              {page.pending.approve}
                            </Button>
                            <Button
                              variant="danger"
                              size="sm"
                              onClick={() => onAction(listing, 'draft')}
                            >
                              {page.pending.reject}
                            </Button>
                            <Button
                              variant="danger"
                              size="sm"
                              onClick={() => onAction(listing, 'ban')}
                            >
                              {page.pending.rejectBan}
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : null}
          </section>
        )
      })}
    </div>
  )
}

export function ListingsContent() {
  const [filter, setFilter] = useState<ListingFilter>('all')
  // The grouped queue has its own endpoint and shape, so only one of the two
  // queries is enabled at a time.
  const listings = useQuery({
    ...adminListingsQuery(
      filter === 'all' || filter === 'pending' ? undefined : filter,
    ),
    enabled: filter !== 'pending',
  })
  const pendingGroups = useQuery({
    ...pendingListingsQuery(),
    enabled: filter === 'pending',
  })
  const [pending, setPending] = useState<{ listing: AdminListing; action: 'ban' | 'unban' } | null>(
    null,
  )
  const [reviewTarget, setReviewTarget] = useState<{
    listing: AdminPendingListing
    action: PendingReviewAction
  } | null>(null)
  const [collapsedHosts, setCollapsedHosts] = useState<ReadonlySet<string>>(new Set())
  const [actionError, setActionError] = useState<string | null>(null)
  const [reviewError, setReviewError] = useState<string | null>(null)
  const toast = useToast()
  const queryClient = useQueryClient()

  const mutation = useMutation({
    mutationFn: ({ id, action }: { id: number; action: 'ban' | 'unban' }) =>
      action === 'ban' ? banAdminListing(id) : unbanAdminListing(id),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'listings'] })
      toast.show({
        title: variables.action === 'ban' ? page.successBan : page.successUnban,
        tone: 'success',
      })
      setPending(null)
      setActionError(null)
    },
    onError: (error) => setActionError(adminErrorMessage(error)),
  })

  const reviewMutation = useMutation({
    mutationFn: ({ listing, action }: { listing: AdminPendingListing; action: PendingReviewAction }) => {
      if (action === 'approve') return approveListing(listing.id)
      return rejectListing(listing.id, action)
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'listings'] })
      const title =
        variables.action === 'approve'
          ? page.pending.successApprove
          : variables.action === 'ban'
            ? page.pending.successBan
            : page.pending.successReject
      toast.show({ title, tone: 'success' })
      setReviewTarget(null)
      setReviewError(null)
    },
    onError: (error) => setReviewError(adminErrorMessage(error)),
  })

  const activeLabel = filters.find((option) => option.value === filter)?.label ?? page.tabs.all

  function closeDialog() {
    if (mutation.isPending) return
    setPending(null)
    setActionError(null)
  }

  function toggleHost(key: string) {
    setCollapsedHosts((current) => {
      const next = new Set(current)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  function openReview(listing: AdminPendingListing, action: PendingReviewAction) {
    setReviewError(null)
    setReviewTarget({ listing, action })
  }

  function closeReview() {
    if (reviewMutation.isPending) return
    setReviewTarget(null)
    setReviewError(null)
  }

  const reviewTitle =
    reviewTarget?.action === 'approve'
      ? page.pending.approveTitle
      : reviewTarget?.action === 'ban'
        ? page.pending.banTitle
        : page.pending.rejectTitle
  const reviewLead =
    reviewTarget?.action === 'approve'
      ? page.pending.approveLead
      : reviewTarget?.action === 'ban'
        ? page.pending.banLead
        : page.pending.rejectLead
  const reviewLabel =
    reviewTarget?.action === 'approve'
      ? page.pending.approve
      : reviewTarget?.action === 'ban'
        ? page.pending.rejectBan
        : page.pending.reject

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <div className="mt-6">
        <FilterTabs
          options={filters}
          value={filter}
          onChange={setFilter}
          ariaLabel={page.tabsAria}
        />

        {filter === 'pending' ? (
          pendingGroups.isPending ? (
            <PageSkeleton rows={6} />
          ) : pendingGroups.isError ? (
            <ErrorBlock
              error={pendingGroups.error}
              onRetry={() => void pendingGroups.refetch()}
            />
          ) : pendingGroups.data.length === 0 ? (
            <Empty
              title={page.pending.emptyTitle}
              reason={page.pending.emptyReason}
              next={page.pending.emptyNext}
            />
          ) : (
            <PendingHostGroups
              groups={pendingGroups.data}
              collapsed={collapsedHosts}
              onToggle={toggleHost}
              onAction={openReview}
            />
          )
        ) : listings.isPending ? (
          <PageSkeleton rows={6} />
        ) : listings.isError ? (
          <ErrorBlock error={listings.error} onRetry={() => void listings.refetch()} />
        ) : listings.data.length === 0 ? (
          <Empty
            title={page.emptyTitle(activeLabel)}
            reason={page.emptyReason}
            next={page.emptyNext}
          />
        ) : (
          <div className="rounded-md border border-line bg-bg p-6">
          <Table minWidth={1000}>
            <TableHead>
              <TableRow>
                <TableHeaderCell>{page.columns.id}</TableHeaderCell>
                <TableHeaderCell>{page.columns.seller}</TableHeaderCell>
                <TableHeaderCell>{page.columns.vendor}</TableHeaderCell>
                <TableHeaderCell>{page.columns.models}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.multiplier}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.concurrency}</TableHeaderCell>
                <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                <TableHeaderCell>{page.columns.fingerprint}</TableHeaderCell>
                <TableHeaderCell>{page.columns.actions}</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {listings.data.map((listing) => {
                const view = adminListingStatus(listing.status)
                const canBan = BANNABLE.has(listing.status)
                return (
                  <TableRow key={listing.id}>
                    <TableCell className="tabular-nums text-[13px] text-ink-3">
                      #{listing.id}
                    </TableCell>
                    <TableCell>
                      <SellerId id={listing.seller_id} />
                    </TableCell>
                    <TableCell className=" text-[13px]">{listing.vendor}</TableCell>
                    <TableCell>
                      <div className="flex max-w-[240px] flex-wrap gap-1">
                        {listing.models.slice(0, 2).map((model) => (
                          <Tag key={model}>{model}</Tag>
                        ))}
                        {listing.models.length > 2 ? (
                          <Tag>+{listing.models.length - 2}</Tag>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell numeric>
                      <Multiplier milli={listing.multiplier_milli} showSaving={false} />
                    </TableCell>
                    <TableCell numeric className="text-ink-3">
                      {listing.concurrency_limit}
                    </TableCell>
                    <TableCell>
                      <Tag tone={view.tone} dot>
                        {view.label}
                      </Tag>
                    </TableCell>
                    <TableCell>
                      <span className="tabular-nums text-[13px] text-ink-3">
                        {listing.key_fingerprint8 || copy.pages.adminCommon.none}
                      </span>
                    </TableCell>
                    <TableCell>
                      {canBan ? (
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => {
                            setActionError(null)
                            setPending({ listing, action: 'ban' })
                          }}
                        >
                          {page.ban}
                        </Button>
                      ) : listing.status === 'banned' ? (
                        <Button
                          variant="primary"
                          size="sm"
                          onClick={() => {
                            setActionError(null)
                            setPending({ listing, action: 'unban' })
                          }}
                        >
                          {page.unban}
                        </Button>
                      ) : (
                        <span className="text-[13px] text-ink-3">{copy.pages.adminCommon.none}</span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={pending !== null}
        title={pending?.action === 'unban' ? page.unbanTitle : page.banTitle}
        description={pending?.action === 'unban' ? page.unbanLead : page.banLead}
        confirmLabel={pending?.action === 'unban' ? page.unban : page.ban}
        tone={pending?.action === 'unban' ? 'primary' : 'danger'}
        loading={mutation.isPending}
        onClose={closeDialog}
        onConfirm={() => {
          if (!pending) return
          mutation.mutate({ id: pending.listing.id, action: pending.action })
        }}
      >
        {pending ? (
          <div className="space-y-3">
            <p className="tabular-nums text-[13px] text-ink">
              {page.target} #{pending.listing.id} · {pending.listing.vendor} ·{' '}
              <SellerId id={pending.listing.seller_id} />
            </p>
            {actionError ? (
              <Notice tone="warning">
                <span role="alert">{actionError}</span>
              </Notice>
            ) : null}
          </div>
        ) : null}
      </ConfirmDialog>

      <ConfirmDialog
        open={reviewTarget !== null}
        title={reviewTitle}
        description={reviewLead}
        confirmLabel={reviewLabel}
        tone={reviewTarget?.action === 'approve' ? 'primary' : 'danger'}
        loading={reviewMutation.isPending}
        onClose={closeReview}
        onConfirm={() => {
          if (!reviewTarget) return
          reviewMutation.mutate(reviewTarget)
        }}
      >
        {reviewTarget ? (
          <div className="space-y-3">
            <p className="tabular-nums text-[13px] text-ink">
              {page.pending.target} #{reviewTarget.listing.id} · {reviewTarget.listing.vendor} ·{' '}
              <SellerId id={reviewTarget.listing.seller_id} />
            </p>
            {reviewError ? (
              <Notice tone="warning">
                <span role="alert">{reviewError}</span>
              </Notice>
            ) : null}
          </div>
        ) : null}
      </ConfirmDialog>
    </>
  )
}
