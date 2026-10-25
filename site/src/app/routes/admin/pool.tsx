import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { formatMultiplier } from '@/lib/multiplier'
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
  adminPoolChannelModelsQuery,
  adminPoolChannelsQuery,
  adminPoolModelsQuery,
  deleteAdminPoolChannel,
  deleteAdminPoolChannelModel,
  deleteAdminPoolModel,
  putAdminPoolChannel,
  putAdminPoolChannelModel,
  putAdminPoolModel,
  type AdminPoolChannel,
  type AdminPoolChannelModel,
  type AdminPoolModel,
  type PoolChannelKind,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  FilterTabs,
  RootOnly,
  Section,
  Select,
  TableSkeleton,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

export const Route = createFileRoute('/admin/pool')({
  component: AdminPoolPage,
})

const page = copy.pages.adminPool
const labels = page.status

type PoolTab = 'models' | 'channels' | 'channelModels'

const TABS = [
  { value: 'models' as const, label: page.tabs.models },
  { value: 'channels' as const, label: page.tabs.channels },
  { value: 'channelModels' as const, label: page.tabs.channelModels },
]

function splitList(value: string): string[] {
  return value
    .split(/[,，\n]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function parseNonNegativeInt(value: string): number | null {
  if (!value.trim()) return 0
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < 0) return null
  return parsed
}

function parsePositiveInt(value: string): number | null {
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed <= 0) return null
  return parsed
}

function parseJSONObject(value: string): Record<string, unknown> | null {
  if (!value.trim()) return {}
  try {
    const parsed: unknown = JSON.parse(value)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    return null
  }
  return null
}

function statusLabel(status: string): string {
  return (labels as Record<string, string>)[status] ?? labels.unknown
}

function kindLabel(kind: string): string {
  if (kind === 'official') return page.kinds.official
  if (kind === 'plan') return page.kinds.plan
  return page.kinds.thirdParty
}

function KindTag({ kind }: { kind: string }) {
  return kind === 'official' ? (
    <Tag tone="brand">{kindLabel(kind)}</Tag>
  ) : (
    <Tag>{kindLabel(kind)}</Tag>
  )
}

function StatusTag({ status }: { status: string }) {
  return (
    <Tag tone={status === 'active' ? 'brand' : 'neutral'} dot={status === 'active'}>
      {statusLabel(status)}
    </Tag>
  )
}

function AdminPoolPage() {
  return (
    <RootOnly>
      <PoolContent />
    </RootOnly>
  )
}

function PoolContent() {
  const [tab, setTab] = useState<PoolTab>('models')
  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />
      <div className="mt-6">
        <FilterTabs
          options={TABS}
          value={tab}
          onChange={setTab}
          ariaLabel={page.tabsAria}
        />
        {tab === 'models' ? (
          <ModelsTab />
        ) : tab === 'channels' ? (
          <ChannelsTab />
        ) : (
          <ChannelModelsTab />
        )}
      </div>
    </>
  )
}

// ---------------------------------------------------------------------------
// 标准模型
// ---------------------------------------------------------------------------

interface ModelForm {
  name: string
  origin_vendor: string
  context_length: string
  capabilities: string
  aliases: string
  verify_prompt: string
  status: string
}

const emptyModelForm: ModelForm = {
  name: '',
  origin_vendor: '',
  context_length: '',
  capabilities: '',
  aliases: '',
  verify_prompt: '',
  status: 'active',
}

function modelToForm(row: AdminPoolModel): ModelForm {
  return {
    name: row.name,
    origin_vendor: row.origin_vendor,
    context_length: row.context_length ? String(row.context_length) : '',
    capabilities: row.capabilities.join(', '),
    aliases: row.aliases.join(', '),
    verify_prompt: row.verify_prompt,
    status: row.status || 'active',
  }
}

function ModelsTab() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const models = useQuery(adminPoolModelsQuery())
  const [form, setForm] = useState<ModelForm>(emptyModelForm)
  const [editing, setEditing] = useState<string | null>(null)
  const [errors, setErrors] = useState<Partial<Record<keyof ModelForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminPoolModel | null>(null)

  const rows = models.data ?? []
  const copyModels = page.models

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: adminPoolModelsQuery().queryKey })
  }

  const save = useMutation({
    mutationFn: putAdminPoolModel,
    onSuccess: () => {
      refresh()
      toast.show({ title: copyModels.success, tone: 'success' })
      setForm(emptyModelForm)
      setEditing(null)
      setErrors({})
      setSubmitError(null)
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  const remove = useMutation({
    mutationFn: (name: string) => deleteAdminPoolModel(name),
    onSuccess: () => {
      refresh()
      toast.show({ title: copyModels.deleteSuccess, tone: 'success' })
      setDeleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function update<K extends keyof ModelForm>(key: K, value: ModelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function submit() {
    const next: Partial<Record<keyof ModelForm, string>> = {}
    const name = form.name.trim()
    const origin = form.origin_vendor.trim()
    const context = parseNonNegativeInt(form.context_length)
    if (!name) next.name = copyModels.errors.nameRequired
    else if (name.length > 128) next.name = copyModels.errors.nameTooLong
    if (origin.length > 64) next.origin_vendor = copyModels.errors.originVendorTooLong
    if (context === null) next.context_length = copyModels.errors.contextInvalid
    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }
    setErrors({})
    save.mutate({
      name,
      origin_vendor: origin,
      context_length: context ?? 0,
      capabilities: splitList(form.capabilities),
      aliases: splitList(form.aliases),
      verify_prompt: form.verify_prompt.trim(),
      status: form.status,
    })
  }

  return (
    <div className="space-y-6">
      <Section
        title={editing ? copyModels.formEditTitle(editing) : copyModels.formTitle}
        hint={copyModels.formLead}
        action={
          editing ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setEditing(null)
                setForm(emptyModelForm)
                setErrors({})
                setSubmitError(null)
              }}
            >
              {page.actions.cancelEdit}
            </Button>
          ) : null
        }
      >
        <form
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field label={copyModels.form.name} htmlFor="pool-model-name" error={errors.name}>
              <TextInput
                id="pool-model-name"
                value={form.name}
                placeholder={copyModels.form.namePlaceholder}
                autoComplete="off"
                disabled={editing !== null}
                invalid={Boolean(errors.name)}
                onChange={(event) => update('name', event.target.value)}
              />
            </Field>
            <Field
              label={copyModels.form.originVendor}
              htmlFor="pool-model-origin"
              error={errors.origin_vendor}
            >
              <TextInput
                id="pool-model-origin"
                value={form.origin_vendor}
                placeholder={copyModels.form.originVendorPlaceholder}
                autoComplete="off"
                invalid={Boolean(errors.origin_vendor)}
                onChange={(event) => update('origin_vendor', event.target.value)}
              />
            </Field>
            <Field
              label={copyModels.form.context}
              htmlFor="pool-model-context"
              error={errors.context_length}
            >
              <TextInput
                id="pool-model-context"
                value={form.context_length}
                placeholder={copyModels.form.contextPlaceholder}
                inputMode="numeric"
                invalid={Boolean(errors.context_length)}
                onChange={(event) => update('context_length', event.target.value)}
              />
            </Field>
            <Field label={copyModels.form.capabilities} htmlFor="pool-model-capabilities">
              <TextInput
                id="pool-model-capabilities"
                value={form.capabilities}
                placeholder={copyModels.form.capabilitiesPlaceholder}
                autoComplete="off"
                onChange={(event) => update('capabilities', event.target.value)}
              />
            </Field>
            <Field label={copyModels.form.aliases} htmlFor="pool-model-aliases">
              <TextInput
                id="pool-model-aliases"
                value={form.aliases}
                placeholder={copyModels.form.aliasesPlaceholder}
                autoComplete="off"
                onChange={(event) => update('aliases', event.target.value)}
              />
            </Field>
            <Field label={copyModels.form.verifyPrompt} htmlFor="pool-model-prompt">
              <TextInput
                id="pool-model-prompt"
                value={form.verify_prompt}
                placeholder={copyModels.form.verifyPromptPlaceholder}
                autoComplete="off"
                onChange={(event) => update('verify_prompt', event.target.value)}
              />
            </Field>
            <Field label={copyModels.form.status} htmlFor="pool-model-status">
              <Select
                id="pool-model-status"
                value={form.status}
                onChange={(event) => update('status', event.target.value)}
              >
                <option value="active">{labels.active}</option>
                <option value="retired">{labels.retired}</option>
              </Select>
            </Field>
          </div>
          <div className="mt-4 flex items-center justify-end">
            <Button type="submit" loading={save.isPending}>
              {save.isPending ? page.actions.saving : copyModels.form.submit}
            </Button>
          </div>
          {submitError ? (
            <p
              role="alert"
              className="mt-4 border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] text-danger"
            >
              {submitError}
            </p>
          ) : null}
        </form>
      </Section>

      <Section title={copyModels.listTitle} hint={page.count(rows.length)}>
        {models.isPending ? (
          <TableSkeleton rows={6} columns={6} />
        ) : models.isError ? (
          <ErrorBlock error={models.error} onRetry={() => void models.refetch()} />
        ) : rows.length === 0 ? (
          <Empty
            title={copyModels.empty.title}
            reason={copyModels.empty.reason}
            next={copyModels.empty.next}
          />
        ) : (
          <Table minWidth={900}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>{copyModels.columns.name}</TableHeaderCell>
                <TableHeaderCell>{copyModels.columns.originVendor}</TableHeaderCell>
                <TableHeaderCell numeric>{copyModels.columns.context}</TableHeaderCell>
                <TableHeaderCell>{copyModels.columns.capabilities}</TableHeaderCell>
                <TableHeaderCell>{copyModels.columns.aliases}</TableHeaderCell>
                <TableHeaderCell>{copyModels.columns.status}</TableHeaderCell>
                <TableHeaderCell className="text-right">
                  {copyModels.columns.actions}
                </TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className="font-mono text-[13px]">{row.name}</TableCell>
                  <TableCell className="text-ink-2">{row.origin_vendor || '—'}</TableCell>
                  <TableCell numeric>{row.context_length || '—'}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {row.capabilities.length === 0
                        ? '—'
                        : row.capabilities.map((capability) => (
                            <Tag key={capability}>{capability}</Tag>
                          ))}
                    </div>
                  </TableCell>
                  <TableCell className="text-[13px] text-ink-3">
                    {row.aliases.join(', ') || '—'}
                  </TableCell>
                  <TableCell>
                    <StatusTag status={row.status} />
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => {
                          setEditing(row.name)
                          setForm(modelToForm(row))
                          setErrors({})
                          setSubmitError(null)
                        }}
                      >
                        {page.actions.edit}
                      </Button>
                      <Button variant="danger" size="sm" onClick={() => setDeleteTarget(row)}>
                        {page.actions.delete}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>

      <ConfirmDialog
        open={deleteTarget !== null}
        title={deleteTarget ? copyModels.deleteTitle(deleteTarget.name) : ''}
        description={copyModels.deleteLead}
        confirmLabel={page.actions.delete}
        loading={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.name)
        }}
        onClose={() => setDeleteTarget(null)}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// 渠道
// ---------------------------------------------------------------------------

interface ChannelForm {
  slug: string
  name: string
  kind: PoolChannelKind
  openai: string
  anthropic: string
  allow_host_pattern: string
  key_pattern: string
  deny_paths: string
  deny_key_prefixes: string
  adaptor: string
  strip_v1_prefix: boolean
  passthrough_body: boolean
  quota_window: string
  exhaustion_signature: string
  terms_url: string
  terms_checked_at: string
  status: string
}

const emptyChannelForm: ChannelForm = {
  slug: '',
  name: '',
  kind: 'official',
  openai: '',
  anthropic: '',
  allow_host_pattern: '',
  key_pattern: '',
  deny_paths: '',
  deny_key_prefixes: '',
  adaptor: '',
  strip_v1_prefix: false,
  passthrough_body: true,
  quota_window: '',
  exhaustion_signature: '',
  terms_url: '',
  terms_checked_at: '',
  status: 'active',
}

function channelToForm(row: AdminPoolChannel): ChannelForm {
  return {
    slug: row.slug,
    name: row.name,
    kind: (row.kind as PoolChannelKind) || 'official',
    openai: row.protocols.openai,
    anthropic: row.protocols.anthropic,
    allow_host_pattern: row.allow_host_pattern,
    key_pattern: row.key_pattern,
    deny_paths: row.deny_paths.join(', '),
    deny_key_prefixes: row.deny_key_prefixes.join(', '),
    adaptor: row.adaptor,
    strip_v1_prefix: row.strip_v1_prefix,
    passthrough_body: row.passthrough_body,
    quota_window: Object.keys(row.quota_window).length
      ? JSON.stringify(row.quota_window)
      : '',
    exhaustion_signature: row.exhaustion_signature,
    terms_url: row.terms_url,
    terms_checked_at: row.terms_checked_at ?? '',
    status: row.status || 'active',
  }
}

function ChannelsTab() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const channels = useQuery(adminPoolChannelsQuery())
  const [form, setForm] = useState<ChannelForm>(emptyChannelForm)
  const [editing, setEditing] = useState<string | null>(null)
  const [errors, setErrors] = useState<Partial<Record<keyof ChannelForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminPoolChannel | null>(null)

  const rows = channels.data ?? []
  const copyChannels = page.channels

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: adminPoolChannelsQuery().queryKey })
    void queryClient.invalidateQueries({ queryKey: adminPoolChannelModelsQuery().queryKey })
  }

  const save = useMutation({
    mutationFn: putAdminPoolChannel,
    onSuccess: () => {
      refresh()
      toast.show({ title: copyChannels.success, tone: 'success' })
      setForm(emptyChannelForm)
      setEditing(null)
      setErrors({})
      setSubmitError(null)
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  const remove = useMutation({
    mutationFn: (slug: string) => deleteAdminPoolChannel(slug),
    onSuccess: () => {
      refresh()
      toast.show({ title: copyChannels.deleteSuccess, tone: 'success' })
      setDeleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function update<K extends keyof ChannelForm>(key: K, value: ChannelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function submit() {
    const next: Partial<Record<keyof ChannelForm, string>> = {}
    const slug = form.slug.trim()
    if (!slug) next.slug = copyChannels.errors.slugRequired
    else if (slug.length > 64) next.slug = copyChannels.errors.slugTooLong
    if (form.name.trim().length > 128) next.name = copyChannels.errors.nameTooLong
    const quota = parseJSONObject(form.quota_window)
    if (quota === null) next.quota_window = copyChannels.errors.quotaInvalid
    let keyPattern = form.key_pattern.trim()
    if (keyPattern) {
      try {
        new RegExp(keyPattern)
      } catch {
        next.key_pattern = copyChannels.errors.keyPatternInvalid
      }
    }
    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }
    setErrors({})
    keyPattern = keyPattern || ''
    save.mutate({
      slug,
      name: form.name.trim(),
      kind: form.kind,
      protocols: { openai: form.openai.trim(), anthropic: form.anthropic.trim() },
      allow_host_pattern: form.allow_host_pattern.trim(),
      key_pattern: keyPattern,
      deny_paths: splitList(form.deny_paths),
      deny_key_prefixes: splitList(form.deny_key_prefixes),
      adaptor: form.adaptor.trim(),
      strip_v1_prefix: form.strip_v1_prefix,
      passthrough_body: form.passthrough_body,
      quota_window: quota ?? {},
      exhaustion_signature: form.exhaustion_signature.trim(),
      terms_url: form.terms_url.trim(),
      terms_checked_at: form.terms_checked_at.trim() || null,
      status: form.status,
    })
  }

  return (
    <div className="space-y-6">
      <Section
        title={editing ? copyChannels.formEditTitle(editing) : copyChannels.formTitle}
        hint={copyChannels.formLead}
        action={
          editing ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setEditing(null)
                setForm(emptyChannelForm)
                setErrors({})
                setSubmitError(null)
              }}
            >
              {page.actions.cancelEdit}
            </Button>
          ) : null
        }
      >
        <form
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field label={copyChannels.form.slug} htmlFor="pool-channel-slug" error={errors.slug}>
              <TextInput
                id="pool-channel-slug"
                value={form.slug}
                placeholder={copyChannels.form.slugPlaceholder}
                autoComplete="off"
                disabled={editing !== null}
                invalid={Boolean(errors.slug)}
                onChange={(event) => update('slug', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.name} htmlFor="pool-channel-name" error={errors.name}>
              <TextInput
                id="pool-channel-name"
                value={form.name}
                placeholder={copyChannels.form.namePlaceholder}
                autoComplete="off"
                invalid={Boolean(errors.name)}
                onChange={(event) => update('name', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.kind} htmlFor="pool-channel-kind">
              <Select
                id="pool-channel-kind"
                value={form.kind}
                onChange={(event) => update('kind', event.target.value as PoolChannelKind)}
              >
                <option value="official">{page.kinds.official}</option>
                <option value="plan">{page.kinds.plan}</option>
                <option value="third_party">{page.kinds.thirdParty}</option>
              </Select>
            </Field>
            <Field label={copyChannels.form.openai} htmlFor="pool-channel-openai">
              <TextInput
                id="pool-channel-openai"
                value={form.openai}
                placeholder={copyChannels.form.openaiPlaceholder}
                autoComplete="off"
                onChange={(event) => update('openai', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.anthropic} htmlFor="pool-channel-anthropic">
              <TextInput
                id="pool-channel-anthropic"
                value={form.anthropic}
                placeholder={copyChannels.form.anthropicPlaceholder}
                autoComplete="off"
                onChange={(event) => update('anthropic', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.adaptor} htmlFor="pool-channel-adaptor">
              <TextInput
                id="pool-channel-adaptor"
                value={form.adaptor}
                placeholder={copyChannels.form.adaptorPlaceholder}
                autoComplete="off"
                onChange={(event) => update('adaptor', event.target.value)}
              />
            </Field>
            <Field
              label={copyChannels.form.allowHostPattern}
              htmlFor="pool-channel-host"
            >
              <TextInput
                id="pool-channel-host"
                value={form.allow_host_pattern}
                placeholder={copyChannels.form.allowHostPatternPlaceholder}
                autoComplete="off"
                onChange={(event) => update('allow_host_pattern', event.target.value)}
              />
            </Field>
            <Field
              label={copyChannels.form.keyPattern}
              htmlFor="pool-channel-key"
              error={errors.key_pattern}
            >
              <TextInput
                id="pool-channel-key"
                value={form.key_pattern}
                placeholder={copyChannels.form.keyPatternPlaceholder}
                autoComplete="off"
                invalid={Boolean(errors.key_pattern)}
                onChange={(event) => update('key_pattern', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.denyPaths} htmlFor="pool-channel-deny-paths">
              <TextInput
                id="pool-channel-deny-paths"
                value={form.deny_paths}
                placeholder={copyChannels.form.denyPathsPlaceholder}
                autoComplete="off"
                onChange={(event) => update('deny_paths', event.target.value)}
              />
            </Field>
            <Field
              label={copyChannels.form.denyKeyPrefixes}
              htmlFor="pool-channel-deny-prefixes"
            >
              <TextInput
                id="pool-channel-deny-prefixes"
                value={form.deny_key_prefixes}
                placeholder={copyChannels.form.denyKeyPrefixesPlaceholder}
                autoComplete="off"
                onChange={(event) => update('deny_key_prefixes', event.target.value)}
              />
            </Field>
            <Field
              label={copyChannels.form.quotaWindow}
              htmlFor="pool-channel-quota"
              error={errors.quota_window}
            >
              <TextInput
                id="pool-channel-quota"
                value={form.quota_window}
                placeholder={copyChannels.form.quotaWindowPlaceholder}
                autoComplete="off"
                invalid={Boolean(errors.quota_window)}
                onChange={(event) => update('quota_window', event.target.value)}
              />
            </Field>
            <Field
              label={copyChannels.form.exhaustionSignature}
              htmlFor="pool-channel-signature"
            >
              <TextInput
                id="pool-channel-signature"
                value={form.exhaustion_signature}
                placeholder={copyChannels.form.exhaustionSignaturePlaceholder}
                autoComplete="off"
                onChange={(event) => update('exhaustion_signature', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.termsUrl} htmlFor="pool-channel-terms">
              <TextInput
                id="pool-channel-terms"
                value={form.terms_url}
                placeholder={copyChannels.form.termsUrlPlaceholder}
                autoComplete="off"
                onChange={(event) => update('terms_url', event.target.value)}
              />
            </Field>
            <Field
              label={copyChannels.form.termsCheckedAt}
              htmlFor="pool-channel-terms-at"
            >
              <TextInput
                id="pool-channel-terms-at"
                value={form.terms_checked_at}
                placeholder={copyChannels.form.termsCheckedAtPlaceholder}
                autoComplete="off"
                onChange={(event) => update('terms_checked_at', event.target.value)}
              />
            </Field>
            <Field label={copyChannels.form.status} htmlFor="pool-channel-status">
              <Select
                id="pool-channel-status"
                value={form.status}
                onChange={(event) => update('status', event.target.value)}
              >
                <option value="active">{labels.active}</option>
                <option value="disabled">{labels.disabled}</option>
              </Select>
            </Field>
          </div>

          <div className="mt-4 flex flex-wrap items-center gap-5">
            <label className="flex items-center gap-2 text-[13px] text-ink">
              <input
                type="checkbox"
                className="h-4 w-4 accent-brand"
                checked={form.strip_v1_prefix}
                onChange={(event) => update('strip_v1_prefix', event.target.checked)}
              />
              {copyChannels.form.stripV1Prefix}
            </label>
            <label className="flex items-center gap-2 text-[13px] text-ink">
              <input
                type="checkbox"
                className="h-4 w-4 accent-brand"
                checked={form.passthrough_body}
                onChange={(event) => update('passthrough_body', event.target.checked)}
              />
              {copyChannels.form.passthroughBody}
            </label>
          </div>

          <div className="mt-4 flex items-center justify-end">
            <Button type="submit" loading={save.isPending}>
              {save.isPending ? page.actions.saving : copyChannels.form.submit}
            </Button>
          </div>
          {submitError ? (
            <p
              role="alert"
              className="mt-4 border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] text-danger"
            >
              {submitError}
            </p>
          ) : null}
        </form>
      </Section>

      <Section title={copyChannels.listTitle} hint={page.count(rows.length)}>
        {channels.isPending ? (
          <TableSkeleton rows={6} columns={5} />
        ) : channels.isError ? (
          <ErrorBlock error={channels.error} onRetry={() => void channels.refetch()} />
        ) : rows.length === 0 ? (
          <Empty
            title={copyChannels.empty.title}
            reason={copyChannels.empty.reason}
            next={copyChannels.empty.next}
          />
        ) : (
          <Table minWidth={880}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>{copyChannels.columns.slug}</TableHeaderCell>
                <TableHeaderCell>{copyChannels.columns.name}</TableHeaderCell>
                <TableHeaderCell>{copyChannels.columns.kind}</TableHeaderCell>
                <TableHeaderCell>{copyChannels.columns.protocols}</TableHeaderCell>
                <TableHeaderCell>{copyChannels.columns.status}</TableHeaderCell>
                <TableHeaderCell className="text-right">
                  {copyChannels.columns.actions}
                </TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className="font-mono text-[13px]">{row.slug}</TableCell>
                  <TableCell className="font-medium">{row.name || '—'}</TableCell>
                  <TableCell>
                    <KindTag kind={row.kind} />
                  </TableCell>
                  <TableCell className="text-[13px] text-ink-3">
                    <div className="truncate">{row.protocols.openai || '—'}</div>
                    {row.protocols.anthropic ? (
                      <div className="truncate">{row.protocols.anthropic}</div>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <StatusTag status={row.status} />
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => {
                          setEditing(row.slug)
                          setForm(channelToForm(row))
                          setErrors({})
                          setSubmitError(null)
                        }}
                      >
                        {page.actions.edit}
                      </Button>
                      <Button variant="danger" size="sm" onClick={() => setDeleteTarget(row)}>
                        {page.actions.delete}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>

      <ConfirmDialog
        open={deleteTarget !== null}
        title={deleteTarget ? copyChannels.deleteTitle(deleteTarget.slug) : ''}
        description={copyChannels.deleteLead}
        confirmLabel={page.actions.delete}
        loading={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.slug)
        }}
        onClose={() => setDeleteTarget(null)}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// 渠道模型
// ---------------------------------------------------------------------------

interface ChannelModelForm {
  channel_slug: string
  upstream_model: string
  model: string
  coefficient_milli: string
  differences: string
  verified: boolean
  status: string
}

const emptyChannelModelForm: ChannelModelForm = {
  channel_slug: '',
  upstream_model: '',
  model: '',
  coefficient_milli: '1000',
  differences: '',
  verified: false,
  status: 'active',
}

function channelModelToForm(row: AdminPoolChannelModel): ChannelModelForm {
  return {
    channel_slug: row.channel_slug,
    upstream_model: row.upstream_model,
    model: row.model,
    coefficient_milli: String(row.coefficient_milli || 1000),
    differences: Object.keys(row.differences).length
      ? JSON.stringify(row.differences)
      : '',
    verified: row.verified,
    status: row.status || 'active',
  }
}

function ChannelModelsTab() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const channelModels = useQuery(adminPoolChannelModelsQuery())
  const channels = useQuery(adminPoolChannelsQuery())
  const models = useQuery(adminPoolModelsQuery())
  const [form, setForm] = useState<ChannelModelForm>(emptyChannelModelForm)
  const [editing, setEditing] = useState<string | null>(null)
  const [errors, setErrors] = useState<Partial<Record<keyof ChannelModelForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminPoolChannelModel | null>(null)

  const rows = channelModels.data ?? []
  const copyCM = page.channelModels

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: adminPoolChannelModelsQuery().queryKey })
  }

  const save = useMutation({
    mutationFn: putAdminPoolChannelModel,
    onSuccess: () => {
      refresh()
      toast.show({ title: copyCM.success, tone: 'success' })
      setForm((current) => ({
        ...emptyChannelModelForm,
        channel_slug: current.channel_slug,
      }))
      setEditing(null)
      setErrors({})
      setSubmitError(null)
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  const remove = useMutation({
    mutationFn: (target: AdminPoolChannelModel) =>
      deleteAdminPoolChannelModel(target.channel_slug, target.upstream_model),
    onSuccess: () => {
      refresh()
      toast.show({ title: copyCM.deleteSuccess, tone: 'success' })
      setDeleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function update<K extends keyof ChannelModelForm>(key: K, value: ChannelModelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function submit() {
    const next: Partial<Record<keyof ChannelModelForm, string>> = {}
    const slug = form.channel_slug.trim()
    const upstream = form.upstream_model.trim()
    const model = form.model.trim()
    const coefficient = parsePositiveInt(form.coefficient_milli)
    const differences = parseJSONObject(form.differences)
    if (!slug) next.channel_slug = copyCM.errors.channelRequired
    if (!upstream) next.upstream_model = copyCM.errors.upstreamRequired
    if (!model) next.model = copyCM.errors.modelRequired
    if (coefficient === null) next.coefficient_milli = copyCM.errors.coefficientInvalid
    if (differences === null) next.differences = copyCM.errors.differencesInvalid
    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }
    setErrors({})
    save.mutate({
      channel_slug: slug,
      upstream_model: upstream,
      model,
      coefficient_milli: coefficient ?? 1000,
      differences: differences ?? {},
      verified: form.verified,
      status: form.status,
    })
  }

  return (
    <div className="space-y-6">
      <Section
        title={
          editing
            ? copyCM.formEditTitle(editing.split('|')[0] ?? '', editing.split('|')[1] ?? '')
            : copyCM.formTitle
        }
        hint={copyCM.formLead}
        action={
          editing ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setEditing(null)
                setForm(emptyChannelModelForm)
                setErrors({})
                setSubmitError(null)
              }}
            >
              {page.actions.cancelEdit}
            </Button>
          ) : null
        }
      >
        <form
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field
              label={copyCM.form.channel}
              htmlFor="pool-cm-channel"
              error={errors.channel_slug}
            >
              <Select
                id="pool-cm-channel"
                value={form.channel_slug}
                disabled={editing !== null}
                onChange={(event) => update('channel_slug', event.target.value)}
              >
                <option value="">{copyCM.errors.channelRequired}</option>
                {(channels.data ?? []).map((channel) => (
                  <option key={channel.slug} value={channel.slug}>
                    {channel.name || channel.slug}（{channel.slug}）
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label={copyCM.form.upstream}
              htmlFor="pool-cm-upstream"
              error={errors.upstream_model}
            >
              <TextInput
                id="pool-cm-upstream"
                value={form.upstream_model}
                placeholder={copyCM.form.upstreamPlaceholder}
                autoComplete="off"
                disabled={editing !== null}
                invalid={Boolean(errors.upstream_model)}
                onChange={(event) => update('upstream_model', event.target.value)}
              />
            </Field>
            <Field label={copyCM.form.model} htmlFor="pool-cm-model" error={errors.model}>
              <TextInput
                id="pool-cm-model"
                list="pool-cm-models"
                value={form.model}
                placeholder={copyCM.form.modelPlaceholder}
                autoComplete="off"
                invalid={Boolean(errors.model)}
                onChange={(event) => update('model', event.target.value)}
              />
              <datalist id="pool-cm-models">
                {(models.data ?? []).map((model) => (
                  <option key={model.name} value={model.name} />
                ))}
              </datalist>
            </Field>
            <Field
              label={copyCM.form.coefficient}
              htmlFor="pool-cm-coefficient"
              hint={copyCM.form.coefficientHint}
              error={errors.coefficient_milli}
            >
              <TextInput
                id="pool-cm-coefficient"
                value={form.coefficient_milli}
                inputMode="numeric"
                invalid={Boolean(errors.coefficient_milli)}
                onChange={(event) => update('coefficient_milli', event.target.value)}
              />
            </Field>
            <Field
              label={copyCM.form.differences}
              htmlFor="pool-cm-differences"
              error={errors.differences}
            >
              <TextInput
                id="pool-cm-differences"
                value={form.differences}
                placeholder={copyCM.form.differencesPlaceholder}
                autoComplete="off"
                invalid={Boolean(errors.differences)}
                onChange={(event) => update('differences', event.target.value)}
              />
            </Field>
            <Field label={copyCM.form.status} htmlFor="pool-cm-status">
              <Select
                id="pool-cm-status"
                value={form.status}
                onChange={(event) => update('status', event.target.value)}
              >
                <option value="active">{labels.active}</option>
                <option value="paused">{labels.paused}</option>
                <option value="banned">{labels.banned}</option>
              </Select>
            </Field>
          </div>

          <label className="mt-4 flex items-center gap-2 text-[13px] text-ink">
            <input
              type="checkbox"
              className="h-4 w-4 accent-brand"
              checked={form.verified}
              onChange={(event) => update('verified', event.target.checked)}
            />
            {copyCM.form.verified}
          </label>

          <div className="mt-4 flex items-center justify-end">
            <Button type="submit" loading={save.isPending}>
              {save.isPending ? page.actions.saving : copyCM.form.submit}
            </Button>
          </div>
          {submitError ? (
            <p
              role="alert"
              className="mt-4 border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] text-danger"
            >
              {submitError}
            </p>
          ) : null}
        </form>
      </Section>

      <Section title={copyCM.listTitle} hint={page.count(rows.length)}>
        {channelModels.isPending ? (
          <TableSkeleton rows={6} columns={6} />
        ) : channelModels.isError ? (
          <ErrorBlock
            error={channelModels.error}
            onRetry={() => void channelModels.refetch()}
          />
        ) : rows.length === 0 ? (
          <Empty
            title={copyCM.empty.title}
            reason={copyCM.empty.reason}
            next={copyCM.empty.next}
          />
        ) : (
          <Table minWidth={880}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell>{copyCM.columns.channel}</TableHeaderCell>
                <TableHeaderCell>{copyCM.columns.upstream}</TableHeaderCell>
                <TableHeaderCell>{copyCM.columns.model}</TableHeaderCell>
                <TableHeaderCell numeric>{copyCM.columns.coefficient}</TableHeaderCell>
                <TableHeaderCell>{copyCM.columns.verified}</TableHeaderCell>
                <TableHeaderCell>{copyCM.columns.status}</TableHeaderCell>
                <TableHeaderCell className="text-right">
                  {copyCM.columns.actions}
                </TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className="font-mono text-[13px]">{row.channel_slug}</TableCell>
                  <TableCell className="font-mono text-[13px]">{row.upstream_model}</TableCell>
                  <TableCell className="font-mono text-[13px]">{row.model}</TableCell>
                  <TableCell numeric>{formatMultiplier(row.coefficient_milli)}x</TableCell>
                  <TableCell>
                    {row.verified ? (
                      <Tag tone="brand" dot>
                        {copy.pages.modelDetail.channels.verified}
                      </Tag>
                    ) : (
                      <span className="text-[13px] text-ink-3">
                        {copy.pages.modelDetail.channels.unverified}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>
                    <StatusTag status={row.status} />
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => {
                          setEditing(`${row.channel_slug}|${row.upstream_model}`)
                          setForm(channelModelToForm(row))
                          setErrors({})
                          setSubmitError(null)
                        }}
                      >
                        {page.actions.edit}
                      </Button>
                      <Button variant="danger" size="sm" onClick={() => setDeleteTarget(row)}>
                        {page.actions.delete}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>

      <ConfirmDialog
        open={deleteTarget !== null}
        title={deleteTarget ? copyCM.deleteTitle(deleteTarget.upstream_model) : ''}
        description={copyCM.deleteLead}
        confirmLabel={page.actions.delete}
        loading={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget)
        }}
        onClose={() => setDeleteTarget(null)}
      />
    </div>
  )
}
