import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { api } from '@/lib/api'
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
  useToast,
} from '@/ui'

import {
  ErrorBlock,
  Field,
  RootOnly,
  Section,
  TableSkeleton,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { vendorName, vendorsQuery } from '@/features/seller/api'

export const Route = createFileRoute('/admin/channel-models')({
  component: AdminChannelModelsPage,
})

const page = copy.pages.adminChannelModels

interface ChannelModel {
  id: number
  vendor: string
  model: string
  upstream_model: string
}

interface ChannelModelInput {
  vendor: string
  model: string
  upstream_model: string
}

const queryKey = ['marketplace', 'admin', 'channel-models'] as const

function text(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

function normalizeChannelModel(raw: unknown): ChannelModel {
  const row = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  const pick = (snake: string, go: string): unknown => row[snake] ?? row[go]
  return {
    id: Number(pick('id', 'ID') ?? 0) || 0,
    vendor: text(pick('vendor', 'Vendor')),
    model: text(pick('model', 'Model')),
    upstream_model: text(pick('upstream_model', 'UpstreamModel')),
  }
}

const channelModelsQuery = () =>
  queryOptions({
    queryKey,
    queryFn: async (): Promise<ChannelModel[]> => {
      const raw = await api.get<unknown>('/marketplace/admin/channel-models')
      const record = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
      const items = Array.isArray(raw)
        ? raw
        : Array.isArray(record.items)
          ? record.items
          : []
      return items.map(normalizeChannelModel)
    },
    staleTime: 15_000,
  })

function saveChannelModel(input: ChannelModelInput): Promise<unknown> {
  return api.put<unknown>('/marketplace/admin/channel-models', input)
}

function deleteChannelModel(vendor: string, model: string): Promise<unknown> {
  return api.delete<unknown>('/marketplace/admin/channel-models', {
    query: { vendor, model },
  })
}

function AdminChannelModelsPage() {
  return (
    <RootOnly>
      <ChannelModelsContent />
    </RootOnly>
  )
}

interface FormValues {
  vendor: string
  model: string
  upstream_model: string
}

type FormErrors = Partial<Record<keyof FormValues, string>>

const emptyForm: FormValues = { vendor: '', model: '', upstream_model: '' }

function validateForm(values: FormValues): FormErrors {
  const errors: FormErrors = {}
  const errorsCopy = page.errors
  const vendor = values.vendor.trim()
  const model = values.model.trim()
  const upstream = values.upstream_model.trim()

  if (!vendor) errors.vendor = errorsCopy.vendorRequired
  else if (vendor.length > 64) errors.vendor = errorsCopy.vendorTooLong

  if (!model) errors.model = errorsCopy.modelRequired
  else if (model.length > 128) errors.model = errorsCopy.modelTooLong

  if (!upstream) errors.upstream_model = errorsCopy.upstreamRequired
  else if (upstream.length > 128) errors.upstream_model = errorsCopy.upstreamTooLong

  return errors
}

function ChannelModelsContent() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const models = useQuery(channelModelsQuery())
  const vendors = useQuery(vendorsQuery())
  const [form, setForm] = useState<FormValues>(emptyForm)
  const [errors, setErrors] = useState<FormErrors>({})
  const [submitError, setSubmitError] = useState<string | null>(null)

  const rows = models.data ?? []

  function refresh() {
    void queryClient.invalidateQueries({ queryKey })
  }

  const save = useMutation({
    mutationFn: saveChannelModel,
    onSuccess: () => {
      refresh()
      toast.show({ title: page.success, description: page.successHint, tone: 'success' })
      setForm((current) => ({ ...current, model: '', upstream_model: '' }))
      setErrors({})
      setSubmitError(null)
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  const remove = useMutation({
    mutationFn: (input: { vendor: string; model: string }) =>
      deleteChannelModel(input.vendor, input.model),
    onSuccess: () => {
      refresh()
      toast.show({ title: page.deleteSuccess, tone: 'success' })
    },
    onError: (error) => {
      toast.show({ title: adminErrorMessage(error), tone: 'error' })
    },
  })

  function update<K extends keyof FormValues>(key: K, value: FormValues[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function submit() {
    const nextErrors = validateForm(form)
    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors)
      return
    }
    setErrors({})
    save.mutate({
      vendor: form.vendor.trim(),
      model: form.model.trim(),
      upstream_model: form.upstream_model.trim(),
    })
  }

  const deleting = (row: ChannelModel): boolean =>
    remove.isPending &&
    remove.variables?.vendor === row.vendor &&
    remove.variables?.model === row.model

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />

      <div className="mt-6 space-y-6">
        <Section title={page.form.title} hint={page.form.lead}>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              submit()
            }}
          >
            <div className="grid gap-4 sm:grid-cols-3">
              <Field
                label={page.form.vendor}
                htmlFor="channel-model-vendor"
                hint={page.form.vendorHint}
                error={errors.vendor}
              >
                <TextInput
                  id="channel-model-vendor"
                  list="channel-model-vendors"
                  placeholder={page.form.vendorPlaceholder}
                  autoComplete="off"
                  value={form.vendor}
                  invalid={Boolean(errors.vendor)}
                  onChange={(event) => update('vendor', event.target.value)}
                />
              </Field>
              <Field
                label={page.form.model}
                htmlFor="channel-model-model"
                error={errors.model}
              >
                <TextInput
                  id="channel-model-model"
                  placeholder={page.form.modelPlaceholder}
                  autoComplete="off"
                  value={form.model}
                  invalid={Boolean(errors.model)}
                  onChange={(event) => update('model', event.target.value)}
                />
              </Field>
              <Field
                label={page.form.upstream}
                htmlFor="channel-model-upstream"
                error={errors.upstream_model}
              >
                <TextInput
                  id="channel-model-upstream"
                  placeholder={page.form.upstreamPlaceholder}
                  autoComplete="off"
                  value={form.upstream_model}
                  invalid={Boolean(errors.upstream_model)}
                  onChange={(event) => update('upstream_model', event.target.value)}
                />
              </Field>
            </div>

            <datalist id="channel-model-vendors">
              {(vendors.data ?? []).map((vendor) => (
                <option key={vendor.id} value={vendor.id} label={vendor.display_name} />
              ))}
            </datalist>

            <div className="mt-4 flex items-center justify-end">
              <Button type="submit" loading={save.isPending}>
                {save.isPending ? page.form.submitting : page.form.submit}
              </Button>
            </div>
          </form>

          {submitError ? (
            <p
              role="alert"
              className="mt-4 border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] text-danger"
            >
              {submitError}
            </p>
          ) : null}
        </Section>

        <Section title={page.listTitle} hint={page.count(rows.length)}>
          {models.isPending ? (
            <TableSkeleton rows={6} columns={4} />
          ) : models.isError ? (
            <ErrorBlock error={models.error} onRetry={() => void models.refetch()} />
          ) : rows.length === 0 ? (
            <Empty
              title={page.empty.title}
              reason={page.empty.reason}
              next={page.empty.next}
            />
          ) : (
            <Table minWidth={720}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{page.columns.vendor}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.model}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.upstream}</TableHeaderCell>
                  <TableHeaderCell className="text-right">{page.columns.actions}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {rows.map((row) => (
                  <TableRow key={row.id}>
                    <TableCell>
                      <div className="font-medium text-ink">
                        {vendorName(row.vendor, vendors.data)}
                      </div>
                      <div className="tabular-nums text-[13px] text-ink-3">{row.vendor}</div>
                    </TableCell>
                    <TableCell className="font-mono text-[13px]">{row.model}</TableCell>
                    <TableCell className="font-mono text-[13px] text-ink-2">
                      {row.upstream_model}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="danger"
                        size="sm"
                        loading={deleting(row)}
                        onClick={() => remove.mutate({ vendor: row.vendor, model: row.model })}
                      >
                        {deleting(row) ? page.deleting : page.delete}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Section>
      </div>
    </>
  )
}
