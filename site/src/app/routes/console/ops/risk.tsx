/**
 * 运营后台 · 风控（P5-C）。
 *
 * 简化版关联图谱：同一公开登录 IP 或同一收款账户的账户互相关联。下方列出
 * 当前进程内的活跃预占，并允许运营强制释放某个挂单的冻结金额与槽位。
 *
 * 强制释放是卡死请求的人工逃生口，不是常规操作：正在执行的请求不会被打断，
 * 释放后同一挂单可能同时接收新流量（超卖）。页面在确认弹窗里明确提示。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Empty,
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
  activeSessionsQuery,
  forceRelease,
  relatedUsersQuery,
  type AdminActiveSession,
  type AdminRelatedUser,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  Notice,
  RootOnly,
  Section,
  TableSkeleton,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

export const Route = createFileRoute('/console/ops/risk')({
  beforeLoad: () => {
    throw redirect({ to: '/console/ops/tools/risk', replace: true })
  },
  component: RiskPage,
})

const page = copy.pages.adminRisk

function parsePositiveInt(raw: string): number | null {
  const trimmed = raw.trim()
  if (!/^[1-9]\d*$/.test(trimmed)) return null
  const value = Number(trimmed)
  return Number.isSafeInteger(value) ? value : null
}

function relationLabel(relation: AdminRelatedUser): string {
  return (
    (page.related.relationTypes as Record<string, string>)[relation.relation_type] ??
    relation.relation_type
  )
}

function RelationTag({ relation }: { relation: AdminRelatedUser }) {
  const tone = relation.relation_type === 'same_payout_account' ? 'warning' : 'neutral'
  return <Tag tone={tone}>{relationLabel(relation)}</Tag>
}

export function RiskPage() {
  return (
    <RootOnly>
      <RiskContent />
    </RootOnly>
  )
}

export function RiskContent() {
  const toast = useToast()
  const queryClient = useQueryClient()

  const [relatedInput, setRelatedInput] = useState('')
  const [relatedUserID, setRelatedUserID] = useState(0)
  const [relatedError, setRelatedError] = useState<string | null>(null)

  const [filterInput, setFilterInput] = useState('')
  const [sessionsListingID, setSessionsListingID] = useState(0)
  const [filterError, setFilterError] = useState<string | null>(null)

  const [forceInput, setForceInput] = useState('')
  const [forceTarget, setForceTarget] = useState<number | null>(null)
  const [forceError, setForceError] = useState<string | null>(null)

  const related = useQuery(relatedUsersQuery(relatedUserID))
  const sessions = useQuery(activeSessionsQuery(sessionsListingID))
  const release = useMutation({
    mutationFn: (listingID: number) => forceRelease(listingID),
    onSuccess: (result) => {
      toast.show({
        title: page.force.success,
        description: page.force.result(result.sessions, result.slots_released),
        tone: 'success',
      })
      setForceTarget(null)
      setForceInput('')
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'sessions'] })
    },
    onError: (error) => {
      toast.show({ title: adminErrorMessage(error), tone: 'error' })
    },
  })

  function submitRelated() {
    if (relatedInput.trim() === '') {
      setRelatedError(page.related.required)
      return
    }
    const userID = parsePositiveInt(relatedInput)
    if (userID === null) {
      setRelatedError(page.related.invalid)
      return
    }
    setRelatedError(null)
    if (userID === relatedUserID) {
      void related.refetch()
      return
    }
    setRelatedUserID(userID)
  }

  function submitFilter() {
    if (filterInput.trim() === '') {
      setFilterError(null)
      if (sessionsListingID === 0) {
        void sessions.refetch()
        return
      }
      setSessionsListingID(0)
      return
    }
    const listingID = parsePositiveInt(filterInput)
    if (listingID === null) {
      setFilterError(page.sessions.filterInvalid)
      return
    }
    setFilterError(null)
    if (listingID === sessionsListingID) {
      void sessions.refetch()
      return
    }
    setSessionsListingID(listingID)
  }

  function submitForce() {
    if (forceInput.trim() === '') {
      setForceError(page.force.required)
      return
    }
    const listingID = parsePositiveInt(forceInput)
    if (listingID === null) {
      setForceError(page.force.invalid)
      return
    }
    setForceError(null)
    setForceTarget(listingID)
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <div className="mt-6 space-y-6">
        <Section title={page.related.title} hint={page.related.lead}>
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(event) => {
              event.preventDefault()
              submitRelated()
            }}
          >
            <Field
              label={page.related.label}
              htmlFor="risk-related-user-id"
              error={relatedError}
            >
              <TextInput
                id="risk-related-user-id"
                value={relatedInput}
                placeholder={page.related.placeholder}
                autoComplete="off"
                mono
                invalid={Boolean(relatedError)}
                onChange={(event) => {
                  setRelatedInput(event.target.value)
                  setRelatedError(null)
                }}
              />
            </Field>
            <Button type="submit" loading={relatedUserID > 0 && related.isFetching}>
              {relatedUserID > 0 && related.isFetching
                ? page.related.submitting
                : page.related.submit}
            </Button>
          </form>

          <div className="mt-6">
            {relatedUserID === 0 ? (
              <Empty
                title={page.related.empty.title}
                reason={page.related.empty.reason}
                next={page.related.empty.next}
              />
            ) : related.isPending ? (
              <TableSkeleton rows={3} />
            ) : related.isError ? (
              <ErrorBlock error={related.error} onRetry={() => void related.refetch()} />
            ) : related.data ? (
              <RelatedTable rows={related.data.items} />
            ) : null}
          </div>
        </Section>

        <Section
          title={page.sessions.title}
          hint={
            sessions.data
              ? `${page.sessions.total(sessions.data.total)} · ${page.sessions.frozenTotal(
                  formatYuan(sessions.data.frozen_micro),
                )}`
              : page.sessions.lead
          }
          action={
            <Button variant="secondary" size="sm" onClick={() => void sessions.refetch()}>
              {page.sessions.refresh}
            </Button>
          }
        >
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(event) => {
              event.preventDefault()
              submitFilter()
            }}
          >
            <Field
              label={page.sessions.filterLabel}
              htmlFor="risk-sessions-listing-id"
              error={filterError}
            >
              <TextInput
                id="risk-sessions-listing-id"
                value={filterInput}
                placeholder={page.sessions.filterPlaceholder}
                autoComplete="off"
                mono
                invalid={Boolean(filterError)}
                onChange={(event) => {
                  setFilterInput(event.target.value)
                  setFilterError(null)
                }}
              />
            </Field>
            <Button type="submit" variant="secondary">
              {page.sessions.submit}
            </Button>
          </form>

          <div className="mt-6">
            {sessions.isPending ? (
              <TableSkeleton rows={4} />
            ) : sessions.isError ? (
              <ErrorBlock error={sessions.error} onRetry={() => void sessions.refetch()} />
            ) : sessions.data && sessions.data.items.length === 0 ? (
              <Empty
                title={page.sessions.empty.title}
                reason={page.sessions.empty.reason}
                next={page.sessions.empty.next}
              />
            ) : sessions.data ? (
              <SessionsTable rows={sessions.data.items} />
            ) : null}
          </div>
        </Section>

        <Section title={page.force.title}>
          <Notice tone="warning">{page.force.lead}</Notice>
          <form
            className="mt-4 flex flex-wrap items-end gap-3"
            onSubmit={(event) => {
              event.preventDefault()
              submitForce()
            }}
          >
            <Field label={page.force.label} htmlFor="risk-force-listing-id" error={forceError}>
              <TextInput
                id="risk-force-listing-id"
                value={forceInput}
                placeholder={page.force.placeholder}
                autoComplete="off"
                mono
                invalid={Boolean(forceError)}
                onChange={(event) => {
                  setForceInput(event.target.value)
                  setForceError(null)
                }}
              />
            </Field>
            <Button type="submit" variant="danger">
              {page.force.submit}
            </Button>
          </form>
        </Section>
      </div>

      <ConfirmDialog
        open={forceTarget !== null}
        title={forceTarget !== null ? page.force.confirmTitle(String(forceTarget)) : ''}
        description={page.force.confirmLead}
        confirmLabel={page.force.confirmLabel}
        tone="danger"
        loading={release.isPending}
        onConfirm={() => {
          if (forceTarget !== null) release.mutate(forceTarget)
        }}
        onClose={() => {
          if (!release.isPending) setForceTarget(null)
        }}
      />
    </>
  )
}

function RelatedTable({ rows }: { rows: AdminRelatedUser[] }) {
  if (rows.length === 0) {
    return <p className="text-meta text-ink-3">{page.related.noRelations}</p>
  }
  return (
    <Table minWidth={520} size="compact">
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{page.related.columns.user}</TableHeaderCell>
          <TableHeaderCell>{page.related.columns.relation}</TableHeaderCell>
          <TableHeaderCell>{page.related.columns.value}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={`${row.relation_type}-${row.user_id}`}>
            <TableCell>
              <span className="tabular-nums">#{row.user_id}</span>
            </TableCell>
            <TableCell>
              <RelationTag relation={row} />
            </TableCell>
            <TableCell className="font-mono text-meta text-ink-2">{row.value}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function SessionsTable({ rows }: { rows: AdminActiveSession[] }) {
  return (
    <Table minWidth={760} size="compact">
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{page.sessions.columns.request}</TableHeaderCell>
          <TableHeaderCell numeric>{page.sessions.columns.listing}</TableHeaderCell>
          <TableHeaderCell numeric>{page.sessions.columns.buyer}</TableHeaderCell>
          <TableHeaderCell numeric>{page.sessions.columns.frozen}</TableHeaderCell>
          <TableHeaderCell>{page.sessions.columns.createdAt}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.request_id}>
            <TableCell>
              <span className="font-mono text-meta">{row.request_id}</span>
            </TableCell>
            <TableCell numeric>#{row.listing_id}</TableCell>
            <TableCell numeric>#{row.buyer_id}</TableCell>
            <TableCell numeric>{formatYuan(row.frozen_micro)}</TableCell>
            <TableCell className="whitespace-nowrap tabular-nums text-meta text-ink-3">
              {formatDateTime(row.created_at)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
