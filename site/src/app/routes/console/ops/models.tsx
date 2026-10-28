import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
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
  adminPoolModelsQuery,
  deleteAdminPoolModel,
  putAdminPoolModel,
  type AdminPoolModel,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  Section,
  Select,
  TableSkeleton,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

// Seller channels now pick their own models (from upstream or the pool), so
// the admin channel-template and channel-model mapping tabs are gone; only
// the standard model pool is managed here.
export const Route = createFileRoute('/console/ops/models')({
  component: PoolContent,
})

const page = copy.pages.adminPool
const labels = page.status

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

function statusLabel(status: string): string {
  return (labels as Record<string, string>)[status] ?? labels.unknown
}

function StatusTag({ status }: { status: string }) {
  return (
    <Tag tone={status === 'active' ? 'brand' : 'neutral'} dot={status === 'active'}>
      {statusLabel(status)}
    </Tag>
  )
}

function PoolContent() {
  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />
      <div className="mt-6">
        <ModelsTab />
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
