/**
 * 运营后台 · 派生指标（P5）。
 *
 * 只读页面：按指标名与时间范围读取 mp_ops_metrics 的小时桶，展示断流率、
 * 503 原因、指纹异常、订阅信号、槽位占用与预占偏差。数值格式按指标语义
 * 区分（比率、微元、计数），前端不做任何写操作。
 */

import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Empty,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from '@/ui'

import {
  OPS_METRIC_NAMES,
  OPS_METRIC_RANGES,
  opsMetricSince,
  opsMetricsQuery,
  type AdminOpsMetric,
  type OpsMetricName,
  type OpsMetricRange,
} from '@/features/admin/api'
import {
  ErrorBlock,
  Field,
  FilterTabs,
  PageSkeleton,
  RootOnly,
  Section,
  Select,
} from '@/features/admin/components'

export const Route = createFileRoute('/console/ops/metrics')({
  component: MetricsPage,
})

const page = copy.pages.adminMetrics

function metricLabel(name: string): string {
  return (page.metricNames as Record<string, string>)[name] ?? name
}

/** Metric-specific display: rate as a percentage, deviation as money. */
function metricValue(row: AdminOpsMetric): string {
  if (row.metric_name === 'stream_cut_rate') {
    return `${(row.metric_value * 100).toFixed(2)}%`
  }
  if (row.metric_name === 'pre_freeze_deviation') {
    return formatYuan(Math.round(row.metric_value))
  }
  return Number.isInteger(row.metric_value)
    ? String(row.metric_value)
    : row.metric_value.toFixed(2)
}

function dimensionsText(row: AdminOpsMetric): string {
  const entries = Object.entries(row.dimensions)
  if (entries.length === 0) return page.none
  return entries.map(([key, value]) => `${key}=${String(value)}`).join(', ')
}

function MetricsPage() {
  return (
    <RootOnly>
      <MetricsContent />
    </RootOnly>
  )
}

export function MetricsContent() {
  const [metricName, setMetricName] = useState<OpsMetricName | ''>('')
  const [range, setRange] = useState<OpsMetricRange>('24h')
  const since = useMemo(() => opsMetricSince(range), [range])
  const rows = useQuery(opsMetricsQuery({ metric_name: metricName, since }))

  const rangeOptions = OPS_METRIC_RANGES.map((value) => ({
    value,
    label: page.ranges[value],
  }))
  const items = rows.data ?? []

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <div className="mt-6 flex flex-wrap items-start gap-x-6 gap-y-4">
        <Field
          label={page.filters.metric}
          htmlFor="metrics-metric"
          className="w-full sm:w-64"
        >
          <Select
            id="metrics-metric"
            value={metricName}
            onChange={(event) =>
              setMetricName(event.target.value as OpsMetricName | '')
            }
          >
            <option value="">{page.filters.all}</option>
            {OPS_METRIC_NAMES.map((name) => (
              <option key={name} value={name}>
                {metricLabel(name)}
              </option>
            ))}
          </Select>
        </Field>
        <div className="min-w-0">
          <span className="mb-2 block text-body font-medium text-ink">
            {page.filters.range}
          </span>
          <FilterTabs
            options={rangeOptions}
            value={range}
            onChange={setRange}
            ariaLabel={page.filters.aria}
          />
        </div>
      </div>

      <div className="mt-6">
        {rows.isPending ? (
          <PageSkeleton rows={6} />
        ) : rows.isError ? (
          <ErrorBlock error={rows.error} onRetry={() => void rows.refetch()} />
        ) : items.length === 0 ? (
          <Empty
            title={page.empty.title}
            reason={page.empty.reason}
            next={page.empty.next}
          />
        ) : (
          <Section title={page.title} hint={items.length}>
            <MetricTable rows={items} />
          </Section>
        )}
      </div>
    </>
  )
}

function MetricTable({ rows }: { rows: AdminOpsMetric[] }) {
  return (
    <Table minWidth={720} size="compact">
      <TableHead>
        <TableRow interactive={false}>
          <TableHeaderCell>{page.columns.metric}</TableHeaderCell>
          <TableHeaderCell numeric>{page.columns.value}</TableHeaderCell>
          <TableHeaderCell>{page.columns.dimensions}</TableHeaderCell>
          <TableHeaderCell>{page.columns.bucketTime}</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.id}>
            <TableCell>
              <span className="font-medium text-ink">
                {metricLabel(row.metric_name)}
              </span>
              <span className="ml-2 font-mono text-caption text-ink-3">
                {row.metric_name}
              </span>
            </TableCell>
            <TableCell numeric>{metricValue(row)}</TableCell>
            <TableCell className="max-w-[320px] truncate font-mono text-meta text-ink-2">
              <span title={dimensionsText(row)}>{dimensionsText(row)}</span>
            </TableCell>
            <TableCell className="whitespace-nowrap tabular-nums text-meta text-ink-3">
              {formatDateTime(row.bucket_time)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
