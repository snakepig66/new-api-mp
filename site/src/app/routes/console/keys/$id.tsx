import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  Input,
  SellerId,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  buttonClasses,
  useToast,
} from '@/ui'

import {
  addKeyModel,
  deleteKey,
  keyDetailQuery,
  removeKeyModel,
  revealTokenKey,
  saveKeyPref,
  setTokenStatus,
} from '@/features/console/api'
import {
  ErrorBlock,
  PageSkeleton,
  Section,
} from '@/features/console/components/DataState'
import { Field, Notice, Select } from '@/features/console/components/form'
import { milliToMultiplierInput, parseMultiplierToMilli, parseSellerIds } from '@/features/console/format'
import { tokenStatusMeta } from '@/features/console/labels'
import {
  prefFormSchema,
  prefFormToInput,
  prefToFormValues,
  type PrefFormValues,
} from '@/features/console/schemas'
import {
  STRATEGIES,
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  type KeyDetail,
} from '@/features/console/types'
import { squareModelsQuery } from '@/features/market/api'

export const Route = createFileRoute('/console/keys/$id')({
  component: KeyDetailPage,
})

const page = copy.pages.consoleKeyDetail
const shared = copy.pages.consoleShared

function KeyDetailPage() {
  const { id } = Route.useParams()
  const tokenId = Number(id)
  const valid = Number.isSafeInteger(tokenId) && tokenId > 0
  const detail = useQuery({ ...keyDetailQuery(tokenId), enabled: valid })

  if (!valid) {
    return (
      <Empty
        className="mt-6"
        title={page.notFoundTitle}
        reason={page.notFoundReason}
        next={page.notFoundNext}
        action={
          <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
            {page.backToKeys}
          </Link>
        }
      />
    )
  }

  if (detail.isPending) {
    return (
      <div className="mt-2">
        <PageSkeleton />
      </div>
    )
  }

  if (detail.isError) {
    if (detail.error instanceof ApiError && detail.error.status === 404) {
      return (
        <Empty
          className="mt-6"
          title={page.notFoundTitle}
          reason={page.notFoundReason}
          next={page.notFoundNext}
          action={
            <Link to="/console/keys" className={buttonClasses('secondary', 'sm')}>
              {page.backToKeys}
            </Link>
          }
        />
      )
    }
    return (
      <>
        <BackLink />
        <ErrorBlock
          className="mt-5"
          error={detail.error}
          onRetry={() => void detail.refetch()}
        />
      </>
    )
  }

  return <KeyDetailContent tokenId={tokenId} detail={detail.data} />
}

function BackLink() {
  return (
    <Link
      to="/console/keys"
      className="inline-flex items-center gap-1 text-[12px] text-muted transition-colors hover:text-ink"
    >
      <span aria-hidden="true">←</span>
      {page.backToKeys}
    </Link>
  )
}

function KeyDetailContent({ tokenId, detail }: { tokenId: number; detail: KeyDetail }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { token, pref, models } = detail
  const status = tokenStatusMeta(token.status)
  const [copying, setCopying] = useState(false)
  const [statusBusy, setStatusBusy] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const form = useForm<PrefFormValues>({
    resolver: zodResolver(prefFormSchema),
    defaultValues: prefToFormValues(pref),
  })

  useEffect(() => {
    form.reset(prefToFormValues(pref))
  }, [form, pref])

  const preferText = form.watch('prefer')
  const blockText = form.watch('block')
  const onlyText = form.watch('only')
  const preferIds = parseSellerIds(preferText).ids
  const blockIds = parseSellerIds(blockText).ids
  const onlyIds = parseSellerIds(onlyText).ids

  async function handleCopy() {
    setCopying(true)
    try {
      const key = await revealTokenKey(tokenId)
      const ok = await writeClipboard(key)
      toast.show({
        title: ok ? shared.copied : shared.copyFailed,
        tone: ok ? 'success' : 'error',
      })
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : shared.copyFailed,
        tone: 'error',
      })
    } finally {
      setCopying(false)
    }
  }

  async function handleStatusToggle() {
    const next =
      token.status === TOKEN_STATUS_ENABLED ? TOKEN_STATUS_DISABLED : TOKEN_STATUS_ENABLED
    setStatusBusy(true)
    try {
      await setTokenStatus(tokenId, next)
      toast.show({
        title: next === TOKEN_STATUS_ENABLED ? page.status.enabled : page.status.disabled,
        tone: 'success',
      })
      await queryClient.invalidateQueries({ queryKey: ['console', 'key', tokenId] })
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint,
        tone: 'error',
      })
    } finally {
      setStatusBusy(false)
    }
  }

  async function handleDelete() {
    setDeleting(true)
    setDeleteError(null)
    try {
      await deleteKey(tokenId)
      toast.show({ title: page.danger.deleted, tone: 'success' })
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
      setDeleteOpen(false)
      await navigate({ to: '/console/keys' })
    } catch (error) {
      setDeleteError(error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint)
    } finally {
      setDeleting(false)
    }
  }

  const savePref = form.handleSubmit(async (values) => {
    form.clearErrors('root')
    try {
      await saveKeyPref(prefFormToInput(tokenId, values))
      toast.show({ title: page.config.saved, tone: 'success' })
      await queryClient.invalidateQueries({ queryKey: ['console', 'key', tokenId] })
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
    } catch (error) {
      form.setError('root', {
        message: error instanceof ApiError ? error.message : page.errors.saveFailed,
      })
    }
  })

  return (
    <>
      <BackLink />

      <div className="mt-4 flex flex-col gap-5 border-b border-line pb-6 lg:flex-row lg:items-end lg:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="text-[24px] font-semibold leading-tight tracking-tight text-ink">
              {token.name || shared.none}
            </h1>
            <Tag tone={status.tone} dot>
              {status.label}
            </Tag>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-[12px] text-muted">
            <span>{page.maskedKey}</span>
            <code className="rounded-[3px] border border-line bg-wash px-1.5 py-0.5 font-numeric text-[12px] text-ink">
              {token.key}
            </code>
            <span>{page.copyKeyHint}</span>
          </div>
          <div className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-[11.5px] text-muted">
            <span>
              {copy.pages.consoleKeys.columns.spend}：{copy.pages.consoleKeys.spend.used(formatYuan(token.used_quota))}
            </span>
            <span>
              {copy.pages.consoleKeys.columns.models}：{copy.pages.consoleKeys.modelsUnit(models.length)}
            </span>
            <span>{page.status.hint}</span>
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" loading={copying} onClick={() => void handleCopy()}>
            {page.copyKey}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            loading={statusBusy}
            onClick={() => void handleStatusToggle()}
          >
            {token.status === TOKEN_STATUS_ENABLED ? page.status.disable : page.status.enable}
          </Button>
          <Button variant="danger" size="sm" onClick={() => setDeleteOpen(true)}>
            {page.danger.action}
          </Button>
        </div>
      </div>

      <Section className="mt-8" title={page.config.title} hint={page.config.hint}>
        {!pref ? <Notice className="mb-4">{page.config.noPref}</Notice> : null}
        <form onSubmit={savePref} className="grid gap-4 sm:grid-cols-2" noValidate>
          <Field
            label={page.config.strategy}
            htmlFor="pref-strategy"
            error={form.formState.errors.strategy?.message}
          >
            <Select id="pref-strategy" {...form.register('strategy')}>
              {STRATEGIES.map((strategy) => (
                <option key={strategy} value={strategy}>
                  {copy.pages.consoleKeys.strategy[strategy]}
                </option>
              ))}
            </Select>
          </Field>

          <Field
            label={page.config.priceCap}
            htmlFor="pref-price-cap"
            hint={page.config.priceCapHint}
            error={form.formState.errors.priceCap?.message}
          >
            <Input
              id="pref-price-cap"
              mono
              inputMode="decimal"
              placeholder="0.85"
              {...form.register('priceCap')}
            />
          </Field>

          <Field
            label={page.config.rpm}
            htmlFor="pref-rpm"
            hint={page.config.rpmHint}
            error={form.formState.errors.rpm?.message}
          >
            <Input
              id="pref-rpm"
              mono
              inputMode="numeric"
              placeholder="60"
              {...form.register('rpm')}
            />
          </Field>

          <div className="hidden sm:block" />

          <Field
            label={page.config.prefer}
            htmlFor="pref-prefer"
            hint={page.config.preferHint}
            error={form.formState.errors.prefer?.message}
          >
            <Input
              id="pref-prefer"
              mono
              placeholder={page.sellerIds.placeholder}
              {...form.register('prefer')}
            />
            <SellerChips
              ids={preferIds}
              onRemove={(sellerId) =>
                form.setValue(
                  'prefer',
                  preferIds.filter((value) => value !== sellerId).join(', '),
                  { shouldDirty: true },
                )
              }
            />
          </Field>

          <Field
            label={page.config.block}
            htmlFor="pref-block"
            hint={page.config.blockHint}
            error={form.formState.errors.block?.message}
          >
            <Input
              id="pref-block"
              mono
              placeholder={page.sellerIds.placeholder}
              {...form.register('block')}
            />
            <SellerChips
              ids={blockIds}
              onRemove={(sellerId) =>
                form.setValue(
                  'block',
                  blockIds.filter((value) => value !== sellerId).join(', '),
                  { shouldDirty: true },
                )
              }
            />
          </Field>

          <Field
            label={page.config.only}
            htmlFor="pref-only"
            hint={page.config.onlyHint}
            error={form.formState.errors.only?.message}
          >
            <Input
              id="pref-only"
              mono
              inputMode="numeric"
              placeholder="10023"
              {...form.register('only')}
            />
            {onlyIds.length === 1 ? (
              <div className="mt-1.5">
                <SellerId id={onlyIds[0]} />
              </div>
            ) : null}
          </Field>

          <div className="sm:col-span-2">
            <label className="inline-flex items-start gap-2 text-[12.5px] text-ink">
              <input
                type="checkbox"
                className="mt-0.5 h-3.5 w-3.5 accent-[#087266]"
                {...form.register('allowFallback')}
              />
              <span>
                {page.config.allowFallback}
                <span className="mt-0.5 block text-[11.5px] leading-5 text-muted">
                  {page.config.allowFallbackHint}
                </span>
              </span>
            </label>
          </div>

          {form.formState.errors.root?.message ? (
            <p
              role="alert"
              className="border border-danger/25 bg-danger-soft px-3 py-2 text-[12.5px] text-danger sm:col-span-2"
            >
              {form.formState.errors.root.message}
            </p>
          ) : null}

          <div className="flex justify-end sm:col-span-2">
            <Button type="submit" loading={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? page.config.saving : page.config.save}
            </Button>
          </div>
        </form>
      </Section>

      <ModelsSection tokenId={tokenId} models={models} />

      <Section className="mt-10" title={page.danger.title}>
        <div className="flex flex-col gap-3 border border-danger/20 bg-danger-soft/40 px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-[12.5px] leading-5 text-muted">{page.danger.hint}</p>
          <Button variant="danger" size="sm" onClick={() => setDeleteOpen(true)}>
            {page.danger.action}
          </Button>
        </div>
      </Section>

      <Dialog
        open={deleteOpen}
        onOpenChange={(open) => {
          if (!deleting) setDeleteOpen(open)
        }}
        title={page.danger.confirmTitle}
        description={page.danger.confirmBody(token.name || shared.none)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteOpen(false)} disabled={deleting}>
              {shared.cancel}
            </Button>
            <Button variant="danger" loading={deleting} onClick={() => void handleDelete()}>
              {deleting ? page.danger.deleting : page.danger.confirm}
            </Button>
          </>
        }
      >
        <p className="text-[13px] leading-6 text-muted">{page.danger.hint}</p>
        {deleteError ? (
          <p role="alert" className="mt-3 text-[12.5px] text-danger">
            {deleteError}
          </p>
        ) : null}
      </Dialog>
    </>
  )
}

function SellerChips({ ids, onRemove }: { ids: number[]; onRemove: (id: number) => void }) {
  if (ids.length === 0) {
    return <p className="mt-1.5 text-[11.5px] text-muted">{page.sellerIds.empty}</p>
  }
  return (
    <div className="mt-2 flex flex-wrap gap-1.5">
      {ids.map((sellerId) => (
        <span
          key={sellerId}
          className="inline-flex items-center gap-1 border border-line bg-wash px-1.5 py-0.5"
        >
          <SellerId id={sellerId} />
          <button
            type="button"
            className="ml-0.5 grid h-4 w-4 place-items-center rounded-[2px] text-muted hover:bg-surface hover:text-ink"
            aria-label={`${shared.remove} ${sellerId}`}
            onClick={() => onRemove(sellerId)}
          >
            ×
          </button>
        </span>
      ))}
    </div>
  )
}

function ModelsSection({
  tokenId,
  models,
}: {
  tokenId: number
  models: KeyDetail['models']
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const square = useQuery({ ...squareModelsQuery(), enabled: true })
  const [selectedModel, setSelectedModel] = useState('')
  const [cap, setCap] = useState('')
  const [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState<string | null>(null)

  const available = useMemo(() => {
    const attached = new Set(models.map((model) => model.model))
    const seen = new Set<string>()
    return (square.data ?? []).filter((model) => {
      if (attached.has(model.model) || seen.has(model.model)) return false
      seen.add(model.model)
      return true
    })
  }, [models, square.data])

  const effectiveModel = available.some((model) => model.model === selectedModel)
    ? selectedModel
    : available[0]?.model ?? ''

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ['console', 'key', tokenId] })
    await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
  }

  async function handleAdd() {
    if (!effectiveModel) return
    const milli = parseMultiplierToMilli(cap)
    if (milli === null) {
      toast.show({ title: page.errors.priceCapInvalid, tone: 'error' })
      return
    }
    setAdding(true)
    try {
      await addKeyModel(tokenId, effectiveModel, milli)
      toast.show({ title: page.models.added, tone: 'success' })
      setCap('')
      setSelectedModel('')
      await refresh()
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint,
        tone: 'error',
      })
    } finally {
      setAdding(false)
    }
  }

  async function handleRemove(model: string) {
    setRemoving(model)
    try {
      await removeKeyModel(tokenId, model)
      toast.show({ title: page.models.removed, tone: 'success' })
      await refresh()
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint,
        tone: 'error',
      })
    } finally {
      setRemoving(null)
    }
  }

  return (
    <Section className="mt-9" title={page.models.title} hint={page.models.hint}>
      {models.length === 0 ? (
        <Empty
          title={page.models.emptyTitle}
          reason={page.models.emptyReason}
          next={page.models.emptyNext}
        />
      ) : (
        <Table minWidth={620}>
          <TableHead>
            <TableRow interactive={false}>
              <TableHeaderCell>{page.models.columns.model}</TableHeaderCell>
              <TableHeaderCell numeric>{page.models.columns.cap}</TableHeaderCell>
              <TableHeaderCell>{page.models.columns.added}</TableHeaderCell>
              <TableHeaderCell>{page.models.columns.actions}</TableHeaderCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {models.map((model) => (
              <TableRow key={model.model}>
                <TableCell className="font-numeric text-[12.5px]">{model.model}</TableCell>
                <TableCell numeric className="text-[12.5px]">
                  {model.price_cap_milli > 0
                    ? `${milliToMultiplierInput(model.price_cap_milli)}x`
                    : page.models.capFollow}
                </TableCell>
                <TableCell className="whitespace-nowrap font-numeric text-[11.5px] text-muted">
                  {formatDateTime(model.added_at)}
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    loading={removing === model.model}
                    onClick={() => void handleRemove(model.model)}
                  >
                    {page.models.remove}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <div className="mt-4 border-t border-line pt-4">
        <div className="text-[12.5px] font-medium text-ink">{page.models.addTitle}</div>
        {square.isError ? (
          <ErrorBlock
            className="mt-2"
            error={square.error}
            onRetry={() => void square.refetch()}
          />
        ) : square.isPending ? (
          <p className="mt-2 text-[12px] text-muted">{shared.loadingOne}</p>
        ) : available.length === 0 ? (
          <p className="mt-2 text-[12px] text-muted">
            {models.length > 0 ? page.models.allAdded : page.models.noModels}
          </p>
        ) : (
          <div className="mt-2 flex flex-col gap-2 sm:flex-row sm:items-end">
            <Field
              label={page.models.addModel}
              htmlFor="add-key-model"
              className="flex-1"
            >
              <Select
                id="add-key-model"
                value={effectiveModel}
                onChange={(event) => setSelectedModel(event.target.value)}
              >
                {available.map((model) => (
                  <option key={model.model} value={model.model}>
                    {model.model} · {model.vendor}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label={page.models.addCap}
              htmlFor="add-key-model-cap"
              className="sm:w-44"
            >
              <Input
                id="add-key-model-cap"
                mono
                inputMode="decimal"
                placeholder="0.85"
                value={cap}
                onChange={(event) => setCap(event.target.value)}
              />
            </Field>
            <Button
              className="shrink-0"
              loading={adding}
              onClick={() => void handleAdd()}
            >
              {adding ? page.models.adding : page.models.add}
            </Button>
          </div>
        )}
      </div>
    </Section>
  )
}

async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Fall through to the legacy path.
  }
  try {
    const area = document.createElement('textarea')
    area.value = text
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.select()
    const copied = document.execCommand('copy')
    area.remove()
    return copied
  } catch {
    return false
  }
}
