import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
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
} from '@/ui'

import { shareRecordsQuery } from '@/features/seller/api'
import { AsyncError, BuyerCode, PageSkeleton } from '@/features/seller/components'
import { sellerErrorMessage } from '@/features/seller/errors'
import { shareStatusView } from '@/features/seller/status'

export const Route = createFileRoute('/seller/records')({
  component: ShareRecordsPage,
})

const page = copy.pages.sellerRecords
const PAGE_SIZE = 20
const tokenFormat = new Intl.NumberFormat('en-US')

function ShareRecordsPage() {
  const [pageNumber, setPageNumber] = useState(1)
  const records = useQuery(shareRecordsQuery(pageNumber, PAGE_SIZE))

  if (records.isPending) return <PageSkeleton />

  if (records.isError) {
    return (
      <AsyncError
        title={page.loadError}
        reason={sellerErrorMessage(records.error)}
        onRetry={() => void records.refetch()}
        retryLabel={copy.actions.retry}
      />
    )
  }

  const data = records.data
  const rows = data?.items ?? []
  const total = data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <span className="tabular-nums text-[13px] text-ink-3">
            {copy.pages.sellerCommon.totalCount(total)}
          </span>
        }
      />

      {rows.length === 0 ? (
        <Empty
          className="mt-6"
          title={page.emptyTitle}
          reason={page.emptyReason}
          next={page.emptyNext}
        />
      ) : (
        <>
          <div className="mt-6 rounded-md border border-line bg-bg p-6">
            <Table minWidth={920}>
              <TableHead>
                <TableRow>
                  <TableHeaderCell>{page.columns.time}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.model}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.buyer}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.tokens}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.income}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.fee}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {rows.map((record) => {
                  const status = shareStatusView(record.status)
                  return (
                    <TableRow key={record.request_id}>
                      <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                        {formatDateTime(record.created_at)}
                      </TableCell>
                      <TableCell>
                        <div className="tabular-nums text-[13px] text-ink">{record.model}</div>
                        <div className="text-[13px] text-ink-3">
                          {page.tierPrefix} {record.tier_label || record.tier}
                        </div>
                      </TableCell>
                      <TableCell>
                        <BuyerCode code={record.buyer_code} />
                      </TableCell>
                      <TableCell numeric className="whitespace-nowrap">
                        {tokenFormat.format(record.input_tokens)} /{' '}
                        {tokenFormat.format(record.output_tokens)}
                      </TableCell>
                      <TableCell numeric className="whitespace-nowrap font-medium">
                        {formatYuan(record.seller_credit_micro)}
                      </TableCell>
                      <TableCell numeric className="whitespace-nowrap text-ink-3">
                        {formatYuan(record.platform_fee_micro)}
                      </TableCell>
                      <TableCell>
                        <Tag tone={status.tone} dot>
                          {status.label}
                        </Tag>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>

            <div className="mt-5 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-4">
              <span className="text-[13px] text-ink-3">
                {page.pagination.summary(pageNumber, pages, total)}
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={pageNumber <= 1}
                  onClick={() => setPageNumber((current) => Math.max(1, current - 1))}
                >
                  {page.pagination.prev}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={pageNumber >= pages}
                  onClick={() => setPageNumber((current) => Math.min(pages, current + 1))}
                >
                  {page.pagination.next}
                </Button>
              </div>
            </div>
          </div>
        </>
      )}
    </>
  )
}
