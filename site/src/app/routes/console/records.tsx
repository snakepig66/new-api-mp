import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  PageHeader,
  PeakBadge,
  SellerId,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  buttonClasses,
} from '@/ui'

import {
  usageRecordCostQuery,
  usageRecordsCostQuery,
} from '@/features/console/api'
import { meQuery } from '@/features/seller/api'
import { ShareRecordsTable } from '@/features/seller/ShareRecordsTable'
import { isSellerActive } from '@/features/seller/status'
import {
  ErrorBlock,
  PageSkeleton,
  Section,
  Skeleton,
} from '@/features/console/components/DataState'
import { formatCount, prettyJson } from '@/features/console/format'
import { toUsageRow, type UsageRow } from '@/features/console/records'
import type { UsageCostRecordDetail } from '@/features/console/api'
import type { UsageTier } from '@/features/console/types'

const PAGE_SIZE = 20

export const Route = createFileRoute('/console/records')({
  validateSearch: (
    search: Record<string, unknown>,
  ): { page: number; request?: string; view?: 'calls' | 'sales' } => {
    const parsed = Number(search.page)
    const view = search.view === 'sales' ? 'sales' : search.view === 'calls' ? 'calls' : undefined
    return {
      page: Number.isInteger(parsed) && parsed > 0 ? parsed : 1,
      request:
        typeof search.request === 'string' && search.request ? search.request : undefined,
      view,
    }
  },
  component: RecordsPage,
})

const page = copy.pages.consoleRecords

function RecordsPage() {
  const { page: currentPage, request, view } = Route.useSearch()
  const navigate = useNavigate()
  const me = useQuery(meQuery())
  const sellerActive = me.data ? isSellerActive(me.data.seller) : false
  const activeView = sellerActive ? (view ?? 'calls') : 'calls'
  const records = useQuery({
    ...usageRecordsCostQuery(currentPage, PAGE_SIZE),
    enabled: activeView === 'calls',
  })

  const total = records.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  function goTo(next: number) {
    void navigate({
      to: '/console/records',
      search: { page: Math.min(Math.max(1, next), totalPages) },
    })
  }

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      {sellerActive ? (
        <nav className="mt-5 flex items-center gap-1.5 overflow-x-auto" aria-label={page.title}>
          {([
            { key: 'calls' as const, label: page.tabs.calls },
            { key: 'sales' as const, label: page.tabs.sales },
          ]).map((tab) => {
            const active = activeView === tab.key
            return (
              <Link
                key={tab.key}
                to="/console/records"
                search={{ page: 1, view: tab.key }}
                aria-current={active ? 'page' : undefined}
                className={
                  active
                    ? 'inline-flex h-9 shrink-0 items-center rounded-full bg-brand-soft px-3.5 text-[14px] font-medium text-brand-ink'
                    : 'inline-flex h-9 shrink-0 items-center rounded-full px-3.5 text-[14px] text-ink-2 transition-colors hover:bg-bg-soft hover:text-ink'
                }
              >
                {tab.label}
              </Link>
            )
          })}
        </nav>
      ) : null}

      {activeView === 'sales' ? (
        <div className="mt-6">
          <ShareRecordsTable />
        </div>
      ) : records.isPending ? (
        <div className="mt-6">
          <PageSkeleton />
        </div>
      ) : records.isError ? (
        <ErrorBlock
          className="mt-6"
          error={records.error}
          onRetry={() => void records.refetch()}
        />
      ) : records.data.items.length === 0 ? (
        <Empty
          className="mt-6"
          title={page.empty.title}
          reason={page.empty.reason}
          next={page.empty.next}
          action={
            <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
              {copy.pages.consoleKeys.create}
            </Link>
          }
        />
      ) : (
        <Section className="mt-6" title={page.title}>
          <Table minWidth={1180}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>{page.columns.time}</TableHeaderCell>
                <TableHeaderCell>{page.columns.model}</TableHeaderCell>
                <TableHeaderCell>{page.columns.seller}</TableHeaderCell>
                <TableHeaderCell>{page.columns.tier}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.tokens}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.tools}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.official}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.paid}</TableHeaderCell>
                <TableHeaderCell numeric>{page.columns.saved}</TableHeaderCell>
                <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                <TableHeaderCell>{page.columns.actions}</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {records.data.items.map((record) => {
                const row = toUsageRow(record)
                return <RecordRow key={row.requestId} row={row} />
              })}
            </TableBody>
          </Table>

          <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
            <span className="tabular-nums text-[13px] text-ink-3">
              {page.pagination.summary(currentPage, totalPages)}
            </span>
            <div className="flex items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={currentPage <= 1}
                onClick={() => goTo(currentPage - 1)}
              >
                {page.pagination.previous}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={currentPage >= totalPages}
                onClick={() => goTo(currentPage + 1)}
              >
                {page.pagination.next}
              </Button>
            </div>
          </div>
        </Section>
      )}

      {activeView === 'calls' && request ? (
        <RecordDialog
          requestId={request}
          onClose={() =>
            void navigate({
              to: '/console/records',
              search: { page: currentPage },
              replace: true,
            })
          }
        />
      ) : null}
    </>
  )
}

function RecordRow({ row }: { row: UsageRow }) {
  const navigate = useNavigate()
  const { page: currentPage } = Route.useSearch()

  function open() {
    void navigate({
      to: '/console/records',
      search: { page: currentPage, request: row.requestId },
    })
  }

  return (
    <tr
      tabIndex={0}
      aria-label={`${page.detail.title} ${row.requestId}`}
      className="cursor-pointer border-b border-line/70 transition-colors last:border-b-0 hover:bg-bg-soft/70 focus-visible:bg-bg-soft/70"
      onClick={open}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          open()
        }
      }}
    >
      <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
        {formatDateTime(row.createdAt)}
      </TableCell>
      <TableCell>
        <div className="max-w-[220px] truncate font-medium text-ink" title={row.model}>
          {row.model}
        </div>
        <div className="tabular-nums text-[13px] text-ink-3">{row.requestId}</div>
      </TableCell>
      <TableCell>
        <SellerId id={row.sellerId} />
      </TableCell>
      <TableCell>
        <TierBadge tier={row.tier} />
      </TableCell>
      <TableCell numeric>
        <div className="text-[13px]">
          {page.tokens.input(formatCount(row.inputTokens))}
          {' · '}
          {page.tokens.output(formatCount(row.outputTokens))}
        </div>
        <div className="text-[13px] text-ink-3">
          {page.tokens.cache(formatCount(row.cacheReadTokens))}
        </div>
      </TableCell>
      <TableCell numeric className="text-[13px]">
        {row.searchCount} / {row.fetchCount}
      </TableCell>
      <TableCell numeric className="text-ink-3">
        {formatYuan(row.officialCostMicro)}
      </TableCell>
      <TableCell numeric>{formatYuan(row.totalDebitMicro)}</TableCell>
      <TableCell numeric className="text-brand-ink">
        {formatYuan(row.savedMicro)}
      </TableCell>
      <TableCell>
        <StatusTag status={row.status} />
      </TableCell>
      <TableCell>
        <Button
          variant="secondary"
          size="sm"
          onClick={(event) => {
            event.stopPropagation()
            open()
          }}
          onKeyDown={(event) => event.stopPropagation()}
        >
          {page.detail.costAction}
        </Button>
      </TableCell>
    </tr>
  )
}

function TierBadge({ tier }: { tier: UsageTier }) {
  if (tier === 'peak' || tier === 'offpeak') return <PeakBadge tier={tier} />
  return <Tag tone="neutral">{page.tierFlat}</Tag>
}

function StatusTag({ status }: { status: string }) {
  if (status === 'success') return <Tag tone="brand">{page.status.success}</Tag>
  if (status === 'failed') return <Tag tone="danger">{page.status.failed}</Tag>
  return <Tag tone="neutral">{page.status.unknown}</Tag>
}

// The detail dialog is opened by the `request` search parameter so a record
// can be linked to directly and the back button closes it.
function RecordDialog({ requestId, onClose }: { requestId: string; onClose: () => void }) {
  const detail = useQuery(usageRecordCostQuery(requestId))
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      title={page.detail.costTitle}
      description={requestId}
      className="max-h-[calc(100vh-3rem)] max-w-2xl overflow-y-auto"
      footer={
        <Button variant="secondary" onClick={onClose}>
          {page.detail.close}
        </Button>
      }
    >
      {detail.isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-4 w-3/5" />
          <Skeleton className="mt-4 h-24 w-full" />
        </div>
      ) : detail.isError ? (
        <ErrorBlock error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <RecordDetailBody detail={detail.data} />
      )}
    </Dialog>
  )
}

export function RecordDetailBody({ detail }: { detail: UsageCostRecordDetail }) {
  const row = toUsageRow(detail)
  const retryText = prettyJson(detail.retry_chain)
  const totalTokens =
    row.inputTokens + row.outputTokens + row.cacheReadTokens + row.cacheWriteTokens
  return (
    <div className="space-y-5">
      <dl className="grid grid-cols-2 gap-x-5 gap-y-2 text-[13px]">
        <Info label={page.detail.requestId} value={row.requestId} mono />
        <Info label={page.detail.createdAt} value={formatDateTime(row.createdAt)} mono />
        <Info label={page.detail.model} value={row.model} mono />
        <Info
          label={page.detail.channelType}
          value={channelTypeLabel(detail.channel_type)}
        />
        <div>
          <dt className="text-ink-3">{page.detail.seller}</dt>
          <dd className="mt-0.5">
            <SellerId id={row.sellerId} />
          </dd>
        </div>
        <div>
          <dt className="text-ink-3">{page.detail.tier}</dt>
          <dd className="mt-0.5">
            <TierBadge tier={row.tier} />
          </dd>
        </div>
        <div>
          <dt className="text-ink-3">{page.detail.status}</dt>
          <dd className="mt-0.5">
            <StatusTag status={row.status} />
          </dd>
        </div>
      </dl>

      {row.usageEstimated ? (
        <p className="border border-warn/25 bg-warn/10 px-3 py-2 text-[13px] text-warn">
          {page.detail.estimated}
        </p>
      ) : null}

      <div>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {page.detail.tokens}
        </h3>
        <dl className="mt-2 grid grid-cols-2 gap-x-5 gap-y-1.5 text-[13px] sm:grid-cols-3">
          <Info label={page.detail.input} value={formatCount(row.inputTokens)} mono />
          <Info label={page.detail.output} value={formatCount(row.outputTokens)} mono />
          <Info label={page.detail.cacheRead} value={formatCount(row.cacheReadTokens)} mono />
          <Info label={page.detail.cacheWrite} value={formatCount(row.cacheWriteTokens)} mono />
          <Info label={page.detail.cacheHit} value={formatCount(row.cacheHitTokens)} mono />
          <Info label={page.detail.search} value={formatCount(row.searchCount)} mono />
          <Info label={page.detail.fetch} value={formatCount(row.fetchCount)} mono />
        </dl>
      </div>

      <div>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {page.detail.cost}
        </h3>
        <dl className="mt-2 grid grid-cols-2 gap-x-5 gap-y-1.5 text-[13px] sm:grid-cols-3">
          <Info label={page.detail.official} value={formatYuan(row.officialCostMicro)} mono />
          <Info label={page.detail.paid} value={formatYuan(row.totalDebitMicro)} mono />
          <Info label={page.detail.saved} value={formatYuan(row.savedMicro)} mono />
          <Info label={page.detail.toolDebit} value={formatYuan(row.toolDebitMicro)} mono />
          <Info
            label={`${page.detail.officialUnitPrice}（${page.detail.unitPriceUnit}）`}
            value={formatUnitPrice(row.officialCostMicro, totalTokens)}
            mono
          />
          <Info
            label={`${page.detail.paidUnitPrice}（${page.detail.unitPriceUnit}）`}
            value={formatUnitPrice(row.totalDebitMicro, totalTokens)}
            mono
          />
          <Info
            label={page.detail.multiplier}
            value={formatPriceRatio(detail.multiplier_milli)}
            mono
          />
          <Info label={page.detail.platformFee} value={formatYuan(detail.platform_fee_micro)} mono />
          <Info label={page.detail.sellerCredit} value={formatYuan(detail.seller_credit_micro)} mono />
        </dl>
      </div>

      <div>
        <h3 className="border-b border-line pb-1.5 text-[13px] font-medium text-ink">
          {page.detail.retry}
        </h3>
        {retryText ? (
          <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-[#0d1117] p-4 font-mono text-[13px] leading-6 text-[#e6edf3]">
            <code>{retryText}</code>
          </pre>
        ) : (
          <p className="mt-2 text-[13px] text-ink-3">{page.detail.retryEmpty}</p>
        )}
      </div>
    </div>
  )
}

/** 渠道类型 label; an unknown or missing supplier falls back to 分享渠道. */
export function channelTypeLabel(channelType: string | undefined): string {
  switch (channelType) {
    case 'official':
      return page.detail.channelTypes.official
    case 'plan':
      return page.detail.channelTypes.plan
    case 'third_party':
      return page.detail.channelTypes.thirdParty
    case 'shared':
      return page.detail.channelTypes.shared
    case '':
    case undefined:
      return page.detail.channelTypes.shared
    default:
      return page.detail.channelTypes.unknown
  }
}

/**
 * Blended unit price in 元 per million tokens. The record does not store the
 * four price-book unit prices, so the dialog shows the effective rate implied
 * by the stored totals; zero tokens render as an em dash.
 */
export function formatUnitPrice(amountMicro: number, totalTokens: number): string {
  if (!Number.isFinite(totalTokens) || totalTokens <= 0) return '—'
  const perMillion = amountMicro / totalTokens
  if (!Number.isFinite(perMillion)) return '—'
  return `¥${perMillion.toFixed(perMillion >= 1 ? 2 : 4)}`
}

/** 计价比例 from milli (300 → "30%"); zero means "not recorded". */
export function formatPriceRatio(multiplierMilli: number): string {
  if (!Number.isFinite(multiplierMilli) || multiplierMilli <= 0) return '—'
  const percent = multiplierMilli / 10
  return `${percent.toFixed(percent % 1 === 0 ? 0 : 1)}%`
}

function Info({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-ink-3">{label}</dt>
      <dd className={mono ? 'mt-0.5 truncate tabular-nums text-[13px] text-ink' : 'mt-0.5 truncate text-ink'}>
        {value}
      </dd>
    </div>
  )
}
