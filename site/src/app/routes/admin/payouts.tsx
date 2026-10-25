import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { useAuth } from '@/lib/auth'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
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
  adminPayoutsQuery,
  approveAdminPayout,
  markAdminPayoutPaid,
  rejectAdminPayout,
  type AdminPayout,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  FilterTabs,
  Notice,
  PageSkeleton,
  RootOnly,
  type FilterTabOption,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { adminPayoutStatus } from '@/features/admin/status'

export const Route = createFileRoute('/admin/payouts')({
  component: AdminPayoutsPage,
})

const page = copy.pages.adminPayouts

type PayoutFilter = 'all' | 'pending' | 'approved' | 'paid' | 'rejected'

const filters: readonly FilterTabOption<PayoutFilter>[] = [
  { value: 'all', label: page.tabs.all },
  { value: 'pending', label: page.tabs.pending },
  { value: 'approved', label: page.tabs.approved },
  { value: 'paid', label: page.tabs.paid },
  { value: 'rejected', label: page.tabs.rejected },
]

type PayoutAction =
  | { type: 'approve'; id: number }
  | { type: 'mark-paid'; id: number }
  | { type: 'reject'; id: number; reason: string }

function AdminPayoutsPage() {
  return (
    <RootOnly>
      <PayoutsContent />
    </RootOnly>
  )
}

function PayoutsContent() {
  const { user } = useAuth()
  const [filter, setFilter] = useState<PayoutFilter>('all')
  const [confirming, setConfirming] = useState<{ payout: AdminPayout; action: 'approve' | 'mark-paid' } | null>(null)
  const [rejecting, setRejecting] = useState<AdminPayout | null>(null)
  const [reason, setReason] = useState('')
  const [reasonError, setReasonError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const toast = useToast()
  const queryClient = useQueryClient()
  const payouts = useQuery(adminPayoutsQuery(filter === 'all' ? undefined : filter))

  const mutation = useMutation({
    mutationFn: (action: PayoutAction) => {
      if (action.type === 'approve') return approveAdminPayout(action.id)
      if (action.type === 'mark-paid') return markAdminPayoutPaid(action.id)
      return rejectAdminPayout(action.id, action.reason)
    },
    onSuccess: (_data, action) => {
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'payouts'] })
      const title =
        action.type === 'approve'
          ? page.successApprove
          : action.type === 'mark-paid'
            ? page.successMarkPaid
            : page.successReject
      toast.show({ title, tone: 'success' })
      setConfirming(null)
      setRejecting(null)
      setReason('')
      setReasonError(null)
      setActionError(null)
    },
    onError: (error) => setActionError(adminErrorMessage(error)),
  })

  const activeLabel = filters.find((option) => option.value === filter)?.label ?? page.tabs.all
  const selfId = user?.id ?? 0

  function closeConfirm() {
    if (mutation.isPending) return
    setConfirming(null)
    setActionError(null)
  }

  function closeReject() {
    if (mutation.isPending) return
    setRejecting(null)
    setReason('')
    setReasonError(null)
    setActionError(null)
  }

  function submitReject() {
    if (!rejecting) return
    const value = reason.trim()
    if (!value) {
      setReasonError(page.reasonRequired)
      return
    }
    setReasonError(null)
    mutation.mutate({ type: 'reject', id: rejecting.id, reason: value })
  }

  const confirmTitle =
    confirming?.action === 'mark-paid' ? page.markPaidTitle : page.approveTitle
  const confirmLead = confirming?.action === 'mark-paid' ? page.markPaidLead : page.approveLead
  const confirmLabel =
    confirming?.action === 'mark-paid' ? page.markPaid : page.approve

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

        {payouts.isPending ? (
          <PageSkeleton rows={6} />
        ) : payouts.isError ? (
          <ErrorBlock error={payouts.error} onRetry={() => void payouts.refetch()} />
        ) : payouts.data.length === 0 ? (
          <Empty
            title={page.emptyTitle(activeLabel)}
            reason={page.emptyReason}
            next={page.emptyNext}
          />
        ) : (
          <div className="rounded-md border border-line bg-bg p-6">
          <Table minWidth={860}>
            <TableHead>
              <TableRow>
                <TableHeaderCell>{page.columns.id}</TableHeaderCell>
                <TableHeaderCell>{page.columns.seller}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.amount}</TableHeaderCell>
                <TableHeaderCell>{page.columns.createdAt}</TableHeaderCell>
                <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                <TableHeaderCell>{page.columns.actions}</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {payouts.data.map((payout) => {
                const view = adminPayoutStatus(payout.status)
                const isSelf = selfId > 0 && payout.seller_id === selfId
                const actionable =
                  payout.status === 'pending' || payout.status === 'approved'
                return (
                  <TableRow key={payout.id}>
                    <TableCell className="tabular-nums text-[13px] text-ink-3">
                      #{payout.id}
                    </TableCell>
                    <TableCell>
                      <SellerId id={payout.seller_id} />
                    </TableCell>
                    <TableCell numeric className="font-medium">
                      {formatYuan(payout.amount_micro)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                      {formatDateTime(payout.created_at)}
                    </TableCell>
                    <TableCell>
                      <Tag tone={view.tone} dot>
                        {view.label}
                      </Tag>
                    </TableCell>
                    <TableCell>
                      {isSelf && actionable ? (
                        <span className="text-[13px] text-ink-3" title={page.selfHint}>
                          {page.selfHint}
                        </span>
                      ) : payout.status === 'pending' ? (
                        <div className="flex flex-wrap items-center gap-2">
                          <Button
                            variant="primary"
                            size="sm"
                            onClick={() => {
                              setActionError(null)
                              setConfirming({ payout, action: 'approve' })
                            }}
                          >
                            {page.approve}
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            onClick={() => {
                              setActionError(null)
                              setReason('')
                              setReasonError(null)
                              setRejecting(payout)
                            }}
                          >
                            {page.reject}
                          </Button>
                        </div>
                      ) : payout.status === 'approved' ? (
                        <Button
                          variant="primary"
                          size="sm"
                          onClick={() => {
                            setActionError(null)
                            setConfirming({ payout, action: 'mark-paid' })
                          }}
                        >
                          {page.markPaid}
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
        open={confirming !== null}
        title={confirmTitle}
        description={confirmLead}
        confirmLabel={confirmLabel}
        tone="primary"
        loading={mutation.isPending}
        onClose={closeConfirm}
        onConfirm={() => {
          if (!confirming) return
          mutation.mutate({ type: confirming.action, id: confirming.payout.id })
        }}
      >
        {confirming ? (
          <div className="space-y-3">
            <p className="text-[13px] text-ink">
              <span className="tabular-nums">#{confirming.payout.id}</span> ·{' '}
              <SellerId id={confirming.payout.seller_id} /> ·{' '}
              <span className="tabular-nums">{formatYuan(confirming.payout.amount_micro)}</span>
            </p>
            {actionError ? (
              <Notice tone="warning">
                <span role="alert">{actionError}</span>
              </Notice>
            ) : null}
          </div>
        ) : null}
      </ConfirmDialog>

      <Dialog
        open={rejecting !== null}
        onOpenChange={(next) => {
          if (!next) closeReject()
        }}
        title={page.rejectTitle}
        description={page.rejectLead}
        footer={
          <>
            <Button variant="secondary" onClick={closeReject} disabled={mutation.isPending}>
              {copy.pages.adminCommon.cancel}
            </Button>
            <Button variant="danger" loading={mutation.isPending} onClick={submitReject}>
              {page.reject}
            </Button>
          </>
        }
      >
        {rejecting ? (
          <div className="space-y-4">
            <p className="text-[13px] text-ink">
              <span className="tabular-nums">#{rejecting.id}</span> ·{' '}
              <SellerId id={rejecting.seller_id} /> ·{' '}
              <span className="tabular-nums">{formatYuan(rejecting.amount_micro)}</span>
            </p>
            <Field
              label={page.reason}
              htmlFor="payout-reject-reason"
              error={reasonError}
            >
              <textarea
                id="payout-reject-reason"
                rows={4}
                value={reason}
                placeholder={page.reasonPlaceholder}
                onChange={(event) => {
                  setReason(event.target.value)
                  setReasonError(null)
                  setActionError(null)
                }}
                className="w-full resize-y rounded-md border border-transparent bg-bg-soft px-4 py-3 text-[15px] leading-6 text-ink transition-colors placeholder:text-ink-3 focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft"
              />
            </Field>
            {actionError ? (
              <Notice tone="warning">
                <span role="alert">{actionError}</span>
              </Notice>
            ) : null}
          </div>
        ) : null}
      </Dialog>
    </>
  )
}
