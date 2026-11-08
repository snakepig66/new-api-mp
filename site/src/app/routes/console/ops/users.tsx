import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { useAuth } from '@/lib/auth'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { formatDate, formatDateTime, formatMonthDay } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  PageHeader,
  Pagination,
  Segmented,
  Switch,
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
  ADMIN_USERS_PAGE_SIZE,
  adjustUserQuota,
  adminTopUpsQuery,
  adminUsersQuery,
  completeAdminTopUp,
  fetchQuotaPerUnit,
  manageAdminUser,
  type AdminTopUp,
  type AdminUser,
  type QuotaAdjustMode,
  type UserManageAction,
} from '@/features/admin/users-api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  TableSkeleton,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

export const Route = createFileRoute('/console/ops/users')({
  component: UsersPage,
})

type Tab = 'users' | 'topups'

const ROLE_LABELS: Record<number, string> = {
  1: '普通用户',
  10: '管理员',
  100: '超级管理员',
}

function roleLabel(role: number): string {
  return ROLE_LABELS[role] ?? `角色 ${role}`
}

/** 额度为单位，生产 1,000,000 额度 = ¥1，这里换算成微元交给 formatYuan。 */
function formatQuota(quota: number, perUnit: number): string {
  const micro = Math.round((quota / perUnit) * 1_000_000)
  return formatYuan(micro)
}

/** 人民币输入 → 整数额度，非法或非正数返回 null。 */
function yuanToQuota(yuan: string, perUnit: number): number | null {
  const trimmed = yuan.trim()
  if (!trimmed) return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed <= 0) return null
  return Math.round(parsed * perUnit)
}

interface QuotaDraft {
  user: AdminUser
  mode: QuotaAdjustMode
  amount: string
}

function UsersPanel({ perUnit }: { perUnit: number }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const { user: operator, isRoot } = useAuth()
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [draft, setDraft] = useState<QuotaDraft | null>(null)
  const [pendingAction, setPendingAction] = useState<{
    user: AdminUser
    action: UserManageAction
  } | null>(null)

  const users = useQuery(adminUsersQuery({ keyword: search, page, page_size: ADMIN_USERS_PAGE_SIZE }))

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  }

  const adjust = useMutation({
    mutationFn: ({ user, mode, quota }: { user: AdminUser; mode: QuotaAdjustMode; quota: number }) =>
      adjustUserQuota(user.id, mode, quota),
    onSuccess: () => {
      refresh()
      toast.show({ title: '余额已调整', tone: 'success' })
      setDraft(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const manage = useMutation({
    mutationFn: ({ user, action }: { user: AdminUser; action: UserManageAction }) =>
      manageAdminUser(user.id, action),
    onSuccess: () => {
      refresh()
      toast.show({ title: '用户状态已更新', tone: 'success' })
      setPendingAction(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function submitSearch() {
    setSearch(keyword.trim())
    setPage(1)
  }

  function openQuota(user: AdminUser) {
    setDraft({ user, mode: 'add', amount: '' })
  }

  function openToggle(user: AdminUser) {
    setPendingAction({ user, action: user.status === 1 ? 'disable' : 'enable' })
  }

  const rows = users.data?.items ?? []
  const total = users.data?.total ?? 0

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex w-full gap-2 sm:w-auto">
          <div className="w-full sm:w-72">
            <TextInput
              placeholder="搜索用户名、显示名、邮箱或 ID..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submitSearch()
              }}
            />
          </div>
          <Button variant="secondary" size="sm" onClick={submitSearch}>
            搜索
          </Button>
        </div>
        <div className="text-meta text-ink-3">
          共 <strong className="font-medium text-ink">{total}</strong> 位用户
        </div>
      </div>

      {users.isPending ? (
        <TableSkeleton rows={8} columns={7} />
      ) : users.isError ? (
        <ErrorBlock error={users.error} onRetry={refresh} />
      ) : rows.length === 0 ? (
        <Empty
          title="暂无用户"
          reason={search ? '没有匹配搜索条件的用户' : '当前还没有注册用户'}
          next={search ? '调整关键字后重试' : undefined}
        />
      ) : (
        <>
          <Table minWidth={940} className="[&_td]:px-3 [&_th]:px-3">
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>用户</TableHeaderCell>
                <TableHeaderCell>角色</TableHeaderCell>
                <TableHeaderCell>状态</TableHeaderCell>
                <TableHeaderCell numeric>余额</TableHeaderCell>
                <TableHeaderCell numeric>已用 / 请求数</TableHeaderCell>
                <TableHeaderCell>注册时间</TableHeaderCell>
                <TableHeaderCell>最后登录</TableHeaderCell>
                <TableHeaderCell numeric>操作</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((row) => {
                const isSelf = operator?.id === row.id
                const enabled = row.status === 1
                const busy = manage.isPending && manage.variables?.user.id === row.id
                return (
                  <TableRow key={row.id}>
                    <TableCell>
                      <div className="flex flex-col">
                        <span className="font-medium text-ink">{row.username}</span>
                        <span className="text-caption text-ink-3">
                          #{row.id}
                          {row.display_name ? ` · ${row.display_name}` : ''}
                        </span>
                        {row.email ? (
                          <span className="max-w-[150px] truncate text-caption text-ink-4">
                            {row.email}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Tag
                        tone={row.role >= 10 ? 'brand' : 'neutral'}
                        className="whitespace-nowrap"
                      >
                        {roleLabel(row.role)}
                      </Tag>
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2 whitespace-nowrap">
                        <Switch
                          checked={enabled}
                          loading={busy}
                          disabled={isSelf || row.role >= 100}
                          onCheckedChange={() => openToggle(row)}
                          aria-label={enabled ? '点击封禁用户' : '点击启用用户'}
                        />
                        <span
                          className={cn(
                            'text-caption font-medium',
                            enabled ? 'text-brand-ink' : 'text-ink-4',
                          )}
                        >
                          {enabled ? '正常' : row.deleted_at ? '已删除' : '已封禁'}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell numeric className="tabular-nums">
                      {formatQuota(row.quota, perUnit)}
                    </TableCell>
                    <TableCell numeric className="tabular-nums text-ink-3 whitespace-nowrap">
                      {formatQuota(row.used_quota, perUnit)} / {row.request_count}
                    </TableCell>
                    <TableCell
                      className="whitespace-nowrap text-ink-3"
                      title={row.created_at ? formatDate(row.created_at) : undefined}
                    >
                      {row.created_at ? formatMonthDay(row.created_at) : '—'}
                    </TableCell>
                    <TableCell
                      className="whitespace-nowrap text-ink-3"
                      title={row.last_login_at ? formatDate(row.last_login_at) : undefined}
                    >
                      {row.last_login_at ? formatMonthDay(row.last_login_at) : '—'}
                    </TableCell>
                    <TableCell numeric>
                      <div className="flex items-center justify-end gap-1 whitespace-nowrap">
                        <Button
                          variant="secondary"
                          size="sm"
                          className="px-2"
                          title="调整余额"
                          onClick={() => openQuota(row)}
                        >
                          余额
                        </Button>
                        {isRoot && row.role < 100 ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="px-2"
                            title={row.role >= 10 ? '取消管理员' : '设为管理员'}
                            loading={manage.isPending && manage.variables?.user.id === row.id}
                            onClick={() =>
                              manage.mutate({
                                user: row,
                                action: row.role >= 10 ? 'demote' : 'promote',
                              })
                            }
                          >
                            {row.role >= 10 ? '取消管理' : '设为管理'}
                          </Button>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          <div className="flex justify-end pt-2">
            <Pagination
              compact
              showSummary
              page={page}
              pageSize={ADMIN_USERS_PAGE_SIZE}
              total={total}
              onPageChange={setPage}
              ariaLabel="用户分页"
            />
          </div>
        </>
      )}

      <QuotaDialog
        draft={draft}
        perUnit={perUnit}
        loading={adjust.isPending}
        onClose={() => setDraft(null)}
        onChange={(patch) => setDraft((current) => (current ? { ...current, ...patch } : current))}
        onConfirm={(quota) => {
          if (!draft) return
          adjust.mutate({ user: draft.user, mode: draft.mode, quota })
        }}
      />

      <ConfirmDialog
        open={pendingAction !== null}
        title={
          pendingAction
            ? pendingAction.action === 'disable'
              ? `确定封禁用户 ${pendingAction.user.username}？`
              : `确定启用用户 ${pendingAction.user.username}？`
            : ''
        }
        description={
          pendingAction?.action === 'disable'
            ? '封禁后该用户将无法登录和调用接口，已签发的会话会被拒绝。'
            : '启用后该用户可以正常登录和使用服务。'
        }
        confirmLabel={pendingAction?.action === 'disable' ? '确认封禁' : '确认启用'}
        tone={pendingAction?.action === 'disable' ? 'danger' : 'primary'}
        loading={manage.isPending}
        onConfirm={() => {
          if (pendingAction) manage.mutate(pendingAction)
        }}
        onClose={() => setPendingAction(null)}
      />
    </div>
  )
}

function QuotaDialog({
  draft,
  perUnit,
  loading,
  onClose,
  onChange,
  onConfirm,
}: {
  draft: QuotaDraft | null
  perUnit: number
  loading: boolean
  onClose: () => void
  onChange: (patch: Partial<Pick<QuotaDraft, 'mode' | 'amount'>>) => void
  onConfirm: (quota: number) => void
}) {
  const toast = useToast()
  const mode: QuotaAdjustMode = draft?.mode ?? 'add'
  const amount = draft?.amount ?? ''
  const quota = draft ? yuanToQuota(amount, perUnit) : null
  const before = draft?.user.quota ?? 0
  const after =
    draft && quota !== null
      ? mode === 'add'
        ? before + quota
        : mode === 'subtract'
          ? Math.max(0, before - quota)
          : quota
      : null

  function confirm() {
    if (!draft) return
    if (quota === null) {
      toast.show({ title: '请输入大于 0 的金额', tone: 'error' })
      return
    }
    onConfirm(quota)
  }

  return (
    <Dialog
      open={draft !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      title={draft ? `调整余额：${draft.user.username}` : '调整余额'}
      description="金额以人民币计，提交后按额度单位换算写入。"
      footer={
        <>
          <Button variant="secondary" disabled={loading} onClick={onClose}>
            取消
          </Button>
          <Button loading={loading} onClick={confirm}>
            确认调整
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Segmented
          options={[
            { value: 'add', label: '增加' },
            { value: 'subtract', label: '扣减' },
            { value: 'override', label: '设为' },
          ]}
          value={mode}
          onChange={(value) => onChange({ mode: value as QuotaAdjustMode })}
          size="sm"
          ariaLabel="调整方式"
        />
        <Field label="金额（元）" htmlFor="quota-amount" hint="例如 10 表示 10 元">
          <TextInput
            id="quota-amount"
            inputMode="decimal"
            placeholder="0.00"
            value={amount}
            disabled={draft === null}
            onChange={(e) => onChange({ amount: e.target.value })}
          />
        </Field>
        {draft ? (
          <div className="rounded-md border border-line bg-surface/60 px-4 py-3 text-meta text-ink-2">
            <div className="flex justify-between">
              <span>调整前余额</span>
              <span className="tabular-nums">{formatQuota(before, perUnit)}</span>
            </div>
            <div className="mt-1 flex justify-between">
              <span>调整后余额</span>
              <span className="tabular-nums font-medium text-ink">
                {after === null ? '—' : formatQuota(after, perUnit)}
              </span>
            </div>
          </div>
        ) : null}
      </div>
    </Dialog>
  )
}

const TOPUP_STATUS_LABELS: Record<string, string> = {
  pending: '待支付',
  success: '已完成',
  failed: '失败',
  expired: '已过期',
}

function topUpStatusLabel(status: string): string {
  return TOPUP_STATUS_LABELS[status] ?? status
}

function TopUpsPanel({ userNames }: { userNames: Map<number, string> }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [status, setStatusFilter] = useState('all')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [completeTarget, setCompleteTarget] = useState<AdminTopUp | null>(null)

  const topups = useQuery(adminTopUpsQuery(page, 20, search))

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['admin', 'topups'] })
  }

  const complete = useMutation({
    mutationFn: (tradeNo: string) => completeAdminTopUp(tradeNo),
    onSuccess: () => {
      refresh()
      toast.show({ title: '补单成功', tone: 'success' })
      setCompleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const all = topups.data?.items ?? []
  const rows = status === 'all' ? all : all.filter((item) => item.status === status)
  const total = topups.data?.total ?? 0

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-2">
          <div className="w-full sm:w-64">
            <TextInput
              placeholder="按订单号搜索..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  setSearch(keyword.trim())
                  setPage(1)
                }
              }}
            />
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setSearch(keyword.trim())
              setPage(1)
            }}
          >
            搜索
          </Button>
          <Segmented
            options={[
              { value: 'all', label: '全部' },
              { value: 'pending', label: '待支付' },
              { value: 'success', label: '已完成' },
              { value: 'failed', label: '失败' },
            ]}
            value={status}
            onChange={(value) => setStatusFilter(value)}
            size="sm"
            ariaLabel="充值状态筛选"
          />
        </div>
        <div className="text-meta text-ink-3">
          共 <strong className="font-medium text-ink">{total}</strong> 笔订单
        </div>
      </div>

      {topups.isPending ? (
        <TableSkeleton rows={8} columns={7} />
      ) : topups.isError ? (
        <ErrorBlock error={topups.error} onRetry={refresh} />
      ) : rows.length === 0 ? (
        <Empty
          title="暂无充值记录"
          reason={status === 'all' ? '当前还没有充值订单' : '没有匹配筛选条件的订单'}
        />
      ) : (
        <>
          <Table minWidth={900}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>订单号</TableHeaderCell>
                <TableHeaderCell>用户</TableHeaderCell>
                <TableHeaderCell numeric>金额</TableHeaderCell>
                <TableHeaderCell>支付方式</TableHeaderCell>
                <TableHeaderCell>状态</TableHeaderCell>
                <TableHeaderCell>时间</TableHeaderCell>
                <TableHeaderCell numeric>操作</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className="font-mono text-caption text-ink-2">{row.trade_no}</TableCell>
                  <TableCell>
                    <div className="flex flex-col">
                      <span className="text-ink">{userNames.get(row.user_id) ?? `#${row.user_id}`}</span>
                      <span className="text-caption text-ink-4">ID {row.user_id}</span>
                    </div>
                  </TableCell>
                  <TableCell numeric className="tabular-nums">
                    ¥{row.money.toFixed(2)}
                  </TableCell>
                  <TableCell className="text-ink-2">{row.payment_method || '—'}</TableCell>
                  <TableCell>
                    <Tag tone={row.status === 'success' ? 'brand' : 'neutral'} dot>
                      {topUpStatusLabel(row.status)}
                    </Tag>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-ink-3">
                    {row.create_time ? formatDateTime(row.create_time) : '—'}
                  </TableCell>
                  <TableCell numeric>
                    <div className="flex justify-end">
                      {row.status === 'pending' ? (
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => setCompleteTarget(row)}
                        >
                          补单
                        </Button>
                      ) : (
                        <span className="text-ink-4">—</span>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <div className="flex justify-end pt-2">
            <Pagination
              compact
              showSummary
              page={page}
              pageSize={20}
              total={total}
              onPageChange={setPage}
              ariaLabel="充值记录分页"
            />
          </div>
        </>
      )}

      <ConfirmDialog
        open={completeTarget !== null}
        title={completeTarget ? `确定补单 ${completeTarget.trade_no}？` : ''}
        description={
          completeTarget
            ? `将把该待支付订单标记为已支付，并为用户入账 ¥${completeTarget.money.toFixed(2)}。请先确认款项已到账。`
            : ''
        }
        confirmLabel="确认补单"
        tone="primary"
        loading={complete.isPending}
        onConfirm={() => {
          if (completeTarget) complete.mutate(completeTarget.trade_no)
        }}
        onClose={() => setCompleteTarget(null)}
      />
    </div>
  )
}

export function UsersPage() {
  const [tab, setTab] = useState<Tab>('users')

  const status = useQuery({
    queryKey: ['admin', 'users', 'quota-per-unit'],
    queryFn: fetchQuotaPerUnit,
    staleTime: 300_000,
  })

  const nameLookup = useQuery(adminUsersQuery({ page: 1, page_size: 100 }))

  const names = useMemo(() => {
    const map = new Map<number, string>()
    for (const row of nameLookup.data?.items ?? []) {
      map.set(row.id, row.display_name || row.username)
    }
    return map
  }, [nameLookup.data])

  const perUnit = status.data ?? 1_000_000

  return (
    <>
      <PageHeader
        title="用户与充值"
        lead="管理站点用户的余额、账号状态与角色，并核对全平台充值订单。所有金额按人民币展示（1,000,000 额度 = ¥1）。"
      />

      <div className="mt-6 space-y-6">
        <div className="flex flex-wrap items-center gap-1.5 border-b border-line pb-3">
          {(
            [
              { value: 'users', label: '用户' },
              { value: 'topups', label: '充值记录' },
            ] as const
          ).map((item) => {
            const active = tab === item.value
            return (
              <button
                key={item.value}
                type="button"
                aria-pressed={active}
                onClick={() => setTab(item.value)}
                className={cn(
                  'inline-flex h-9 items-center rounded-full px-3.5 text-body font-medium transition-colors',
                  active
                    ? 'bg-brand-soft text-brand-ink'
                    : 'text-ink-2 hover:bg-brand/[0.06] hover:text-ink',
                )}
              >
                {item.label}
              </button>
            )
          })}
        </div>

        {tab === 'users' ? (
          <UsersPanel perUnit={perUnit} />
        ) : (
          <TopUpsPanel userNames={names} />
        )}
      </div>
    </>
  )
}
