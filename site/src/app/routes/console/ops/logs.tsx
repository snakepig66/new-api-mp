import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { useAuth } from '@/lib/auth'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Empty,
  PageHeader,
  Pagination,
  Segmented,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
} from '@/ui'

import { ADMIN_LOGS_PAGE_SIZE, adminLogsQuery } from '@/features/admin/logs-api'
import { ErrorBlock, TableSkeleton, TextInput } from '@/features/admin/components'
import { fetchQuotaPerUnit } from '@/features/admin/users-api'

export const Route = createFileRoute('/console/ops/logs')({
  component: LogsPage,
})

type Scope = 'all' | 'self'
type Preset = 'today' | '7d' | '30d'

const PRESET_OPTIONS: { value: Preset; label: string }[] = [
  { value: 'today', label: '今天' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
]

function presetRange(preset: Preset, anchor: number): { start: number; end: number } {
  const end = Math.floor(anchor / 1000)
  const startOfDay = new Date(anchor)
  startOfDay.setHours(0, 0, 0, 0)
  if (preset === 'today') return { start: Math.floor(startOfDay.getTime() / 1000), end }
  const days = preset === '7d' ? 7 : 30
  return { start: Math.floor(startOfDay.getTime() / 1000) - (days - 1) * 86400, end }
}

function formatQuota(quota: number, perUnit: number): string {
  return formatYuan(Math.round((quota / perUnit) * 1_000_000))
}

function LogsPage() {
  const { user } = useAuth()
  const [scope, setScope] = useState<Scope>('all')
  const [preset, setPreset] = useState<Preset>('7d')
  const [usernameInput, setUsernameInput] = useState('')
  const [modelInput, setModelInput] = useState('')
  const [username, setUsername] = useState('')
  const [model, setModel] = useState('')
  const [page, setPage] = useState(1)
  const [anchor, setAnchor] = useState(() => Date.now())

  const range = useMemo(() => presetRange(preset, anchor), [preset, anchor])
  const effectiveUsername = scope === 'self' ? user?.username ?? '' : username

  const perUnit = useQuery({
    queryKey: ['admin', 'quota-per-unit'],
    queryFn: fetchQuotaPerUnit,
    staleTime: 10 * 60_000,
  })
  const unit = perUnit.data ?? 1_000_000

  const logs = useQuery(
    adminLogsQuery({
      username: effectiveUsername,
      model_name: model,
      start_timestamp: range.start,
      end_timestamp: range.end,
      page,
      page_size: ADMIN_LOGS_PAGE_SIZE,
    }),
  )

  const rows = logs.data?.items ?? []
  const total = logs.data?.total ?? 0

  function submit() {
    setUsername(usernameInput.trim())
    setModel(modelInput.trim())
    setPage(1)
    setAnchor(Date.now())
  }

  function switchScope(next: Scope) {
    setScope(next)
    setPage(1)
  }

  function switchPreset(next: Preset) {
    setPreset(next)
    setPage(1)
    setAnchor(Date.now())
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="调用记录"
        lead="全站所有用户的调用明细。默认显示全部用户，可切换为仅自己；时间范围为自然日。"
      />

      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Segmented
            options={[
              { value: 'all', label: '全部用户' },
              { value: 'self', label: '仅自己' },
            ]}
            value={scope}
            onChange={(value) => switchScope(value as Scope)}
            size="sm"
          />
          <Segmented
            options={PRESET_OPTIONS}
            value={preset}
            onChange={(value) => switchPreset(value as Preset)}
            size="sm"
          />
          <div className="ml-auto text-meta text-ink-3">
            共 <strong className="font-medium text-ink">{total}</strong> 条调用
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="w-full sm:w-56">
            <TextInput
              placeholder={scope === 'self' ? `仅看 ${user?.username ?? '自己'}` : '按用户名筛选'}
              value={scope === 'self' ? (user?.username ?? '') : usernameInput}
              disabled={scope === 'self'}
              onChange={(e) => setUsernameInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit()
              }}
            />
          </div>
          <div className="w-full sm:w-56">
            <TextInput
              placeholder="按模型筛选，如 deepseek-v4.1-flash-1017"
              value={modelInput}
              onChange={(e) => setModelInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit()
              }}
            />
          </div>
          <Button variant="secondary" size="sm" onClick={submit}>
            搜索
          </Button>
        </div>
      </div>

      {logs.isPending ? (
        <TableSkeleton rows={8} columns={9} />
      ) : logs.isError ? (
        <ErrorBlock error={logs.error} onRetry={() => void logs.refetch()} />
      ) : rows.length === 0 ? (
        <Empty
          title="暂无调用记录"
          reason={
            scope === 'self'
              ? '你在这个时间范围内没有调用记录'
              : '这个时间范围内没有匹配的调用记录'
          }
          next={scope === 'self' ? '切换到「全部用户」或调整时间范围' : '调整用户名、模型或时间范围后重试'}
        />
      ) : (
        <>
          <Table minWidth={1000} className="[&_td]:px-3 [&_th]:px-3">
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>时间</TableHeaderCell>
                <TableHeaderCell>用户</TableHeaderCell>
                <TableHeaderCell>模型</TableHeaderCell>
                <TableHeaderCell>令牌</TableHeaderCell>
                <TableHeaderCell numeric>输入 tokens</TableHeaderCell>
                <TableHeaderCell numeric>输出 tokens</TableHeaderCell>
                <TableHeaderCell numeric>额度</TableHeaderCell>
                <TableHeaderCell numeric>耗时</TableHeaderCell>
                <TableHeaderCell>渠道</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className="whitespace-nowrap text-ink-3">
                    {formatDateTime(row.created_at)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap font-medium text-ink">
                    {row.username || '—'}
                  </TableCell>
                  <TableCell className="max-w-[240px] truncate" title={row.model_name}>
                    {row.model_name || '—'}
                  </TableCell>
                  <TableCell className="max-w-[140px] truncate text-ink-3" title={row.token_name}>
                    {row.token_name || '—'}
                  </TableCell>
                  <TableCell numeric className="tabular-nums text-ink-3">
                    {row.prompt_tokens.toLocaleString()}
                  </TableCell>
                  <TableCell numeric className="tabular-nums text-ink-3">
                    {row.completion_tokens.toLocaleString()}
                  </TableCell>
                  <TableCell numeric className="whitespace-nowrap tabular-nums">
                    {formatQuota(row.quota, unit)}
                  </TableCell>
                  <TableCell numeric className="whitespace-nowrap tabular-nums text-ink-3">
                    {row.use_time > 0 ? `${row.use_time}s` : '—'}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-ink-3">
                    <div className="flex items-center gap-1.5">
                      {row.channel_name ? (
                        <span className="max-w-[140px] truncate" title={row.channel_name}>
                          {row.channel_name}
                        </span>
                      ) : (
                        <span className="text-ink-4">#{row.channel}</span>
                      )}
                      {row.is_stream ? <Tag tone="brand">流式</Tag> : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="flex justify-end">
            <Pagination
              page={page}
              pageSize={ADMIN_LOGS_PAGE_SIZE}
              total={total}
              onPageChange={setPage}
              ariaLabel="调用记录分页"
              showSummary
            />
          </div>
        </>
      )}
    </div>
  )
}
