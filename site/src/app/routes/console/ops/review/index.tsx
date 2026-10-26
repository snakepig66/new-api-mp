import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
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
  banAdminListing,
  unbanAdminListing,
  type AdminListing,
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

type ListingFilter = 'all' | 'canary' | 'live' | 'paused' | 'banned'

const filters: readonly FilterTabOption<ListingFilter>[] = [
  { value: 'all', label: page.tabs.all },
  { value: 'canary', label: page.tabs.canary },
  { value: 'live', label: page.tabs.live },
  { value: 'paused', label: page.tabs.paused },
  { value: 'banned', label: page.tabs.banned },
]

const BANNABLE = new Set(['live', 'paused', 'canary', 'verifying'])

function AdminListingsPage() {
  return (
    <RootOnly>
      <ListingsContent />
    </RootOnly>
  )
}

function ListingsContent() {
  const [filter, setFilter] = useState<ListingFilter>('all')
  const listings = useQuery(adminListingsQuery(filter === 'all' ? undefined : filter))
  const [pending, setPending] = useState<{ listing: AdminListing; action: 'ban' | 'unban' } | null>(
    null,
  )
  const [actionError, setActionError] = useState<string | null>(null)
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

  const activeLabel = filters.find((option) => option.value === filter)?.label ?? page.tabs.all

  function closeDialog() {
    if (mutation.isPending) return
    setPending(null)
    setActionError(null)
  }

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

        {listings.isPending ? (
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
    </>
  )
}
