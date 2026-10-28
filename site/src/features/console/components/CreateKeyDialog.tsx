import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/cn'
import { ModelPicker, type ModelSelection } from '@/features/market/ModelPicker'
import { currentBaseUrl } from '@/features/market/url'
import { Button, Dialog, Input, useToast } from '@/ui'

import { consoleKeysQuery, createKey, currentIPQuery, walletQuery } from '../api'
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
  /** Square row action: start in selected scope with this model checked. */
  initialModel?: string
}

export function CreateKeyDialog({
  open,
  onOpenChange,
  onCreated,
  onOpenDetail,
  initialModel,
}: CreateKeyDialogProps) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const keysQuery = useQuery({ ...consoleKeysQuery(), enabled: open })
  const currentIp = useQuery({ ...currentIPQuery(), enabled: open })

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
  const wallet = useQuery({ ...walletQuery(), enabled: open && created !== null })

  useEffect(() => {
    if (!open) return
    form.reset(initialModel ? { ...defaultPref, modelScope: 'selected' } : defaultPref)
    setName('')
    setSpendCap('')
    setSelection(initialModel ? { [initialModel]: '' } : {})
    setCopyFrom('')
    setNeverExpires(true)
    setExpiresAt('')
    setAllowIps('')
    setError(null)
    setSubmitting(false)
    setCreated(null)
    setCopied(false)
  }, [form, open, initialModel])

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

  const walletZero =
    wallet.data !== undefined && Number(wallet.data.balance_micro) <= 0

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
        <CreatedKey
          result={created}
          onCopy={() => void copyKey()}
          copied={copied}
          walletZero={walletZero}
        />
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
            <div className="mt-2 inline-flex glass-inset p-0.5">
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

          <details className="glass-inset">
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
                  className="h-11 w-full rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-3 text-[14px] text-ink"
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
                  {currentIp.data?.ip ? (
                    <div className="mb-1.5 flex flex-wrap items-center gap-2 text-[12px] text-ink-3">
                      <span className="font-mono">
                        {page.currentIp.label(currentIp.data.ip)}
                      </span>
                      <button
                        type="button"
                        className="text-brand-ink underline-offset-2 hover:underline"
                        onClick={insertCurrentIP}
                      >
                        {page.currentIp.insert}
                      </button>
                      <button
                        type="button"
                        className="text-brand-ink underline-offset-2 hover:underline"
                        onClick={() => void copyCurrentIP()}
                      >
                        {page.currentIp.copy}
                      </button>
                    </div>
                  ) : null}
                  <textarea
                    id="create-key-allow-ips"
                    rows={3}
                    spellCheck={false}
                    className="w-full resize-y rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-3 py-2 font-mono text-[13px] text-ink transition-colors placeholder:text-ink-3 focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20"
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
        active ? 'bg-white/90 font-medium text-brand-ink shadow-sm' : 'text-ink-3 hover:text-ink',
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
  walletZero,
}: {
  result: CreateKeyResult
  onCopy: () => void
  copied: boolean
  walletZero: boolean
}) {
  const toast = useToast()
  const [copiedConfig, setCopiedConfig] = useState<string | null>(null)
  const baseUrl = `${currentBaseUrl()}/v1`
  // The Anthropic SDK appends /v1/messages itself, so Claude Code's base URL
  // must be the origin rather than the OpenAI-style /v1 base.
  const claudeBaseUrl = currentBaseUrl()
  const model =
    result.model_scope === 'selected' && result.models.length > 0
      ? result.models[0].model
      : dialogCopy.allScopeModel
  const configs = [
    {
      key: 'claudeCode',
      label: dialogCopy.claudeCode,
      text: claudeCodeConfig(claudeBaseUrl, result.key, model),
    },
    {
      key: 'cursor',
      label: dialogCopy.cursor,
      text: openAIConfig(baseUrl, result.key, model),
    },
    {
      key: 'cherryStudio',
      label: dialogCopy.cherryStudio,
      text: cherryStudioConfig(baseUrl, result.key, model),
    },
  ]

  async function copyConfig(key: string, text: string) {
    const ok = await writeClipboard(text)
    setCopiedConfig(ok ? key : null)
    toast.show({
      title: ok ? shared.copied : shared.copyFailed,
      tone: ok ? 'success' : 'error',
    })
  }

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
        <code className="mt-3 block break-all glass-inset rounded-sm px-3 py-2 font-mono text-[13px] text-ink">
          {result.key}
        </code>
        <div className="mt-2 flex flex-wrap items-center gap-2 text-[13px] text-ink-3">
          <span>{dialogCopy.baseUrlLabel}</span>
          <code className="glass-inset rounded-sm px-2 py-0.5 font-mono text-[13px] text-ink">
            {baseUrl}
          </code>
          <span>{dialogCopy.baseUrlHint}</span>
        </div>
      </div>

      {walletZero ? (
        <div
          role="status"
          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-warn/25 bg-warn/10 px-4 py-3 text-[13px] text-warn"
        >
          <div>
            <div className="font-medium">{dialogCopy.balanceWarning.title}</div>
            <p className="mt-0.5">{dialogCopy.balanceWarning.body}</p>
          </div>
          <a
            href="/console/wallet"
            className="shrink-0 font-medium underline underline-offset-2"
          >
            {dialogCopy.balanceWarning.action}
          </a>
        </div>
      ) : null}

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

      <div>
        <div className="text-[14px] font-medium text-ink">{dialogCopy.configsTitle}</div>
        <p className="mt-0.5 text-[13px] text-ink-3">{dialogCopy.configsHint}</p>
        <div className="mt-2 space-y-2">
          {configs.map((config) => (
            <div key={config.key} className="glass-inset">
              <div className="flex items-center justify-between gap-2 border-b border-line px-3 py-2">
                <span className="text-[13px] font-medium text-ink">{config.label}</span>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => void copyConfig(config.key, config.text)}
                >
                  {copiedConfig === config.key ? shared.copied : dialogCopy.copyConfig}
                </Button>
              </div>
              <pre className="overflow-x-auto px-3 py-2 font-mono text-[12px] leading-5 text-ink">
                {config.text}
              </pre>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function claudeCodeConfig(baseUrl: string, key: string, model: string): string {
  return [
    `export ANTHROPIC_BASE_URL="${baseUrl}"`,
    `export ANTHROPIC_AUTH_TOKEN="${key}"`,
    `export ANTHROPIC_MODEL="${model}"`,
  ].join('\n')
}

function openAIConfig(baseUrl: string, key: string, model: string): string {
  return [`Base URL: ${baseUrl}`, `API Key: ${key}`, `Model: ${model}`].join('\n')
}

function cherryStudioConfig(baseUrl: string, key: string, model: string): string {
  return [`API 地址: ${baseUrl}`, `API Key: ${key}`, `模型: ${model}`].join('\n')
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
