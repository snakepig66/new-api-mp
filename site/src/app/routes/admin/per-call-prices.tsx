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
  adminPerCallPricesQuery,
  createAdminPerCallPrice,
  type AdminPerCallPrice,
} from '@/features/admin/api'
import {
  ErrorBlock,
  Field,
  PageSkeleton,
  RootOnly,
  Section,
  Select,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import {
  emptyPerCallForm,
  perCallFormSchema,
  perCallFormToInput,
  type PerCallFormValues,
} from '@/features/admin/schemas'

export const Route = createFileRoute('/admin/per-call-prices')({
  component: AdminPerCallPricesPage,
})

const page = copy.pages.adminPerCallPrices
const common = copy.pages.adminCommon

function itemLabel(item: string): string {
  if (item === 'search' || item === 'fetch') return page.items[item]
  return item || common.none
}

function groupByVendor(
  rows: AdminPerCallPrice[],
): { vendor: string; rows: AdminPerCallPrice[] }[] {
  const groups = new Map<string, AdminPerCallPrice[]>()
  for (const row of rows) {
    const list = groups.get(row.vendor)
    if (list) list.push(row)
    else groups.set(row.vendor, [row])
  }
  return [...groups.entries()].map(([vendor, items]) => ({ vendor, rows: items }))
}

function AdminPerCallPricesPage() {
  return (
    <RootOnly>
      <PerCallPricesContent />
    </RootOnly>
  )
}

function PerCallPricesContent() {
  const prices = useQuery(adminPerCallPricesQuery())
  const [dialogOpen, setDialogOpen] = useState(false)
  const groups = groupByVendor(prices.data ?? [])

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
          <PageSkeleton rows={5} />
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
                key={group.vendor}
                title={<span className="font-numeric">{group.vendor}</span>}
                hint={page.groupCount(group.rows.length)}
              >
                <Table minWidth={720}>
                  <TableHead>
                    <TableRow>
                      <TableHeaderCell>{page.columns.item}</TableHeaderCell>
                      <TableHeaderCell numeric>{page.columns.unitPrice}</TableHeaderCell>
                      <TableHeaderCell>{page.columns.version}</TableHeaderCell>
                      <TableHeaderCell>{page.columns.effectiveAt}</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {group.rows.map((row) => (
                      <TableRow key={row.id}>
                        <TableCell>
                          <span className="font-numeric text-[12.5px]">{itemLabel(row.item)}</span>
                        </TableCell>
                        <TableCell numeric>
                          {formatYuan(row.unit_price_micro)}
                          <span className="ml-1 text-[11px] text-muted">/ 次</span>
                        </TableCell>
                        <TableCell className="font-numeric text-[12px] text-muted">
                          v{row.version}
                        </TableCell>
                        <TableCell className="whitespace-nowrap font-numeric text-[12px] text-muted">
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

      <PerCallDialog open={dialogOpen} onClose={() => setDialogOpen(false)} />
    </>
  )
}

type FormErrors = Partial<Record<keyof PerCallFormValues, string>>

function PerCallDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<PerCallFormValues>(() => emptyPerCallForm())
  const [errors, setErrors] = useState<FormErrors>({})
  const [submitError, setSubmitError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: createAdminPerCallPrice,
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['marketplace', 'admin', 'per-call-prices'],
      })
      toast.show({ title: page.success, description: page.successHint, tone: 'success' })
      setForm(emptyPerCallForm())
      setErrors({})
      setSubmitError(null)
      onClose()
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  function update<K extends keyof PerCallFormValues>(key: K, value: PerCallFormValues[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function reset() {
    setForm(emptyPerCallForm())
    setErrors({})
    setSubmitError(null)
    mutation.reset()
  }

  function submit() {
    const result = perCallFormSchema().safeParse(form)
    if (!result.success) {
      const next: FormErrors = {}
      for (const issue of result.error.issues) {
        const key = issue.path[0]
        if (typeof key === 'string') next[key as keyof PerCallFormValues] = issue.message
      }
      setErrors(next)
      return
    }
    setErrors({})
    mutation.mutate(perCallFormToInput(result.data))
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
      className="max-h-[calc(100vh-3rem)] max-w-xl overflow-y-auto"
      footer={
        <>
          <Button
            variant="secondary"
            disabled={mutation.isPending}
            onClick={() => {
              onClose()
              reset()
            }}
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
        <Field label={page.dialog.vendor} htmlFor="per-call-vendor" error={errors.vendor}>
          <TextInput
            id="per-call-vendor"
            placeholder={page.dialog.vendorPlaceholder}
            value={form.vendor}
            invalid={Boolean(errors.vendor)}
            onChange={(event) => update('vendor', event.target.value)}
          />
        </Field>
        <Field label={page.dialog.item} htmlFor="per-call-item" error={errors.item}>
          <Select
            id="per-call-item"
            value={form.item}
            onChange={(event) => update('item', event.target.value)}
          >
            <option value="search">{page.items.search}</option>
            <option value="fetch">{page.items.fetch}</option>
          </Select>
        </Field>
        <Field label={page.dialog.unitPrice} htmlFor="per-call-price" error={errors.unit_price_micro}>
          <TextInput
            id="per-call-price"
            inputMode="numeric"
            value={form.unit_price_micro}
            invalid={Boolean(errors.unit_price_micro)}
            onChange={(event) => update('unit_price_micro', event.target.value)}
          />
        </Field>
        <Field
          label={page.dialog.version}
          htmlFor="per-call-version"
          hint={page.dialog.versionHint}
          error={errors.version}
        >
          <TextInput
            id="per-call-version"
            inputMode="numeric"
            placeholder="1"
            value={form.version}
            invalid={Boolean(errors.version)}
            onChange={(event) => update('version', event.target.value)}
          />
        </Field>
        <Field
          label={page.dialog.effectiveAt}
          htmlFor="per-call-effective-at"
          hint={page.dialog.effectiveAtHint}
          error={errors.effective_at}
          className="sm:col-span-2"
        >
          <TextInput
            id="per-call-effective-at"
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
          className="mt-4 border border-danger/25 bg-danger-soft px-3 py-2 text-[12px] text-danger"
        >
          {submitError}
        </p>
      ) : null}
    </Dialog>
  )
}
