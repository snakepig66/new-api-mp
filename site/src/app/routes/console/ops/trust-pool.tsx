/**
 * 运营后台 · 认证池（P5）。
 *
 * 认证池决定一个新上游地址是可信任、需复审还是直接拒绝。页面只编辑运营
 * 可维护的字段；official 只能来自代码种子，source 不可改，种子记录不可
 * 删除。后端对同一组规则再做一次校验，前端校验只是更早的提示。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { useAuth } from '@/lib/auth'
import { formatDateTime, parseApiTime } from '@/lib/time'
import {
  Button,
  Dialog,
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
  type TagTone,
} from '@/ui'

import {
  TRUST_POOL_ENDPOINTS,
  adminPoolChannelsQuery,
  approveTrustPoolChange,
  createTrustPool,
  deleteTrustPool,
  rejectTrustPoolChange,
  trustPoolApprovalsQuery,
  trustPoolDetailQuery,
  trustPoolQuery,
  updateTrustPool,
  type AdminTrustPool,
  type AdminTrustPoolApproval,
  type TrustPoolEndpoint,
  type TrustPoolInput,
  type TrustPoolKind,
  type TrustPoolSource,
  type TrustPoolTrust,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  FilterTabs,
  Notice,
  Section,
  Select,
  TableSkeleton,
  TextArea,
  TextInput,
  type FilterTabOption,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import {
  shanghaiDateTimeToRfc3339,
  toShanghaiInputValue,
} from '@/features/admin/schemas'

export const Route = createFileRoute('/console/ops/trust-pool')({
  component: TrustPoolPage,
})

const page = copy.pages.adminTrustPool
const common = copy.pages.adminCommon
const PAGE_SIZE = 50

const KINDS: readonly TrustPoolKind[] = ['official', 'third_party', 'plan']
const TRUSTS: readonly TrustPoolTrust[] = ['trusted', 'watch', 'blocked']
const SOURCES: readonly TrustPoolSource[] = ['seed', 'manual', 'probe']

type TrustPoolTab = 'pool' | 'pending'
const TRUST_POOL_TABS: readonly FilterTabOption<TrustPoolTab>[] = [
  { value: 'pool', label: page.tabs.pool },
  { value: 'pending', label: page.tabs.pending },
]

function approvalActionLabel(action: string): string {
  return (
    (page.approvals.action as Record<string, string>)[action] ?? action
  )
}

function approvalTarget(row: AdminTrustPoolApproval): string {
  return row.target || `#${row.trust_pool_id}`
}

function asKind(value: string): TrustPoolKind {
  return (KINDS as readonly string[]).includes(value) ? (value as TrustPoolKind) : 'third_party'
}

function asTrust(value: string): TrustPoolTrust {
  return (TRUSTS as readonly string[]).includes(value) ? (value as TrustPoolTrust) : 'watch'
}

function asEndpoint(value: string): TrustPoolEndpoint | null {
  return (TRUST_POOL_ENDPOINTS as readonly string[]).includes(value)
    ? (value as TrustPoolEndpoint)
    : null
}

function kindLabel(kind: string): string {
  if (kind === 'official') return page.kind.official
  if (kind === 'plan') return page.kind.plan
  return page.kind.thirdParty
}

function trustLabel(trust: string): string {
  if (trust === 'trusted') return page.trust.trusted
  if (trust === 'blocked') return page.trust.blocked
  return page.trust.watch
}

function sourceLabel(source: string): string {
  if (source === 'seed') return page.source.seed
  if (source === 'probe') return page.source.probe
  return page.source.manual
}

function endpointLabel(endpoint: string): string {
  return (page.endpoints as Record<string, string>)[endpoint] ?? endpoint
}

function targetLabel(row: AdminTrustPool): string {
  return row.host || row.host_pattern || `#${row.id}`
}

function splitLines(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
}

function expiresInput(value: string | null): string {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : toShanghaiInputValue(date)
}

function isExpired(value: string | null): boolean {
  const date = parseApiTime(value ?? '')
  return date !== null && date.getTime() <= Date.now()
}

function KindTag({ kind }: { kind: string }) {
  return kind === 'official' ? (
    <Tag tone="brand">{kindLabel(kind)}</Tag>
  ) : (
    <Tag>{kindLabel(kind)}</Tag>
  )
}

function TrustTag({ trust }: { trust: string }) {
  const tone: TagTone =
    trust === 'trusted' ? 'brand' : trust === 'blocked' ? 'danger' : 'neutral'
  return (
    <Tag tone={tone} dot>
      {trustLabel(trust)}
    </Tag>
  )
}

function SourceTag({ source }: { source: string }) {
  return source === 'seed' ? <Tag tone="brand">{page.seedTag}</Tag> : <Tag>{sourceLabel(source)}</Tag>
}

interface TrustPoolForm {
  host: string
  host_pattern: string
  path_prefix: string
  key_prefix: string
  vendor_slug: string
  kind: TrustPoolKind
  trust: TrustPoolTrust
  endpoints: TrustPoolEndpoint[]
  expires_at: string
  deny_key_prefixes: string
  deny_paths: string
  notes: string
}

const emptyForm: TrustPoolForm = {
  host: '',
  host_pattern: '',
  path_prefix: '',
  key_prefix: '',
  vendor_slug: '',
  kind: 'third_party',
  trust: 'watch',
  endpoints: ['openai_chat'],
  expires_at: '',
  deny_key_prefixes: '',
  deny_paths: '',
  notes: '',
}

function formFromRow(row: AdminTrustPool): TrustPoolForm {
  return {
    host: row.host,
    host_pattern: row.host_pattern,
    path_prefix: row.path_prefix,
    key_prefix: row.key_prefix,
    vendor_slug: row.vendor_slug,
    kind: asKind(row.kind),
    trust: asTrust(row.trust),
    endpoints: row.endpoints
      .map(asEndpoint)
      .filter((endpoint): endpoint is TrustPoolEndpoint => endpoint !== null),
    expires_at: expiresInput(row.expires_at),
    deny_key_prefixes: row.deny_key_prefixes.join('\n'),
    deny_paths: row.deny_paths.join('\n'),
    notes: row.notes,
  }
}

export function TrustPoolPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const { user } = useAuth()
  const selfId = user?.id ?? 0
  const channels = useQuery(adminPoolChannelsQuery())

  const [tab, setTab] = useState<TrustPoolTab>('pool')
  const [trustFilter, setTrustFilter] = useState('')
  const [kindFilter, setKindFilter] = useState('')
  const [sourceFilter, setSourceFilter] = useState('')
  const [pageIndex, setPageIndex] = useState(1)

  const rows = useQuery(
    trustPoolQuery({
      trust: trustFilter || undefined,
      kind: kindFilter || undefined,
      source: sourceFilter || undefined,
      page: pageIndex,
      page_size: PAGE_SIZE,
    }),
  )
  const approvals = useQuery(trustPoolApprovalsQuery('pending', tab === 'pending'))

  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<AdminTrustPool | null>(null)
  const [form, setForm] = useState<TrustPoolForm>(emptyForm)
  const [errors, setErrors] = useState<Partial<Record<keyof TrustPoolForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminTrustPool | null>(null)
  const [detailID, setDetailID] = useState<number | null>(null)

  const fallbackTotal = rows.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(fallbackTotal / PAGE_SIZE))
  const source = editing?.source ?? 'manual'
  const officialLocked = form.kind === 'official' && source !== 'seed'

  const vendorOptions = useMemo(() => {
    const list = (channels.data ?? []).map((channel) => ({
      slug: channel.slug,
      label: channel.name ? `${channel.name}（${channel.slug}）` : channel.slug,
    }))
    if (form.vendor_slug && !list.some((option) => option.slug === form.vendor_slug)) {
      list.unshift({ slug: form.vendor_slug, label: form.vendor_slug })
    }
    return list
  }, [channels.data, form.vendor_slug])

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'trust-pool'] })
  }

  const save = useMutation({
    mutationFn: (vars: { id: number | null; input: TrustPoolInput }) =>
      vars.id ? updateTrustPool(vars.id, vars.input) : createTrustPool(vars.input),
    onSuccess: (_row, vars) => {
      refresh()
      toast.show({
        title: vars.id ? page.success.update : page.success.create,
        tone: 'success',
      })
      closeForm()
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  const remove = useMutation({
    mutationFn: (id: number) => deleteTrustPool(id),
    onSuccess: () => {
      refresh()
      toast.show({ title: page.success.delete, tone: 'success' })
      setDeleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const approve = useMutation({
    mutationFn: (id: number) => approveTrustPoolChange(id),
    onSuccess: () => {
      refresh()
      toast.show({ title: page.approvals.successApprove, tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const reject = useMutation({
    mutationFn: (id: number) => rejectTrustPoolChange(id),
    onSuccess: () => {
      refresh()
      toast.show({ title: page.approvals.successReject, tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function closeForm() {
    setFormOpen(false)
    setEditing(null)
    setForm(emptyForm)
    setErrors({})
    setSubmitError(null)
  }

  function openCreate() {
    setEditing(null)
    setForm(emptyForm)
    setErrors({})
    setSubmitError(null)
    setFormOpen(true)
  }

  function openEdit(row: AdminTrustPool) {
    setEditing(row)
    setForm(formFromRow(row))
    setErrors({})
    setSubmitError(null)
    setFormOpen(true)
  }

  function update<K extends keyof TrustPoolForm>(key: K, value: TrustPoolForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function toggleEndpoint(endpoint: TrustPoolEndpoint) {
    update(
      'endpoints',
      form.endpoints.includes(endpoint)
        ? form.endpoints.filter((item) => item !== endpoint)
        : [...form.endpoints, endpoint],
    )
  }

  function changeFilter(setter: (value: string) => void, value: string) {
    setter(value)
    setPageIndex(1)
  }

  function submit() {
    const next: Partial<Record<keyof TrustPoolForm, string>> = {}
    const host = form.host.trim()
    const hostPattern = form.host_pattern.trim()
    if (!host && !hostPattern) {
      next.host = page.errors.hostRequired
    } else if (host && hostPattern) {
      next.host = page.errors.hostAndPattern
      next.host_pattern = page.errors.hostAndPattern
    }
    if (host && /[/\\?#@\s]/.test(host)) next.host = page.errors.hostInvalid
    if (hostPattern) {
      try {
        new RegExp(`^(?:${hostPattern})$`)
      } catch {
        next.host_pattern = page.errors.patternInvalid
      }
    }
    if (form.trust === 'trusted' && !form.expires_at.trim()) {
      next.expires_at = page.errors.trustedExpiry
    }
    if (form.trust === 'blocked' && !form.notes.trim()) {
      next.notes = page.errors.blockedNotes
    }
    if (officialLocked) next.kind = page.errors.officialSeed
    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }
    let expiresAt = ''
    if (form.expires_at.trim()) {
      const parsed = shanghaiDateTimeToRfc3339(form.expires_at.trim())
      if (!parsed) {
        setErrors({ expires_at: page.errors.expiresInvalid })
        return
      }
      expiresAt = parsed
    }
    setErrors({})
    save.mutate({
      id: editing?.id ?? null,
      input: {
        host,
        host_pattern: hostPattern,
        path_prefix: form.path_prefix.trim(),
        key_prefix: form.key_prefix.trim(),
        vendor_slug: form.vendor_slug.trim(),
        kind: form.kind,
        trust: form.trust,
        endpoints: form.endpoints,
        expires_at: expiresAt,
        deny_key_prefixes: splitLines(form.deny_key_prefixes),
        deny_paths: splitLines(form.deny_paths),
        notes: form.notes.trim(),
      },
    })
  }

  const list = rows.data?.items ?? []
  const approvalList = approvals.data ?? []

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Button onClick={openCreate}>{page.actions.create}</Button>
        }
      />

      <FilterTabs
        options={TRUST_POOL_TABS}
        value={tab}
        onChange={setTab}
        ariaLabel={page.tabsAria}
      />

      {tab === 'pool' ? (
      <div className="mt-6">
        <Section
          title={page.listTitle}
          hint={page.count(fallbackTotal)}
          action={
            <span className="text-meta text-ink-3">{page.pageOf(pageIndex, totalPages)}</span>
          }
        >
          <div className="mb-5 grid gap-3 sm:grid-cols-3">
            <Field label={page.filters.trust} htmlFor="trust-pool-filter-trust">
              <Select
                id="trust-pool-filter-trust"
                value={trustFilter}
                aria-label={page.filters.aria}
                onChange={(event) => changeFilter(setTrustFilter, event.target.value)}
              >
                <option value="">{page.filters.all}</option>
                {TRUSTS.map((trust) => (
                  <option key={trust} value={trust}>
                    {trustLabel(trust)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={page.filters.kind} htmlFor="trust-pool-filter-kind">
              <Select
                id="trust-pool-filter-kind"
                value={kindFilter}
                onChange={(event) => changeFilter(setKindFilter, event.target.value)}
              >
                <option value="">{page.filters.all}</option>
                {KINDS.map((kind) => (
                  <option key={kind} value={kind}>
                    {kindLabel(kind)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={page.filters.source} htmlFor="trust-pool-filter-source">
              <Select
                id="trust-pool-filter-source"
                value={sourceFilter}
                onChange={(event) => changeFilter(setSourceFilter, event.target.value)}
              >
                <option value="">{page.filters.all}</option>
                {SOURCES.map((value) => (
                  <option key={value} value={value}>
                    {sourceLabel(value)}
                  </option>
                ))}
              </Select>
            </Field>
          </div>

          {rows.isPending ? (
            <TableSkeleton rows={6} columns={9} />
          ) : rows.isError ? (
            <ErrorBlock error={rows.error} onRetry={() => void rows.refetch()} />
          ) : list.length === 0 ? (
            <Empty title={page.empty.title} reason={page.empty.reason} next={page.empty.next} />
          ) : (
            <Table minWidth={1080}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{page.columns.target}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.kind}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.trust}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.endpoints}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.expiresAt}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.source}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.createdAt}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.notes}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.actions}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {list.map((row) => {
                  const expired = row.trust === 'trusted' && isExpired(row.expires_at)
                  return (
                    <TableRow key={row.id}>
                      <TableCell className="font-mono text-meta">
                        <div className="max-w-[260px] truncate" title={targetLabel(row)}>
                          {targetLabel(row)}
                        </div>
                        {row.host_pattern ? (
                          <div className="mt-1 text-caption text-ink-3">
                            {page.form.hostPattern}
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        <KindTag kind={row.kind} />
                      </TableCell>
                      <TableCell>
                        <TrustTag trust={row.trust} />
                      </TableCell>
                      <TableCell>
                        <div className="flex max-w-[220px] flex-wrap gap-1">
                          {row.endpoints.length === 0
                            ? common.none
                            : row.endpoints.map((endpoint) => (
                                <Tag key={endpoint}>{endpointLabel(endpoint)}</Tag>
                              ))}
                        </div>
                      </TableCell>
                      <TableCell className={expired ? 'text-danger' : 'text-ink-2'}>
                        {row.expires_at ? formatDateTime(row.expires_at) : common.none}
                      </TableCell>
                      <TableCell>
                        <SourceTag source={row.source} />
                      </TableCell>
                      <TableCell className="text-meta text-ink-3">
                        {formatDateTime(row.created_at)}
                      </TableCell>
                      <TableCell className="max-w-[220px] truncate text-meta text-ink-3">
                        {row.notes || common.none}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-2">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setDetailID(row.id)}
                          >
                            {page.actions.details}
                          </Button>
                          <Button variant="secondary" size="sm" onClick={() => openEdit(row)}>
                            {page.actions.edit}
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            disabled={row.source === 'seed'}
                            title={row.source === 'seed' ? page.seedLocked : undefined}
                            onClick={() => setDeleteTarget(row)}
                          >
                            {page.actions.delete}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}

          <div className="mt-4 flex items-center justify-end gap-2">
            <Button
              variant="secondary"
              size="sm"
              disabled={pageIndex <= 1}
              onClick={() => setPageIndex((current) => Math.max(1, current - 1))}
            >
              {page.actions.prev}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={pageIndex >= totalPages}
              onClick={() => setPageIndex((current) => current + 1)}
            >
              {page.actions.next}
            </Button>
          </div>
        </Section>
      </div>
      ) : (
        <div className="mt-6">
          <Section
            title={page.approvals.title}
            hint={page.approvals.hint(approvalList.length)}
          >
            {approvals.isPending ? (
              <TableSkeleton rows={4} columns={6} />
            ) : approvals.isError ? (
              <ErrorBlock error={approvals.error} onRetry={() => void approvals.refetch()} />
            ) : approvalList.length === 0 ? (
              <Empty
                title={page.approvals.emptyTitle}
                reason={page.approvals.emptyReason}
                next={page.approvals.emptyNext}
              />
            ) : (
              <Table minWidth={860}>
                <TableHead>
                  <TableRow interactive={false}>
                    <TableHeaderCell>{page.approvals.columns.target}</TableHeaderCell>
                    <TableHeaderCell>{page.approvals.columns.action}</TableHeaderCell>
                    <TableHeaderCell>{page.approvals.columns.submitter}</TableHeaderCell>
                    <TableHeaderCell>{page.approvals.columns.createdAt}</TableHeaderCell>
                    <TableHeaderCell>{page.approvals.columns.notes}</TableHeaderCell>
                    <TableHeaderCell numeric>
                      {page.approvals.columns.actions}
                    </TableHeaderCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {approvalList.map((row) => {
                    const isSelf = row.submitter_id === selfId
                    const approving = approve.isPending && approve.variables === row.id
                    const rejecting = reject.isPending && reject.variables === row.id
                    const busy = approving || rejecting
                    return (
                      <TableRow key={row.id}>
                        <TableCell className="font-mono text-meta">
                          <div className="max-w-[260px] truncate" title={approvalTarget(row)}>
                            {approvalTarget(row)}
                          </div>
                        </TableCell>
                        <TableCell>
                          <Tag>{approvalActionLabel(row.action)}</Tag>
                        </TableCell>
                        <TableCell className="text-meta text-ink-2">
                          {page.approvals.submitter(row.submitter_id)}
                        </TableCell>
                        <TableCell className="text-meta text-ink-3">
                          {formatDateTime(row.created_at)}
                        </TableCell>
                        <TableCell className="max-w-[220px] truncate text-meta text-ink-3">
                          {row.notes || common.none}
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex justify-end gap-2">
                            <Button
                              size="sm"
                              loading={approving}
                              disabled={isSelf || busy}
                              title={isSelf ? page.approvals.selfReview : undefined}
                              onClick={() => approve.mutate(row.id)}
                            >
                              {page.approvals.approve}
                            </Button>
                            <Button
                              variant="danger"
                              size="sm"
                              loading={rejecting}
                              disabled={isSelf || busy}
                              title={isSelf ? page.approvals.selfReview : undefined}
                              onClick={() => reject.mutate(row.id)}
                            >
                              {page.approvals.reject}
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            )}
          </Section>
        </div>
      )}

      <Dialog
        open={formOpen}
        onOpenChange={(next) => {
          if (!next) closeForm()
        }}
        title={editing ? page.form.editTitle(editing.id) : page.form.createTitle}
        description={page.form.lead}
        className="max-h-[88vh] max-w-3xl overflow-y-auto"
        footer={
          <>
            <Button variant="secondary" onClick={closeForm} disabled={save.isPending}>
              {page.actions.cancel}
            </Button>
            <Button
              type="submit"
              form="trust-pool-form"
              loading={save.isPending}
              disabled={officialLocked}
            >
              {save.isPending
                ? page.actions.saving
                : editing
                  ? page.actions.save
                  : page.form.submitCreate}
            </Button>
          </>
        }
      >
        <form
          id="trust-pool-form"
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={page.form.host} htmlFor="trust-pool-host" error={errors.host}>
              <TextInput
                id="trust-pool-host"
                value={form.host}
                placeholder={page.form.hostPlaceholder}
                autoComplete="off"
                mono
                invalid={Boolean(errors.host)}
                onChange={(event) => update('host', event.target.value)}
              />
            </Field>
            <Field
              label={page.form.hostPattern}
              htmlFor="trust-pool-host-pattern"
              error={errors.host_pattern}
            >
              <TextInput
                id="trust-pool-host-pattern"
                value={form.host_pattern}
                placeholder={page.form.hostPatternPlaceholder}
                autoComplete="off"
                mono
                invalid={Boolean(errors.host_pattern)}
                onChange={(event) => update('host_pattern', event.target.value)}
              />
            </Field>
            <Field
              label={page.form.pathPrefix}
              htmlFor="trust-pool-path-prefix"
              hint={page.form.addressHint}
            >
              <TextInput
                id="trust-pool-path-prefix"
                value={form.path_prefix}
                placeholder={page.form.pathPrefixPlaceholder}
                autoComplete="off"
                mono
                onChange={(event) => update('path_prefix', event.target.value)}
              />
            </Field>
            <Field label={page.form.keyPrefix} htmlFor="trust-pool-key-prefix">
              <TextInput
                id="trust-pool-key-prefix"
                value={form.key_prefix}
                placeholder={page.form.keyPrefixPlaceholder}
                autoComplete="off"
                mono
                onChange={(event) => update('key_prefix', event.target.value)}
              />
            </Field>
            <Field label={page.form.vendorSlug} htmlFor="trust-pool-vendor">
              <Select
                id="trust-pool-vendor"
                value={form.vendor_slug}
                onChange={(event) => update('vendor_slug', event.target.value)}
              >
                <option value="">{page.form.vendorSlugEmpty}</option>
                {vendorOptions.map((option) => (
                  <option key={option.slug} value={option.slug}>
                    {option.label}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label={page.form.kind}
              htmlFor="trust-pool-kind"
              hint={page.form.kindHint}
              error={errors.kind}
            >
              <Select
                id="trust-pool-kind"
                value={form.kind}
                onChange={(event) => update('kind', event.target.value as TrustPoolKind)}
              >
                <option value="official" disabled={source !== 'seed'}>
                  {kindLabel('official')}
                </option>
                <option value="plan">{kindLabel('plan')}</option>
                <option value="third_party">{kindLabel('third_party')}</option>
              </Select>
            </Field>
            <Field label={page.form.trust} htmlFor="trust-pool-trust">
              <Select
                id="trust-pool-trust"
                value={form.trust}
                onChange={(event) => update('trust', event.target.value as TrustPoolTrust)}
              >
                {TRUSTS.map((trust) => (
                  <option key={trust} value={trust}>
                    {trustLabel(trust)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label={page.form.expiresAt}
              htmlFor="trust-pool-expires"
              hint={page.form.expiresAtHint}
              error={errors.expires_at}
            >
              <TextInput
                id="trust-pool-expires"
                type="datetime-local"
                value={form.expires_at}
                invalid={Boolean(errors.expires_at)}
                onChange={(event) => update('expires_at', event.target.value)}
              />
            </Field>
          </div>

          <div className="mt-4">
            <Field label={page.form.endpoints}>
              <div className="flex flex-wrap gap-4">
                {TRUST_POOL_ENDPOINTS.map((endpoint) => (
                  <label key={endpoint} className="flex items-center gap-2 text-meta text-ink">
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-brand"
                      checked={form.endpoints.includes(endpoint)}
                      onChange={() => toggleEndpoint(endpoint)}
                    />
                    {endpointLabel(endpoint)}
                  </label>
                ))}
              </div>
            </Field>
          </div>

          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            <Field
              label={page.form.denyKeyPrefixes}
              htmlFor="trust-pool-deny-prefixes"
              hint={page.form.lineHint}
            >
              <TextArea
                id="trust-pool-deny-prefixes"
                rows={3}
                value={form.deny_key_prefixes}
                placeholder="sk-sp-"
                onChange={(event) => update('deny_key_prefixes', event.target.value)}
              />
            </Field>
            <Field
              label={page.form.denyPaths}
              htmlFor="trust-pool-deny-paths"
              hint={page.form.lineHint}
            >
              <TextArea
                id="trust-pool-deny-paths"
                rows={3}
                value={form.deny_paths}
                placeholder="/api/coding"
                onChange={(event) => update('deny_paths', event.target.value)}
              />
            </Field>
          </div>

          <div className="mt-4">
            <Field
              label={page.form.notes}
              htmlFor="trust-pool-notes"
              error={errors.notes}
            >
              <TextArea
                id="trust-pool-notes"
                rows={3}
                value={form.notes}
                placeholder={page.form.notesPlaceholder}
                invalid={Boolean(errors.notes)}
                onChange={(event) => update('notes', event.target.value)}
              />
            </Field>
          </div>

          {editing ? (
            <p className="mt-3 text-meta text-ink-3">
              {page.form.source}: {sourceLabel(editing.source)}
            </p>
          ) : null}

          <Notice tone="brand" className="mt-4">
            {page.form.approvalNotice}
          </Notice>

          {officialLocked ? (
            <Notice tone="warning" className="mt-4">
              {page.governance.official}
            </Notice>
          ) : form.trust === 'trusted' && !form.expires_at.trim() ? (
            <Notice tone="warning" className="mt-4">
              {page.governance.trusted}
            </Notice>
          ) : form.trust === 'blocked' && !form.notes.trim() ? (
            <Notice tone="warning" className="mt-4">
              {page.governance.blocked}
            </Notice>
          ) : null}

          {submitError ? (
            <p
              role="alert"
              className="mt-4 border border-danger/25 bg-danger/10 px-3 py-2 text-meta text-danger"
            >
              {submitError}
            </p>
          ) : null}
        </form>
      </Dialog>

      <ConfirmDialog
        open={deleteTarget !== null}
        title={deleteTarget ? page.deleteTitle(targetLabel(deleteTarget)) : ''}
        description={page.deleteLead}
        confirmLabel={page.actions.save}
        loading={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.id)
        }}
        onClose={() => setDeleteTarget(null)}
      />

      <TrustPoolDetail id={detailID} onClose={() => setDetailID(null)} />
    </>
  )
}

function DetailItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-caption uppercase tracking-[0.08em] text-ink-3">{label}</dt>
      <dd className="mt-1 break-words text-body text-ink">{children}</dd>
    </div>
  )
}

function TrustPoolDetail({ id, onClose }: { id: number | null; onClose: () => void }) {
  const detail = useQuery(trustPoolDetailQuery(id ?? 0))
  const row = detail.data
  return (
    <Dialog
      open={id !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={id ? page.detail.title(id) : ''}
      className="max-h-[88vh] max-w-2xl overflow-y-auto"
      footer={
        <Button variant="secondary" onClick={onClose}>
          {copy.actions.close}
        </Button>
      }
    >
      {detail.isPending ? (
        <p className="text-meta text-ink-3">{common.loading}</p>
      ) : detail.isError ? (
        <ErrorBlock error={detail.error} onRetry={() => void detail.refetch()} />
      ) : row ? (
        <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          <DetailItem label={page.detail.match}>{targetLabel(row)}</DetailItem>
          <DetailItem label={page.detail.vendor}>{row.vendor_slug || page.detail.empty}</DetailItem>
          <DetailItem label={page.detail.kind}>
            <KindTag kind={row.kind} />
          </DetailItem>
          <DetailItem label={page.detail.trust}>
            <TrustTag trust={row.trust} />
          </DetailItem>
          <DetailItem label={page.detail.source}>
            <SourceTag source={row.source} />
          </DetailItem>
          <DetailItem label={page.detail.expiresAt}>
            {row.expires_at ? formatDateTime(row.expires_at) : page.detail.empty}
          </DetailItem>
          <DetailItem label={page.detail.pathPrefix}>
            {row.path_prefix || page.detail.empty}
          </DetailItem>
          <DetailItem label={page.detail.keyPrefix}>
            {row.key_prefix || page.detail.empty}
          </DetailItem>
          <DetailItem label={page.detail.endpoints}>
            {row.endpoints.length === 0
              ? page.detail.empty
              : row.endpoints.map(endpointLabel).join('、')}
          </DetailItem>
          <DetailItem label={page.detail.createdAt}>{formatDateTime(row.created_at)}</DetailItem>
          <DetailItem label={page.detail.updatedAt}>{formatDateTime(row.updated_at)}</DetailItem>
          <DetailItem label={page.detail.denyKeyPrefixes}>
            {row.deny_key_prefixes.length === 0
              ? page.detail.empty
              : row.deny_key_prefixes.join('、')}
          </DetailItem>
          <DetailItem label={page.detail.denyPaths}>
            {row.deny_paths.length === 0 ? page.detail.empty : row.deny_paths.join('、')}
          </DetailItem>
          <div className="sm:col-span-2">
            <DetailItem label={page.detail.notes}>
              <span className="whitespace-pre-wrap">{row.notes || page.detail.empty}</span>
            </DetailItem>
          </div>
        </dl>
      ) : null}
    </Dialog>
  )
}
