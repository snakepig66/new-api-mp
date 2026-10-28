import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
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
  useToast,
} from '@/ui'

import {
  adminPricesQuery,
  createAdminPrice,
  type AdminPrice,
} from '@/features/admin/api'
import {
  ErrorBlock,
  Field,
  PageSkeleton,
  Section,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import {
  emptyPriceForm,
  priceFormSchema,
  priceFormToInput,
  type PriceFormValues,
} from '@/features/admin/schemas'

export const Route = createFileRoute('/console/ops/pricing/')({
  component: PricesContent,
})

const page = copy.pages.adminPrices
const common = copy.pages.adminCommon

function groupByModel(rows: AdminPrice[]): { model: string; rows: AdminPrice[] }[] {
  const groups = new Map<string, AdminPrice[]>()
  for (const row of rows) {
    const list = groups.get(row.model)
    if (list) list.push(row)
    else groups.set(row.model, [row])
  }
  return [...groups.entries()].map(([model, items]) => ({ model, rows: items }))
}

function PricesContent() {
  const prices = useQuery(adminPricesQuery())
  const [dialogOpen, setDialogOpen] = useState(false)
  const groups = groupByModel(prices.data ?? [])

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Button size="sm" onClick={() => setDialogOpen(true)}>
            {page.add}
          </Button>
        }
      />

      <div className="mt-6">
        {prices.isPending ? (
          <PageSkeleton rows={8} />
        ) : prices.isError ? (
          <ErrorBlock error={prices.error} onRetry={() => void prices.refetch()} />
        ) : groups.length === 0 ? (
          <Empty
            title={page.empty.title}
            reason={page.empty.reason}
            next={page.empty.next}
            action={
              <Button variant="secondary" size="sm" onClick={() => setDialogOpen(true)}>
                {page.add}
              </Button>
            }
          />
        ) : (
          <div className="space-y-8">
            {groups.map((group) => (
              <Section
                key={group.model}
                title={<span className="tabular-nums">{group.model}</span>}
                hint={page.groupCount(group.rows.length)}
              >
                <Table minWidth={900}>
                  <TableHead>
                    <TableRow>
                      <TableHeaderCell>{page.columns.vendor}</TableHeaderCell>
                      <TableHeaderCell numeric>
                        {page.columns.input} / {page.unit}
                      </TableHeaderCell>
                      <TableHeaderCell numeric>
                        {page.columns.output} / {page.unit}
                      </TableHeaderCell>
                      <TableHeaderCell numeric>
                        {page.columns.cacheRead} / {page.unit}
                      </TableHeaderCell>
                      <TableHeaderCell numeric>
                        {page.columns.cacheWrite} / {page.unit}
                      </TableHeaderCell>
                      <TableHeaderCell>{page.columns.version}</TableHeaderCell>
                      <TableHeaderCell>{page.columns.effectiveAt}</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {group.rows.map((row) => (
                      <TableRow key={row.id}>
                        <TableCell className=" text-[13px]">{row.vendor}</TableCell>
                        <TableCell numeric>{formatYuan(row.input_micro_per_mtok)}</TableCell>
                        <TableCell numeric>{formatYuan(row.output_micro_per_mtok)}</TableCell>
                        <TableCell numeric className="text-ink-3">
                          {formatYuan(row.cache_read_micro_per_mtok)}
                        </TableCell>
                        <TableCell numeric className="text-ink-3">
                          {formatYuan(row.cache_write_micro_per_mtok)}
                        </TableCell>
                        <TableCell className="tabular-nums text-[13px] text-ink-3">
                          v{row.version}
                        </TableCell>
                        <TableCell className="whitespace-nowrap tabular-nums text-[13px] text-ink-3">
                          {formatDateTime(row.effective_at)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Section>
            ))}
          </div>
        )}
      </div>

      <PriceDialog open={dialogOpen} onClose={() => setDialogOpen(false)} />
    </>
  )
}

type FormErrors = Partial<Record<keyof PriceFormValues, string>>

function PriceDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<PriceFormValues>(() => emptyPriceForm())
  const [errors, setErrors] = useState<FormErrors>({})
  const [submitError, setSubmitError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: createAdminPrice,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'prices'] })
      toast.show({ title: page.success, description: page.successHint, tone: 'success' })
      setForm(emptyPriceForm())
      setErrors({})
      setSubmitError(null)
      onClose()
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  function update<K extends keyof PriceFormValues>(key: K, value: PriceFormValues[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function reset() {
    setForm(emptyPriceForm())
    setErrors({})
    setSubmitError(null)
    mutation.reset()
  }

  function submit() {
    const result = priceFormSchema().safeParse(form)
    if (!result.success) {
      const next: FormErrors = {}
      for (const issue of result.error.issues) {
        const key = issue.path[0]
        if (typeof key === 'string') next[key as keyof PriceFormValues] = issue.message
      }
      setErrors(next)
      return
    }
    setErrors({})
    mutation.mutate(priceFormToInput(result.data))
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          onClose()
          reset()
        }
      }}
      title={page.dialog.title}
      description={page.dialog.lead}
      className="max-h-[calc(100vh-3rem)] max-w-2xl overflow-y-auto"
      footer={
        <>
          <Button
            variant="secondary"
            onClick={() => {
              onClose()
              reset()
            }}
            disabled={mutation.isPending}
          >
            {common.cancel}
          </Button>
          <Button loading={mutation.isPending} onClick={submit}>
            {mutation.isPending ? page.dialog.submitting : page.dialog.submit}
          </Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label={page.dialog.model}
          htmlFor="price-model"
          error={errors.model}
          className="sm:col-span-1"
        >
          <TextInput
            id="price-model"
            mono={false}
            placeholder={page.dialog.modelPlaceholder}
            value={form.model}
            invalid={Boolean(errors.model)}
            onChange={(event) => update('model', event.target.value)}
          />
        </Field>
        <Field label={page.dialog.vendor} htmlFor="price-vendor" error={errors.vendor}>
          <TextInput
            id="price-vendor"
            placeholder={page.dialog.vendorPlaceholder}
            value={form.vendor}
            invalid={Boolean(errors.vendor)}
            onChange={(event) => update('vendor', event.target.value)}
          />
        </Field>
        <Field
          label={page.dialog.version}
          htmlFor="price-version"
          hint={page.dialog.versionHint}
          error={errors.version}
        >
          <TextInput
            id="price-version"
            inputMode="numeric"
            placeholder="1"
            value={form.version}
            invalid={Boolean(errors.version)}
            onChange={(event) => update('version', event.target.value)}
          />
        </Field>
        <Field label={page.dialog.input} htmlFor="price-input" error={errors.input_micro_per_mtok}>
          <TextInput
            id="price-input"
            inputMode="decimal"
            value={form.input_micro_per_mtok}
            invalid={Boolean(errors.input_micro_per_mtok)}
            onChange={(event) => update('input_micro_per_mtok', event.target.value)}
          />
        </Field>
        <Field label={page.dialog.output} htmlFor="price-output" error={errors.output_micro_per_mtok}>
          <TextInput
            id="price-output"
            inputMode="decimal"
            value={form.output_micro_per_mtok}
            invalid={Boolean(errors.output_micro_per_mtok)}
            onChange={(event) => update('output_micro_per_mtok', event.target.value)}
          />
        </Field>
        <Field
          label={page.dialog.cacheRead}
          htmlFor="price-cache-read"
          error={errors.cache_read_micro_per_mtok}
        >
          <TextInput
            id="price-cache-read"
            inputMode="decimal"
            value={form.cache_read_micro_per_mtok}
            invalid={Boolean(errors.cache_read_micro_per_mtok)}
            onChange={(event) => update('cache_read_micro_per_mtok', event.target.value)}
          />
        </Field>
        <Field
          label={page.dialog.cacheWrite}
          htmlFor="price-cache-write"
          error={errors.cache_write_micro_per_mtok}
        >
          <TextInput
            id="price-cache-write"
            inputMode="decimal"
            value={form.cache_write_micro_per_mtok}
            invalid={Boolean(errors.cache_write_micro_per_mtok)}
            onChange={(event) => update('cache_write_micro_per_mtok', event.target.value)}
          />
        </Field>
        <Field
          label={page.dialog.effectiveAt}
          htmlFor="price-effective-at"
          hint={page.dialog.effectiveAtHint}
          error={errors.effective_at}
          className="sm:col-span-2"
        >
          <TextInput
            id="price-effective-at"
            type="datetime-local"
            value={form.effective_at}
            invalid={Boolean(errors.effective_at)}
            onChange={(event) => update('effective_at', event.target.value)}
          />
        </Field>
      </div>

      {submitError ? (
        <p
          role="alert"
          className="mt-4 border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] text-danger"
        >
          {submitError}
        </p>
      ) : null}
    </Dialog>
  )
}
