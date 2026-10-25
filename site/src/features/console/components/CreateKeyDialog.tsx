import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/cn'
import { squareModelsQuery, type SquareModel } from '@/features/market/api'
import { Button, Dialog, Empty, Input, Tag, useToast } from '@/ui'

import { consoleKeysQuery, createKey } from '../api'
import {
  microToYuanInput,
  milliToMultiplierInput,
  parseYuanToMicro,
} from '../format'
import {
  buildModelSelection,
  prefFormSchema,
  prefFormToInput,
  prefToFormValues,
  spendCapSchema,
  type PrefFormValues,
} from '../schemas'
import type { CreateKeyResult } from '../types'
import { ErrorBlock, Skeleton } from './DataState'
import { Field } from './form'
import { KeyRoutingFields } from './KeyRoutingFields'

const page = copy.pages.consoleKeys
const dialogCopy = page.createDialog
const shared = copy.pages.consoleShared

type PresetKey = 'balanced' | 'cheapest' | 'stable' | 'selected' | 'custom'

const defaultPref: PrefFormValues = {
  strategy: 'balanced',
  modelScope: 'all',
  priceCap: '',
  rpm: '',
  prefer: '',
  block: '',
  only: '',
  allowFallback: false,
  officialOnly: false,
}

const presetDefaults: Record<Exclude<PresetKey, 'custom'>, PrefFormValues> = {
  balanced: { ...defaultPref },
  cheapest: { ...defaultPref, strategy: 'cheapest' },
  stable: { ...defaultPref, strategy: 'stable', officialOnly: true },
  selected: { ...defaultPref, modelScope: 'selected' },
}

export interface CreateKeyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (tokenId: number) => void
  onOpenDetail?: (tokenId: number) => void
}

export function CreateKeyDialog({
  open,
  onOpenChange,
  onCreated,
  onOpenDetail,
}: CreateKeyDialogProps) {
  const toast = useToast()
  const modelsQuery = useQuery({ ...squareModelsQuery(), enabled: open })
  const keysQuery = useQuery({ ...consoleKeysQuery(), enabled: open })

  const form = useForm<PrefFormValues>({
    resolver: zodResolver(prefFormSchema),
    defaultValues: defaultPref,
  })
  const values = form.watch()

  const [name, setName] = useState('')
  const [spendCap, setSpendCap] = useState('')
  const [search, setSearch] = useState('')
  const [selection, setSelection] = useState<Record<string, string>>({})
  const [copyFrom, setCopyFrom] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [created, setCreated] = useState<CreateKeyResult | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!open) return
    form.reset(defaultPref)
    setName('')
    setSpendCap('')
    setSearch('')
    setSelection({})
    setCopyFrom('')
    setError(null)
    setSubmitting(false)
    setCreated(null)
    setCopied(false)
  }, [form, open])

  const models = modelsQuery.data ?? []
  const filtered = useMemo(() => {
    const keyword = search.trim().toLowerCase()
    if (!keyword) return models
    return models.filter(
      (model) =>
        model.model.toLowerCase().includes(keyword) ||
        model.vendor.toLowerCase().includes(keyword),
    )
  }, [models, search])

  const preset = derivePreset(values, spendCap)
  const selectedCount = Object.keys(selection).length

  function applyPreset(next: Exclude<PresetKey, 'custom'>) {
    form.reset(presetDefaults[next])
    setSpendCap('')
    setError(null)
    if (next !== 'selected') {
      setSelection({})
    }
  }

  function toggleModel(model: string) {
    setSelection((current) => {
      if (model in current) {
        const next = { ...current }
        delete next[model]
        return next
      }
      return { ...current, [model]: '' }
    })
  }

  function setCap(model: string, value: string) {
    setSelection((current) => ({ ...current, [model]: value }))
  }

  function selectAll() {
    setSelection((current) => {
      const next = { ...current }
      for (const model of filtered) {
        if (!(model.model in next)) next[model.model] = ''
      }
      return next
    })
  }

  function applyCopySettings(rawID: string) {
    setCopyFrom(rawID)
    const id = Number(rawID)
    if (!id) return
    const item = keysQuery.data?.find((entry) => entry.token.id === id)
    if (!item) return
    form.reset(prefToFormValues(item.pref))
    const nextSelection: Record<string, string> = {}
    for (const model of item.pref?.models ?? []) {
      nextSelection[model.model] = milliToMultiplierInput(model.price_cap_milli)
    }
    setSelection(nextSelection)
    setSpendCap(
      item.token.unlimited_quota ? '' : microToYuanInput(item.token.remain_quota),
    )
    setError(null)
  }

  async function submit() {
    setError(null)
    if (name.trim().length > 50) {
      setError(dialogCopy.errors.nameTooLong)
      return
    }
    const valid = await form.trigger()
    if (!valid) return
    const current = form.getValues()

    let modelsPayload: { model: string; price_cap_milli: number }[] = []
    if (current.modelScope === 'selected') {
      const result = buildModelSelection(selection)
      if (!result.ok) {
        setError(dialogCopy.errors.modelsRequired)
        return
      }
      modelsPayload = result.models
    }
    const spendCheck = spendCapSchema.safeParse(spendCap)
    if (!spendCheck.success) {
      setError(dialogCopy.errors.spendInvalid)
      return
    }
    const prefPayload = prefFormToInput(0, current)

    setSubmitting(true)
    try {
      const result = await createKey({
        name: name.trim(),
        strategy: current.strategy,
        model_scope: current.modelScope,
        spend_cap_micro: spendCap.trim() ? parseYuanToMicro(spendCap) : null,
        price_cap_milli: prefPayload.price_cap_milli,
        rpm: prefPayload.rpm,
        prefer_seller_ids: prefPayload.prefer_seller_ids,
        block_seller_ids: prefPayload.block_seller_ids,
        only_seller_id: prefPayload.only_seller_id,
        allow_fallback: prefPayload.allow_fallback,
        official_only: prefPayload.official_only,
        models: modelsPayload,
      })
      onCreated(result.id)
      setCreated(result)
      toast.show({ title: dialogCopy.successTitle, tone: 'success' })
    } catch (submitError) {
      setError(
        submitError instanceof ApiError ? submitError.message : dialogCopy.errors.createFailed,
      )
    } finally {
      setSubmitting(false)
    }
  }

  async function copyKey() {
    if (!created) return
    const ok = await writeClipboard(created.key)
    setCopied(ok)
    toast.show({
      title: ok ? shared.copied : shared.copyFailed,
      tone: ok ? 'success' : 'error',
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!submitting) onOpenChange(next)
      }}
      title={created ? dialogCopy.successTitle : dialogCopy.title}
      description={created ? dialogCopy.successHint : dialogCopy.description}
      className="max-w-3xl"
      footer={
        created ? (
          <div className="flex w-full flex-wrap items-center justify-between gap-2">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              {dialogCopy.done}
            </Button>
            <div className="flex items-center gap-2">
              <Button variant="secondary" onClick={() => void copyKey()}>
                {copied ? shared.copied : dialogCopy.copyKey}
              </Button>
              {onOpenDetail ? (
                <Button
                  onClick={() => {
                    onOpenDetail(created.id)
                    onOpenChange(false)
                  }}
                >
                  {dialogCopy.viewDetail}
                </Button>
              ) : null}
            </div>
          </div>
        ) : (
          <div className="flex w-full items-center justify-between gap-2">
            <Button
              variant="secondary"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              {shared.cancel}
            </Button>
            <Button onClick={() => void submit()} loading={submitting}>
              {submitting ? dialogCopy.creating : dialogCopy.create}
            </Button>
          </div>
        )
      }
    >
      {created ? (
        <CreatedKey result={created} onCopy={() => void copyKey()} copied={copied} />
      ) : (
        <div className="space-y-5">
          {error ? (
            <p
              role="alert"
              className="border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] leading-5 text-danger"
            >
              {error}
            </p>
          ) : null}

          <Field label={dialogCopy.nameLabel} htmlFor="create-key-name" hint={dialogCopy.nameHint}>
            <Input
              id="create-key-name"
              autoFocus
              maxLength={50}
              placeholder={dialogCopy.namePlaceholder}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>

          <div>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-[14px] font-medium text-ink">{dialogCopy.presetLabel}</span>
              {preset === 'custom' ? <Tag tone="neutral">{dialogCopy.custom}</Tag> : null}
            </div>
            <div className="mt-2 grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {(['balanced', 'cheapest', 'stable', 'selected'] as const).map((key) => {
                const presetCopy = dialogCopy.presets[key]
                const active = preset === key
                return (
                  <button
                    key={key}
                    type="button"
                    aria-pressed={active}
                    onClick={() => applyPreset(key)}
                    className={cn(
                      'rounded-md border px-3 py-3 text-left transition-colors',
                      active
                        ? 'border-brand bg-brand-soft'
                        : 'border-line bg-bg hover:border-line-strong hover:bg-bg-soft/60',
                    )}
                  >
                    <span className="flex items-center justify-between gap-2">
                      <span className="text-[13px] font-medium text-ink">{presetCopy.label}</span>
                      {active ? <Tag tone="brand">{shared.confirm}</Tag> : null}
                    </span>
                    <span className="mt-1 block text-[13px] leading-5 text-ink-3">
                      {presetCopy.hint}
                    </span>
                  </button>
                )
              })}
            </div>
            {preset === 'custom' ? (
              <p className="mt-2 text-[13px] text-ink-3">{dialogCopy.changePreset}</p>
            ) : null}
          </div>

          {values.modelScope === 'selected' ? (
            <ModelPicker
              models={filtered}
              allModels={models}
              pending={modelsQuery.isPending}
              error={modelsQuery.error}
              onRetry={() => void modelsQuery.refetch()}
              search={search}
              onSearch={setSearch}
              selection={selection}
              onToggle={toggleModel}
              onCap={setCap}
              onSelectAll={selectAll}
              onClear={() => setSelection({})}
            />
          ) : null}

          <details className="rounded-md border border-line">
            <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-[14px] font-medium text-ink">
              <span>
                <span aria-hidden="true" className="mr-1.5 text-ink-3">
                  ▸
                </span>
                {dialogCopy.advanced}
              </span>
              <span className="hidden text-[13px] font-normal text-ink-3 sm:inline">
                {dialogCopy.advancedHint}
              </span>
            </summary>
            <div className="space-y-4 border-t border-line px-4 py-4">
              <Field
                label={dialogCopy.copyFrom.label}
                htmlFor="create-key-copy-from"
                hint={dialogCopy.copyFrom.hint}
              >
                <select
                  id="create-key-copy-from"
                  className="h-11 w-full rounded-md border border-transparent bg-bg-soft px-3 text-[14px] text-ink"
                  value={copyFrom}
                  onChange={(event) => applyCopySettings(event.target.value)}
                >
                  <option value="">{dialogCopy.copyFrom.none}</option>
                  {(keysQuery.data ?? []).map((item) => (
                    <option key={item.token.id} value={String(item.token.id)}>
                      {item.token.name || `Key ${item.token.id}`}
                    </option>
                  ))}
                </select>
              </Field>

              <div className="grid gap-4 sm:grid-cols-2">
                <KeyRoutingFields form={form} idPrefix="create" />
                <Field
                  label={dialogCopy.spendLabel}
                  htmlFor="create-key-spend"
                  hint={dialogCopy.spendHint}
                >
                  <div className="flex items-center gap-2">
                    <Input
                      id="create-key-spend"
                      inputMode="decimal"
                      placeholder={dialogCopy.spendPlaceholder}
                      value={spendCap}
                      onChange={(event) => setSpendCap(event.target.value)}
                    />
                    <span className="shrink-0 text-[13px] text-ink-3">元</span>
                  </div>
                </Field>
              </div>

              {values.modelScope === 'selected' && selectedCount > 0 ? (
                <p className="text-[13px] text-ink-3">
                  {dialogCopy.models.selected(selectedCount)}
                </p>
              ) : null}
            </div>
          </details>
        </div>
      )}
    </Dialog>
  )
}

function derivePreset(values: PrefFormValues, spendCap: string): PresetKey {
  if (spendCap.trim()) return 'custom'
  const restEmpty =
    !values.priceCap.trim() &&
    !values.rpm.trim() &&
    !values.prefer.trim() &&
    !values.block.trim() &&
    !values.only.trim() &&
    !values.allowFallback
  if (!restEmpty) return 'custom'

  if (values.modelScope === 'selected') {
    return values.strategy === 'balanced' && !values.officialOnly ? 'selected' : 'custom'
  }
  if (values.officialOnly) {
    return values.strategy === 'stable' ? 'stable' : 'custom'
  }
  if (values.strategy === 'balanced') return 'balanced'
  if (values.strategy === 'cheapest') return 'cheapest'
  return 'custom'
}

function CreatedKey({
  result,
  onCopy,
  copied,
}: {
  result: CreateKeyResult
  onCopy: () => void
  copied: boolean
}) {
  return (
    <div className="space-y-4">
      <div className="rounded-md border border-brand/30 bg-brand-soft px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <div className="text-[13px] text-ink-3">{dialogCopy.successTitle}</div>
            <div className="mt-0.5 text-[15px] font-medium text-ink">{result.name}</div>
          </div>
          <Button size="sm" onClick={onCopy}>
            {copied ? shared.copied : dialogCopy.copyKey}
          </Button>
        </div>
        <code className="mt-3 block break-all rounded-sm bg-bg px-3 py-2 font-mono text-[13px] text-ink">
          {result.key}
        </code>
      </div>
      <div className="grid gap-2 text-[13px] text-ink-3 sm:grid-cols-2">
        <span>
          {copy.pages.consoleKeys.columns.models}：
          {result.model_scope === 'all'
            ? dialogCopy.scope.all
            : page.modelsUnit(result.models.length)}
        </span>
        <span>
          {copy.pages.consoleKeys.columns.strategy}：
          {copy.pages.consoleKeys.strategy[result.strategy]}
        </span>
      </div>
    </div>
  )
}

function ModelPicker({
  models,
  allModels,
  pending,
  error,
  onRetry,
  search,
  onSearch,
  selection,
  onToggle,
  onCap,
  onSelectAll,
  onClear,
}: {
  models: SquareModel[]
  allModels: SquareModel[]
  pending: boolean
  error: unknown
  onRetry: () => void
  search: string
  onSearch: (value: string) => void
  selection: Record<string, string>
  onToggle: (model: string) => void
  onCap: (model: string, value: string) => void
  onSelectAll: () => void
  onClear: () => void
}) {
  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-[14px] font-medium text-ink">{dialogCopy.models.label}</div>
          <p className="mt-0.5 text-[13px] text-ink-3">{dialogCopy.models.hint}</p>
        </div>
        <span className="tabular-nums text-[13px] text-brand-ink">
          {dialogCopy.models.selected(Object.keys(selection).length)}
        </span>
      </div>

      <div className="mt-2 flex flex-wrap gap-2">
        <Input
          aria-label={dialogCopy.models.search}
          placeholder={dialogCopy.models.search}
          className="min-w-[140px] flex-1"
          value={search}
          onChange={(event) => onSearch(event.target.value)}
        />
        <Button variant="secondary" size="md" className="shrink-0" onClick={onSelectAll}>
          {dialogCopy.models.selectAll}
        </Button>
        <Button variant="ghost" size="md" className="shrink-0" onClick={onClear}>
          {dialogCopy.models.clear}
        </Button>
      </div>

      <div className="mt-2 max-h-56 overflow-y-auto rounded-md border border-line">
        {pending ? (
          <div className="space-y-2 p-3">
            <Skeleton className="h-5" />
            <Skeleton className="h-5" />
            <Skeleton className="h-5" />
          </div>
        ) : error ? (
          <ErrorBlock className="m-3" error={error} onRetry={onRetry} />
        ) : models.length === 0 ? (
          <Empty
            className="border-0"
            title={allModels.length === 0 ? dialogCopy.models.empty : undefined}
            reason={allModels.length === 0 ? '' : dialogCopy.models.noMatch}
          />
        ) : (
          <ul className="divide-y divide-line">
            {models.map((model) => {
              const checked = model.model in selection
              return (
                <li
                  key={model.model}
                  className={cn(
                    'flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2 transition-colors',
                    checked ? 'bg-brand-soft/50' : 'bg-bg hover:bg-bg-soft/60',
                  )}
                >
                  <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2.5">
                    <input
                      type="checkbox"
                      className="h-3.5 w-3.5 shrink-0 accent-brand"
                      checked={checked}
                      onChange={() => onToggle(model.model)}
                    />
                    <span className="min-w-0">
                      <span className="block truncate text-[13px] text-ink">{model.model}</span>
                      <span className="block text-[13px] text-ink-3">
                        {model.vendor} · {model.channel_count} 个渠道
                      </span>
                    </span>
                  </label>
                  <label className="flex shrink-0 items-center gap-1.5 text-[13px] text-ink-3">
                    <span className="whitespace-nowrap">{page.wizard.models.capLabel}</span>
                    <input
                      type="text"
                      inputMode="decimal"
                      disabled={!checked}
                      aria-label={`${model.model} ${page.wizard.models.capLabel}`}
                      placeholder={dialogCopy.models.capPlaceholder}
                      value={selection[model.model] ?? ''}
                      onChange={(event) => onCap(model.model, event.target.value)}
                      className="h-8 w-32 rounded-sm border border-transparent bg-bg-soft px-2 tabular-nums text-[13px] text-ink disabled:bg-bg-soft disabled:text-ink-3"
                    />
                  </label>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </div>
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
