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
  Input,
  Tag,
  buttonClasses,
  useToast,
} from '@/ui'

import {
  addKeyModel,
  currentIPQuery,
  deleteKey,
  keyDetailQuery,
  removeKeyModel,
  revealTokenKey,
  saveKeyPref,
  saveKeyTokenLimits,
  setTokenStatus,
} from '@/features/console/api'
import {
  ErrorBlock,
  PageSkeleton,
  Section,
} from '@/features/console/components/DataState'
import { Field, Notice } from '@/features/console/components/form'
import { KeyRoutingFields } from '@/features/console/components/KeyRoutingFields'
import {
  dateTimeInputToUnix,
  milliToMultiplierInput,
  unixToDateTimeInput,
} from '@/features/console/format'
import { isTokenExpired, tokenStatusMeta } from '@/features/console/labels'
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
  TOKEN_STATUS_EXPIRED,
  type KeyDetail,
  type TokenRecord,
} from '@/features/console/types'
import { squareModelsQuery } from '@/features/market/api'
import { ModelPicker, type ModelSelection } from '@/features/market/ModelPicker'

export const Route = createFileRoute('/console/keys/$id')({
  component: KeyDetailPage,
})

const page = copy.pages.consoleKeyDetail
const shared = copy.pages.consoleShared

export function KeyDetailPage() {
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
      className="inline-flex items-center gap-1 text-meta text-ink-2 transition-colors hover:text-ink"
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
  const status = tokenStatusMeta(isTokenExpired(token) ? TOKEN_STATUS_EXPIRED : token.status)
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
            <h1 className="text-h2 font-semibold leading-tight tracking-tight text-ink">
              {token.name || shared.none}
            </h1>
            <Tag tone={status.tone} dot>
              {status.label}
            </Tag>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-meta text-ink-3">
            <span>{page.maskedKey}</span>
            <code className="glass-inset rounded-sm px-2 py-0.5 text-meta tabular-nums text-ink">
              {token.key}
            </code>
            <span>{page.copyKeyHint}</span>
          </div>
          <div className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-meta text-ink-3">
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
              className="border border-danger/25 bg-danger/10 px-3 py-2 text-meta text-danger sm:col-span-2"
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

      <KeyLimitsSection tokenId={tokenId} token={token} />

      <OfficialOnlyModels pref={pref} />

      <ModelsSection
        tokenId={tokenId}
        models={models}
        modelScope={pref?.model_scope ?? 'selected'}
        pref={pref}
      />

      <Section className="mt-10" title={page.danger.title}>
        <div className="flex flex-col gap-3 rounded-md border border-danger/20 bg-danger/5 px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-meta leading-5 text-ink-3">{page.danger.hint}</p>
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
        <p className="text-meta leading-6 text-ink-3">{page.danger.hint}</p>
        {deleteError ? (
          <p role="alert" className="mt-3 text-meta text-danger">
            {deleteError}
          </p>
        ) : null}
      </Dialog>
    </>
  )
}


/**
 * Warns when the key's official-only switch cannot reach some of its models.
 * Data comes from the square list (official_available), which the backend
 * computes in the same pooled queries as listing_count.
 */
function OfficialOnlyModels({ pref }: { pref: KeyDetail['pref'] }) {
  const officialOnly = Boolean(pref?.official_only)
  const square = useQuery({ ...squareModelsQuery(), enabled: officialOnly })
  if (!pref || !officialOnly || !square.data) return null

  const scope =
    pref.model_scope === 'selected'
      ? new Set(pref.models.map((model) => model.model))
      : null
  const blocked = square.data
    .filter(
      (model) =>
        (scope === null || scope.has(model.model)) &&
        // Supply exists, but none of it is on an official channel. Models
        // with no supply at all fail for a different reason and would only
        // make this notice noisy.
        model.listing_count > 0 &&
        model.official_available === false,
    )
    .map((model) => model.model)
  if (blocked.length === 0) return null

  return (
    <Notice className="mt-9" tone="warning" title={page.officialOnly.title}>
      <p>{page.officialOnly.body}</p>
      <p className="mt-1 font-mono text-meta">{blocked.join('、')}</p>
      <Link
        to="/models"
        className="mt-2 inline-block font-medium underline underline-offset-2"
      >
        {page.officialOnly.action}
      </Link>
    </Notice>
  )
}

function KeyLimitsSection({ tokenId, token }: { tokenId: number; token: TokenRecord }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const currentIp = useQuery({ ...currentIPQuery() })
  const [never, setNever] = useState(token.expired_time <= 0)
  const [expiresAt, setExpiresAt] = useState(unixToDateTimeInput(token.expired_time))
  const [allowIps, setAllowIps] = useState(token.allow_ips ?? '')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setNever(token.expired_time <= 0)
    setExpiresAt(unixToDateTimeInput(token.expired_time))
    setAllowIps(token.allow_ips ?? '')
  }, [token.expired_time, token.allow_ips])

  function insertCurrentIP() {
    const ip = currentIp.data?.ip?.trim()
    if (!ip) return
    setAllowIps((previous) => {
      const lines = previous
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean)
      if (lines.includes(ip)) return previous
      return lines.length ? `${lines.join('\n')}\n${ip}` : ip
    })
  }

  async function copyCurrentIP() {
    const ip = currentIp.data?.ip?.trim()
    if (!ip) return
    const ok = await writeClipboard(ip)
    toast.show({
      title: ok ? shared.copied : shared.copyFailed,
      tone: ok ? 'success' : 'error',
    })
  }

  async function save() {
    let expiredTime = -1
    if (!never) {
      const parsed = dateTimeInputToUnix(expiresAt)
      if (parsed === null || parsed <= Math.floor(Date.now() / 1000)) {
        toast.show({ title: page.limits.expiryInvalid, tone: 'error' })
        return
      }
      expiredTime = parsed
    }
    setSaving(true)
    try {
      await saveKeyTokenLimits({
        token_id: tokenId,
        expired_time: expiredTime,
        allow_ips: allowIps,
      })
      toast.show({ title: page.limits.saved, tone: 'success' })
      await queryClient.invalidateQueries({ queryKey: ['console', 'key', tokenId] })
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
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
    <Section className="mt-9" title={page.limits.title} hint={page.limits.hint}>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label={page.limits.expiry}
          htmlFor="key-limits-expiry"
          hint={page.limits.expiryHint}
        >
          <Input
            id="key-limits-expiry"
            type="datetime-local"
            disabled={never}
            value={expiresAt}
            onChange={(event) => setExpiresAt(event.target.value)}
          />
          <label className="mt-2 inline-flex items-center gap-2 text-meta text-ink">
            <input
              type="checkbox"
              className="h-3.5 w-3.5 accent-brand"
              checked={never}
              onChange={(event) => {
                setNever(event.target.checked)
                if (event.target.checked) setExpiresAt('')
              }}
            />
            {page.limits.never}
          </label>
        </Field>

        <Field
          label={page.limits.allowIps}
          htmlFor="key-limits-allow-ips"
          hint={page.limits.allowIpsHint}
        >
          {currentIp.data?.ip ? (
            <div className="mb-1.5 flex flex-wrap items-center gap-2 text-caption text-ink-3">
              <span className="font-mono">
                {copy.pages.consoleKeys.currentIp.label(currentIp.data.ip)}
              </span>
              <button
                type="button"
                className="text-brand-ink underline-offset-2 hover:underline"
                onClick={insertCurrentIP}
              >
                {copy.pages.consoleKeys.currentIp.insert}
              </button>
              <button
                type="button"
                className="text-brand-ink underline-offset-2 hover:underline"
                onClick={() => void copyCurrentIP()}
              >
                {copy.pages.consoleKeys.currentIp.copy}
              </button>
            </div>
          ) : null}
          <textarea
            id="key-limits-allow-ips"
            rows={3}
            spellCheck={false}
            className="w-full resize-y rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-3 py-2 font-mono text-meta text-ink transition-colors placeholder:text-ink-3 focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20"
            placeholder={page.limits.allowIpsPlaceholder}
            value={allowIps}
            onChange={(event) => setAllowIps(event.target.value)}
          />
        </Field>
      </div>
      <div className="mt-4 flex justify-end">
        <Button loading={saving} onClick={() => void save()}>
          {saving ? page.limits.saving : page.limits.save}
        </Button>
      </div>
    </Section>
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
          <div className="text-body font-medium text-ink">{page.models.scopeLabel}</div>
          <div className="mt-2 inline-flex glass-inset p-0.5">
            <ScopeButton active={scope === 'all'} onClick={() => setScope('all')} label={page.models.allScopeTitle} />
            <ScopeButton
              active={scope === 'selected'}
              onClick={() => setScope('selected')}
              label={copy.pages.consoleKeys.createDialog.scope.selected}
            />
          </div>
        </div>
        <span className="tabular-nums text-meta text-ink-3">
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
      className={`rounded-sm px-3 py-1.5 text-meta font-medium transition-colors ${
        active
          ? 'bg-white/90 text-brand-ink shadow-sm'
          : 'text-ink-3 hover:text-ink'
      }`}
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
