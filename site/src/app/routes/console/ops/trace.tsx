/**
 * 运营后台 · 全链路排查（P5）。
 *
 * 输入 request_id，展示一次请求的完整证据：请求详情、重试链（选择 → 每一跳）、
 * 结算账本，以及该挂单最近的验真/巡检/人工审核记录。只读页面，不做任何写操作。
 */

import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
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
} from '@/ui'

import {
  requestTraceQuery,
  type AdminRequestTrace,
  type AdminTraceLedgerRow,
  type AdminTraceProbeRow,
  type AdminTraceRequest,
} from '@/features/admin/api'
import {
  ErrorBlock,
  Field,
  PageSkeleton,
  RootOnly,
  Section,
  TextInput,
} from '@/features/admin/components'

export const Route = createFileRoute('/console/ops/trace')({
  component: TracePage,
})

const page = copy.pages.adminTrace

function requestStatusTag(status: string): ReactNode {
  if (status === 'success') return <Tag tone="brand" dot>{page.labels.success}</Tag>
  if (status === 'failed') return <Tag tone="danger" dot>{page.labels.failed}</Tag>
  return <Tag dot>{status || page.labels.none}</Tag>
}

function entryTypeLabel(entryType: string): string {
  return (page.entryTypes as Record<string, string>)[entryType] ?? entryType
}

function probeTypeLabel(probeType: string): string {
  return (page.probeTypes as Record<string, string>)[probeType] ?? probeType
}

function ledgerStatusLabel(status: string): string {
  return (page.ledgerStatus as Record<string, string>)[status] ?? status
}

function TracePage() {
  return (
    <RootOnly>
      <TraceContent />
    </RootOnly>
  )
}

export function TraceContent() {
  const [input, setInput] = useState('')
  const [requestId, setRequestId] = useState('')
  const [inputError, setInputError] = useState<string | null>(null)
  const trace = useQuery(requestTraceQuery(requestId))

  function submit() {
    const trimmed = input.trim()
    if (trimmed === '') {
      setInputError(page.form.required)
      return
    }
    setInputError(null)
    setRequestId(trimmed)
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <form
        className="mt-6 flex flex-wrap items-end gap-3"
        onSubmit={(event) => {
          event.preventDefault()
          submit()
        }}
      >
        <Field label={page.form.label} htmlFor="trace-request-id" error={inputError}>
          <TextInput
            id="trace-request-id"
            value={input}
            placeholder={page.form.placeholder}
            autoComplete="off"
            mono
            invalid={Boolean(inputError)}
            onChange={(event) => {
              setInput(event.target.value)
              setInputError(null)
            }}
          />
        </Field>
        <Button
          type="submit"
          loading={requestId !== '' && trace.isFetching}
        >
          {requestId !== '' && trace.isFetching ? page.form.submitting : page.form.submit}
        </Button>
      </form>

      <div className="mt-6">
        {requestId === '' ? (
          <Empty
            title={page.empty.title}
            reason={page.empty.reason}
            next={page.empty.next}
          />
        ) : trace.isPending ? (
          <PageSkeleton rows={6} />
        ) : trace.isError ? (
          <ErrorBlock error={trace.error} onRetry={() => void trace.refetch()} />
        ) : trace.data ? (
          <TraceResult trace={trace.data} />
        ) : null}
      </div>
    </>
  )
}

function DetailItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-[12px] uppercase tracking-[0.08em] text-ink-3">{label}</dt>
      <dd className="mt-1 break-words text-[14px] text-ink">{children}</dd>
    </div>
  )
}

function TraceResult({ trace }: { trace: AdminRequestTrace }) {
  const request = trace.request
  return (
    <div className="space-y-6">
      <Section title={page.requestTitle}>
        <RequestDetails request={request} />
      </Section>

      <Section title={page.retryTitle} hint={trace.retry_chain.length || undefined}>
        {trace.retry_chain.length === 0 ? (
          <p className="text-[13px] text-ink-3">{page.retryEmpty}</p>
        ) : (
          <ol className="relative ml-1 space-y-4 border-l border-line pl-5">
            {trace.retry_chain.map((hop, index) => (
              <li key={`${hop.seq}-${index}`} className="relative">
                <span
                  aria-hidden="true"
                  className="absolute -left-[27px] top-1.5 h-2.5 w-2.5 rounded-full bg-brand"
                />
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span className="text-[13px] font-medium text-ink">
                    #{hop.seq} · {hop.event}
                  </span>
                  {hop.listing_id > 0 ? (
                    <span className="tabular-nums text-[13px] text-ink-2">
                      {page.retryColumns.listing} #{hop.listing_id}
                    </span>
                  ) : null}
                  <span className="tabular-nums text-[13px] text-ink-3">
                    {formatDateTime(hop.at)}
                  </span>
                </div>
                {hop.detail ? (
                  <p className="mt-1 whitespace-pre-wrap break-words text-[13px] leading-5 text-ink-2">
                    {hop.detail}
                  </p>
                ) : null}
              </li>
            ))}
          </ol>
        )}
      </Section>

      <Section title={page.ledgerTitle} hint={trace.ledger.length || undefined}>
        {trace.ledger.length === 0 ? (
          <p className="text-[13px] text-ink-3">{page.ledgerEmpty}</p>
        ) : (
          <LedgerTable rows={trace.ledger} />
        )}
      </Section>

      <Section title={page.probesTitle} hint={trace.probes.length || undefined}>
        {trace.probes.length === 0 ? (
          <p className="text-[13px] text-ink-3">{page.probesEmpty}</p>
        ) : (
          <ProbeTable rows={trace.probes} />
        )}
      </Section>
    </div>
  )
}

function RequestDetails({ request }: { request: AdminTraceRequest }) {
  const tokens = `${request.input_tokens} / ${request.output_tokens}`
  const cache = `${request.cache_read_tokens} / ${request.cache_write_tokens}`
  const tools = `${request.search_count} / ${request.fetch_count}`
  return (
    <>
      <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
        <DetailItem label={page.request.requestId}>
          <span className="font-mono text-[13px]">{request.request_id}</span>
        </DetailItem>
        <DetailItem label={page.request.status}>{requestStatusTag(request.status)}</DetailItem>
        <DetailItem label={page.request.createdAt}>
          {formatDateTime(request.created_at)}
        </DetailItem>
        <DetailItem label={page.request.buyer}>
          <span className="tabular-nums">#{request.buyer_user_id}</span>
        </DetailItem>
        <DetailItem label={page.request.seller}>
          <span className="tabular-nums">#{request.seller_id}</span>
        </DetailItem>
        <DetailItem label={page.request.listing}>
          <span className="tabular-nums">#{request.listing_id}</span>
        </DetailItem>
        <DetailItem label={page.request.model}>{request.model}</DetailItem>
        <DetailItem label={page.request.upstreamModel}>
          {request.upstream_model || page.labels.none}
        </DetailItem>
        <DetailItem label={page.request.tier}>{request.tier || page.labels.none}</DetailItem>
        <DetailItem label={page.request.priceVersion}>{request.price_version}</DetailItem>
        <DetailItem label={page.request.multiplier}>
          <Multiplier milli={request.multiplier_milli} showSaving={false} />
        </DetailItem>
        <DetailItem label={page.request.latency}>
          <span className="tabular-nums">{request.latency_ms} ms</span>
        </DetailItem>
        <DetailItem label={page.request.tokens}>
          <span className="tabular-nums">{tokens}</span>
        </DetailItem>
        <DetailItem label={page.request.cache}>
          <span className="tabular-nums">{cache}</span>
        </DetailItem>
        <DetailItem label={page.request.tools}>
          <span className="tabular-nums">{tools}</span>
        </DetailItem>
        <DetailItem label={page.request.totalDebit}>
          <span className="tabular-nums">{formatYuan(request.total_debit_micro)}</span>
        </DetailItem>
        <DetailItem label={page.request.platformFee}>
          <span className="tabular-nums">{formatYuan(request.platform_fee_micro)}</span>
        </DetailItem>
        <DetailItem label={page.request.sellerCredit}>
          <span className="tabular-nums">{formatYuan(request.seller_credit_micro)}</span>
        </DetailItem>
        <DetailItem label={page.request.officialCost}>
          <span className="tabular-nums">{formatYuan(request.official_cost_micro)}</span>
        </DetailItem>
        <DetailItem label={page.request.giftDebit}>
          <span className="tabular-nums">{formatYuan(request.gift_debit_micro)}</span>
        </DetailItem>
        <DetailItem label={page.request.capped}>
          <span className="tabular-nums">{formatYuan(request.capped_micro)}</span>
        </DetailItem>
        <DetailItem label={page.request.usageEstimated}>
          {request.usage_estimated ? page.request.yes : page.request.no}
        </DetailItem>
        <DetailItem label={page.request.fingerprintAnomaly}>
          {request.model_fingerprint_anomaly ? page.request.yes : page.request.no}
        </DetailItem>
      </dl>
    </>
  )
}

function LedgerTable({ rows }: { rows: AdminTraceLedgerRow[] }) {
  return (
    <Table minWidth={820} size="compact">
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{page.ledgerColumns.entryType}</TableHeaderCell>
          <TableHeaderCell>{page.ledgerColumns.account}</TableHeaderCell>
          <TableHeaderCell numeric>{page.ledgerColumns.amount}</TableHeaderCell>
          <TableHeaderCell>{page.ledgerColumns.status}</TableHeaderCell>
          <TableHeaderCell numeric>{page.ledgerColumns.priceVersion}</TableHeaderCell>
          <TableHeaderCell>{page.ledgerColumns.createdAt}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.id}>
            <TableCell>{entryTypeLabel(row.entry_type)}</TableCell>
            <TableCell className="font-mono text-[13px] text-ink-2">{row.account}</TableCell>
            <TableCell numeric>{formatYuan(row.amount_micro)}</TableCell>
            <TableCell>{ledgerStatusLabel(row.status)}</TableCell>
            <TableCell numeric>{row.price_version}</TableCell>
            <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
              {formatDateTime(row.created_at)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function ProbeTable({ rows }: { rows: AdminTraceProbeRow[] }) {
  return (
    <Table minWidth={820} size="compact">
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{page.probesColumns.type}</TableHeaderCell>
          <TableHeaderCell>{page.probesColumns.ok}</TableHeaderCell>
          <TableHeaderCell numeric>{page.probesColumns.latency}</TableHeaderCell>
          <TableHeaderCell>{page.probesColumns.detail}</TableHeaderCell>
          <TableHeaderCell>{page.probesColumns.createdAt}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.id}>
            <TableCell>{probeTypeLabel(row.type)}</TableCell>
            <TableCell>
              <Tag tone={row.ok ? 'brand' : 'danger'} dot>
                {row.ok ? page.labels.ok : page.labels.fail}
              </Tag>
            </TableCell>
            <TableCell numeric>{row.latency_ms} ms</TableCell>
            <TableCell className="max-w-[360px] truncate text-[13px] text-ink-2" title={row.detail}>
              {row.detail || page.labels.none}
            </TableCell>
            <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
              {formatDateTime(row.created_at)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
