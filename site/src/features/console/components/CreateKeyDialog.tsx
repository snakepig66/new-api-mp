import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/cn'
import { ModelPicker, type ModelSelection } from '@/features/market/ModelPicker'
import { Button, Dialog, Input, useToast } from '@/ui'

import { consoleKeysQuery, createKey } from '../api'
import {
  dateTimeInputToUnix,
  microToYuanInput,
  milliToMultiplierInput,
  parseYuanToMicro,
  unixToDateTimeInput,
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
import { Field } from './form'
import { KeyRoutingFields } from './KeyRoutingFields'

const page = copy.pages.consoleKeys
const dialogCopy = page.createDialog
const scopeCopy = dialogCopy.scope
const shared = copy.pages.consoleShared

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
  const queryClient = useQueryClient()
  const keysQuery = useQuery({ ...consoleKeysQuery(), enabled: open })

  const form = useForm<PrefFormValues>({
    resolver: zodResolver(prefFormSchema),
    defaultValues: defaultPref,
  })
  const values = form.watch()

  const [name, setName] = useState('')
  const [spendCap, setSpendCap] = useState('')
  const [selection, setSelection] = useState<ModelSelection>({})
  const [copyFrom, setCopyFrom] = useState('')
  const [neverExpires, setNeverExpires] = useState(true)
  const [expiresAt, setExpiresAt] = useState('')
  const [allowIps, setAllowIps] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [created, setCreated] = useState<CreateKeyResult | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!open) return
    form.reset(defaultPref)
    setName('')
    setSpendCap('')
    setSelection({})
    setCopyFrom('')
    setNeverExpires(true)
    setExpiresAt('')
    setAllowIps('')
    setError(null)
    setSubmitting(false)
    setCreated(null)
    setCopied(false)
  }, [form, open])

  const scope = values.modelScope
  const selectedCount = Object.keys(selection).length

  function setScope(next: 'all' | 'selected') {
    form.setValue('modelScope', next, { shouldDirty: true })
    setError(null)
  }

  function applyCopySettings(rawID: string) {
    setCopyFrom(rawID)
    const id = Number(rawID)
    if (!id) return
    const item = keysQuery.data?.find((entry) => entry.token.id === id)
    if (!item) return
    form.reset(prefToFormValues(item.pref))
    const nextSelection: ModelSelection = {}
    for (const model of item.pref?.models ?? []) {
      nextSelection[model.model] = milliToMultiplierInput(model.price_cap_milli)
    }
    setSelection(nextSelection)
    setSpendCap(
      item.token.unlimited_quota ? '' : microToYuanInput(item.token.remain_quota),
    )
    if (item.token.expired_time > 0) {
      setNeverExpires(false)
      setExpiresAt(unixToDateTimeInput(item.token.expired_time))
    } else {
      setNeverExpires(true)
      setExpiresAt('')
    }
    setAllowIps(item.token.allow_ips ?? '')
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
    let expiredTime = -1
    if (!neverExpires) {
      const parsed = dateTimeInputToUnix(expiresAt)
      if (parsed === null || parsed <= Math.floor(Date.now() / 1000)) {
        setError(dialogCopy.errors.expiryInvalid)
        return
      }
      expiredTime = parsed
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
        expired_time: expiredTime,
        allow_ips: allowIps,
        models: modelsPayload,
      })
      onCreated(result.id)
      void queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
      void queryClient.invalidateQueries({ queryKey: ['tokens', 'list'] })
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
            <div className="text-[14px] font-medium text-ink">{scopeCopy.label}</div>
            <div className="mt-2 inline-flex rounded-md border border-line bg-bg-soft p-0.5">
              <ScopeButton
                active={scope === 'all'}
                onClick={() => setScope('all')}
                label={scopeCopy.all}
              />
              <ScopeButton
                active={scope === 'selected'}
                onClick={() => setScope('selected')}
                label={scopeCopy.selected}
              />
            </div>
            <p className="mt-1.5 text-[13px] leading-5 text-ink-3">{scopeCopy.hint}</p>
          </div>

          {scope === 'selected' ? (
            <ModelPicker selection={selection} onChange={setSelection} idPrefix="create-key" />
          ) : null}

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
                <Field
                  label={dialogCopy.expiry.label}
                  htmlFor="create-key-expiry"
                  hint={dialogCopy.expiry.hint}
                >
                  <Input
                    id="create-key-expiry"
                    type="datetime-local"
                    disabled={neverExpires}
                    value={expiresAt}
                    onChange={(event) => setExpiresAt(event.target.value)}
                  />
                  <label className="mt-2 inline-flex items-center gap-2 text-[13px] text-ink">
                    <input
                      type="checkbox"
                      className="h-3.5 w-3.5 accent-brand"
                      checked={neverExpires}
                      onChange={(event) => {
                        setNeverExpires(event.target.checked)
                        if (event.target.checked) setExpiresAt('')
                      }}
                    />
                    {dialogCopy.expiry.never}
                  </label>
                </Field>

                <Field
                  label={dialogCopy.allowIps.label}
                  htmlFor="create-key-allow-ips"
                  hint={dialogCopy.allowIps.hint}
                >
                  <textarea
                    id="create-key-allow-ips"
                    rows={3}
                    spellCheck={false}
                    className="w-full resize-y rounded-md border border-transparent bg-bg-soft px-3 py-2 font-mono text-[13px] text-ink transition-colors placeholder:text-ink-3 focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft"
                    placeholder={dialogCopy.allowIps.placeholder}
                    value={allowIps}
                    onChange={(event) => setAllowIps(event.target.value)}
                  />
                </Field>
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                <KeyRoutingFields form={form} idPrefix="create-key" showScope={false} />
              </div>

              {scope === 'selected' && selectedCount > 0 ? (
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
      className={cn(
        'rounded-sm px-3 py-1.5 text-[13px] transition-colors',
        active ? 'bg-bg font-medium text-ink shadow-sm' : 'text-ink-3 hover:text-ink',
      )}
    >
      {label}
    </button>
  )
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
            ? scopeCopy.all
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
