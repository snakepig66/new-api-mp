import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatYuan } from '@/lib/money'
import {
  Button,
  Dialog,
  Empty,
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
import { Notice } from '@/features/console/components/form'
import { KeyRoutingFields } from '@/features/console/components/KeyRoutingFields'
import { milliToMultiplierInput } from '@/features/console/format'
import { tokenStatusMeta } from '@/features/console/labels'
import {
  buildModelSelection,
  prefFormSchema,
  prefFormToInput,
  prefToFormValues,
  type PrefFormValues,
} from '@/features/console/schemas'
import {
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  type KeyDetail,
} from '@/features/console/types'
import { ModelPicker, type ModelSelection } from '@/features/market/ModelPicker'

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
      className="inline-flex items-center gap-1 text-[13px] text-ink-2 transition-colors hover:text-ink"
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
          <div className="mt-2 flex flex-wrap items-center gap-2 text-[13px] text-ink-3">
            <span>{page.maskedKey}</span>
            <code className="rounded-sm bg-bg-soft px-2 py-0.5 text-[13px] tabular-nums text-ink">
              {token.key}
            </code>
            <span>{page.copyKeyHint}</span>
          </div>
          <div className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-[13px] text-ink-3">
            <span>
              {copy.pages.consoleKeys.columns.spend}：{copy.pages.consoleKeys.spend.used(formatYuan(token.used_quota))}
            </span>
            <span>
              {copy.pages.consoleKeys.columns.models}：
              {pref?.model_scope === 'all'
                ? page.models.allScopeTitle
                : copy.pages.consoleKeys.modelsUnit(models.length)}
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
          <KeyRoutingFields form={form} idPrefix="pref" />

          {form.formState.errors.root?.message ? (
            <p
              role="alert"
              className="border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] text-danger sm:col-span-2"
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

      <ModelsSection
        tokenId={tokenId}
        models={models}
        modelScope={pref?.model_scope ?? 'selected'}
        pref={pref}
      />

      <Section className="mt-10" title={page.danger.title}>
        <div className="flex flex-col gap-3 rounded-md border border-danger/20 bg-danger/5 px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-[13px] leading-5 text-ink-3">{page.danger.hint}</p>
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
            <Button variant="secondary" onClick={() => setDeleteOpen(false)} disabled={deleting}>
              {shared.cancel}
            </Button>
            <Button variant="danger" loading={deleting} onClick={() => void handleDelete()}>
              {deleting ? page.danger.deleting : page.danger.confirm}
            </Button>
          </>
        }
      >
        <p className="text-[13px] leading-6 text-ink-3">{page.danger.hint}</p>
        {deleteError ? (
          <p role="alert" className="mt-3 text-[13px] text-danger">
            {deleteError}
          </p>
        ) : null}
      </Dialog>
    </>
  )
}


function ModelsSection({
  tokenId,
  models,
  modelScope,
  pref,
}: {
  tokenId: number
  models: KeyDetail['models']
  modelScope: 'all' | 'selected'
  pref: KeyDetail['pref']
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [scope, setScope] = useState<'all' | 'selected'>(modelScope)
  const [selection, setSelection] = useState<ModelSelection>(() => modelsToSelection(models))
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setScope(modelScope)
  }, [modelScope])

  useEffect(() => {
    setSelection(modelsToSelection(models))
  }, [models])

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ['console', 'key', tokenId] })
    await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
  }

  async function save() {
    let next = new Map<string, number>()
    if (scope === 'selected') {
      const parsed = buildModelSelection(selection)
      if (!parsed.ok) {
        toast.show({
          title: parsed.error === page.models.capInvalid ? page.models.capInvalid : page.models.required,
          tone: 'error',
        })
        return
      }
      next = new Map(parsed.models.map((model) => [model.model, model.price_cap_milli]))
    }

    setSaving(true)
    try {
      if (scope !== modelScope || !pref) {
        await saveKeyPref({
          ...prefFormToInput(tokenId, prefToFormValues(pref)),
          model_scope: scope,
        })
      }
      if (scope === 'selected') {
        const current = new Map(models.map((model) => [model.model, model.price_cap_milli]))
        for (const model of current.keys()) {
          if (!next.has(model)) await removeKeyModel(tokenId, model)
        }
        for (const [model, cap] of next) {
          const had = current.get(model)
          if (had === cap) continue
          if (had !== undefined) await removeKeyModel(tokenId, model)
          await addKeyModel(tokenId, model, cap)
        }
      }
      toast.show({ title: page.models.saved, tone: 'success' })
      await refresh()
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint,
        tone: 'error',
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Section className="mt-9" title={page.models.title} hint={page.models.hint}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-[14px] font-medium text-ink">{page.models.scopeLabel}</div>
          <div className="mt-2 inline-flex rounded-md border border-line bg-bg-soft p-0.5">
            <ScopeButton active={scope === 'all'} onClick={() => setScope('all')} label={page.models.allScopeTitle} />
            <ScopeButton
              active={scope === 'selected'}
              onClick={() => setScope('selected')}
              label={copy.pages.consoleKeys.createDialog.scope.selected}
            />
          </div>
        </div>
        <span className="tabular-nums text-[13px] text-ink-3">
          {scope === 'all'
            ? copy.pages.consoleKeys.createDialog.scope.all
            : copy.pages.consoleKeys.modelsUnit(models.length)}
        </span>
      </div>

      {scope === 'all' ? (
        <Notice className="mt-4" title={page.models.allScopeTitle}>
          {page.models.allScopeHint}
        </Notice>
      ) : (
        <>
          <div className="mt-4">
            <ModelPicker
              selection={selection}
              onChange={setSelection}
              idPrefix={`key-${tokenId}`}
            />
          </div>
          <div className="mt-4 flex justify-end">
            <Button loading={saving} onClick={() => void save()}>
              {saving ? page.models.saving : page.models.save}
            </Button>
          </div>
        </>
      )}
    </Section>
  )
}

function ScopeButton({
  active,
  onClick,
  label,
}: {
  active: boolean
  onClick: () => void
  label: string
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={
        active
          ? 'rounded-sm bg-bg px-3 py-1.5 text-[13px] font-medium text-ink shadow-sm'
          : 'rounded-sm px-3 py-1.5 text-[13px] text-ink-3 hover:text-ink'
      }
    >
      {label}
    </button>
  )
}

function modelsToSelection(rows: KeyDetail['models']): ModelSelection {
  const selection: ModelSelection = {}
  for (const model of rows) {
    selection[model.model] = milliToMultiplierInput(model.price_cap_milli)
  }
  return selection
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
