import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/cn'
import { squareModelsQuery } from '@/features/market/api'
import {
  Button,
  Dialog,
  Empty,
  Input,
  Tag,
  useToast,
} from '@/ui'

import { createKey } from '../api'
import { parseYuanToMicro } from '../format'
import { buildModelSelection, keyNameSchema, spendCapSchema } from '../schemas'
import { STRATEGIES, type Strategy } from '../types'
import { ErrorBlock, Skeleton } from './DataState'
import { Field } from './form'

const wizard = copy.pages.consoleKeys.wizard

export interface CreateKeyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (tokenId: number | null) => void
}

const strategyLabels: Record<Strategy, { label: string; hint: string }> = {
  cheapest: wizard.strategy.options.cheapest,
  balanced: wizard.strategy.options.balanced,
  stable: wizard.strategy.options.stable,
}

export function CreateKeyDialog({ open, onOpenChange, onCreated }: CreateKeyDialogProps) {
  const toast = useToast()
  const modelsQuery = useQuery({ ...squareModelsQuery(), enabled: open })

  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [search, setSearch] = useState('')
  const [selection, setSelection] = useState<Record<string, string>>({})
  const [strategy, setStrategy] = useState<Strategy>('balanced')
  const [spendCap, setSpendCap] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!open) return
    setStep(0)
    setName('')
    setSearch('')
    setSelection({})
    setStrategy('balanced')
    setSpendCap('')
    setError(null)
    setSubmitting(false)
  }, [open])

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

  const selectedCount = Object.keys(selection).length

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

  function goNext() {
    setError(null)
    if (step === 0) {
      const parsed = keyNameSchema.safeParse(name)
      if (!parsed.success) {
        setError(parsed.error.issues[0]?.message ?? wizard.errors.nameRequired)
        return
      }
      setStep(1)
      return
    }
    if (step === 1) {
      const result = buildModelSelection(selection)
      if (!result.ok) {
        setError(result.error)
        return
      }
      setStep(2)
      return
    }
    if (step === 2) {
      setStep(3)
    }
  }

  async function submit() {
    setError(null)
    const nameCheck = keyNameSchema.safeParse(name)
    if (!nameCheck.success) {
      setStep(0)
      setError(nameCheck.error.issues[0]?.message ?? wizard.errors.nameRequired)
      return
    }
    const modelsResult = buildModelSelection(selection)
    if (!modelsResult.ok) {
      setStep(1)
      setError(modelsResult.error)
      return
    }
    const spendCheck = spendCapSchema.safeParse(spendCap)
    if (!spendCheck.success) {
      setError(spendCheck.error.issues[0]?.message ?? wizard.errors.spendInvalid)
      return
    }

    setSubmitting(true)
    try {
      const spendMicro = spendCap.trim() ? parseYuanToMicro(spendCap) : null
      const result = await createKey({
        name: name.trim(),
        strategy,
        spend_cap_micro: spendMicro,
        models: modelsResult.models,
      })

      if (result.tokenId === null) {
        toast.show({ title: wizard.locateFailed, tone: 'default' })
      } else if (!result.prefSaved) {
        toast.show({ title: wizard.prefFailed, tone: 'error' })
      } else if (result.failures.length > 0) {
        toast.show({ title: wizard.partial(result.failures.length), tone: 'error' })
      } else {
        toast.show({ title: wizard.success, tone: 'success' })
      }
      onCreated(result.tokenId)
      onOpenChange(false)
    } catch (submitError) {
      const message = submitError instanceof ApiError ? submitError.message : wizard.failed
      setError(message)
    } finally {
      setSubmitting(false)
    }
  }

  const canNext = step < 3

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!submitting) onOpenChange(next)
      }}
      title={wizard.title}
      description={wizard.description}
      className="max-w-2xl"
      footer={
        <div className="flex w-full items-center justify-between gap-2">
          <Button
            variant="secondary"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            {copy.pages.consoleShared.cancel}
          </Button>
          <div className="flex items-center gap-2">
            {step > 0 ? (
              <Button
                variant="secondary"
                onClick={() => {
                  setError(null)
                  setStep((current) => Math.max(0, current - 1))
                }}
                disabled={submitting}
              >
                {wizard.back}
              </Button>
            ) : null}
            {canNext ? (
              <Button onClick={goNext} disabled={modelsQuery.isPending && step === 1}>
                {wizard.next}
              </Button>
            ) : (
              <Button onClick={() => void submit()} loading={submitting}>
                {submitting ? wizard.creating : wizard.create}
              </Button>
            )}
          </div>
        </div>
      }
    >
      <ol className="mb-5 grid grid-cols-4 gap-1" aria-label={wizard.stepOf(step + 1, 4)}>
        {wizard.steps.map((label, index) => {
          const state = index === step ? 'current' : index < step ? 'done' : 'todo'
          return (
            <li key={label} className="min-w-0">
              <div
                className={cn(
                  'border-t-2 pt-1.5 text-[13px]',
                  state === 'current' && 'border-brand font-medium text-ink',
                  state === 'done' && 'border-brand/50 text-ink-3',
                  state === 'todo' && 'border-line text-ink-3/70',
                )}
              >
                <span className="tabular-nums text-[13px] text-ink-3/80">0{index + 1}</span>{' '}
                <span className="truncate">{label}</span>
              </div>
            </li>
          )
        })}
      </ol>

      {error ? (
        <p role="alert" className="mb-4 border border-danger/25 bg-danger/10 px-3 py-2 text-[13px] leading-5 text-danger">
          {error}
        </p>
      ) : null}

      {step === 0 ? (
        <div className="space-y-4">
          <Field
            label={wizard.name.label}
            htmlFor="wizard-key-name"
            hint={wizard.name.hint}
          >
            <Input
              id="wizard-key-name"
              autoFocus
              maxLength={50}
              placeholder={wizard.name.placeholder}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <p className="text-[13px] leading-5 text-ink-3">{copy.pages.consoleKeys.lead}</p>
        </div>
      ) : null}

      {step === 1 ? (
        <div>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <div className="text-[13px] font-medium text-ink">{wizard.models.label}</div>
              <p className="mt-0.5 text-[13px] text-ink-3">{wizard.models.hint}</p>
            </div>
            <span className="tabular-nums text-[13px] text-brand-ink">
              {wizard.models.selected(selectedCount)}
            </span>
          </div>

          <div className="mt-3 flex flex-wrap gap-2">
            <Input
              aria-label={wizard.models.search}
              placeholder={wizard.models.search}
              className="min-w-[140px] flex-1"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <Button variant="secondary" size="md" className="shrink-0" onClick={selectAll}>
              {wizard.models.selectAll}
            </Button>
            <Button
              variant="ghost"
              size="md"
              className="shrink-0"
              onClick={() => setSelection({})}
            >
              {wizard.models.clear}
            </Button>
          </div>

          <div className="mt-3 max-h-64 overflow-y-auto rounded-md border border-line">
            {modelsQuery.isPending ? (
              <div className="space-y-2 p-3">
                <Skeleton className="h-5" />
                <Skeleton className="h-5" />
                <Skeleton className="h-5" />
              </div>
            ) : modelsQuery.isError ? (
              <ErrorBlock
                className="m-3"
                error={modelsQuery.error}
                onRetry={() => void modelsQuery.refetch()}
              />
            ) : filtered.length === 0 ? (
              <Empty
                className="border-0"
                title={models.length === 0 ? wizard.models.emptyTitle : undefined}
                reason={
                  models.length === 0 ? wizard.models.emptyReason : wizard.models.noMatch
                }
                next={models.length === 0 ? wizard.models.emptyNext : undefined}
              />
            ) : (
              <ul className="divide-y divide-line">
                {filtered.map((model) => {
                  const checked = model.model in selection
                  return (
                    <li
                      key={`${model.model}/${model.vendor}`}
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
                          onChange={() => toggleModel(model.model)}
                        />
                        <span className="min-w-0">
                          <span className="block truncate text-[13px] text-ink">{model.model}</span>
                          <span className="block text-[13px] text-ink-3">
                            {model.vendor} · {wizard.models.sellers(model.listing_count)}
                          </span>
                        </span>
                      </label>
                      <label className="flex shrink-0 items-center gap-1.5 text-[13px] text-ink-3">
                        <span className="whitespace-nowrap">{wizard.models.capLabel}</span>
                        <input
                          type="text"
                          inputMode="decimal"
                          disabled={!checked}
                          aria-label={`${model.model} ${wizard.models.capLabel}`}
                          placeholder={wizard.models.capPlaceholder}
                          value={selection[model.model] ?? ''}
                          onChange={(event) => setCap(model.model, event.target.value)}
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
      ) : null}

      {step === 2 ? (
        <div>
          <div className="text-[13px] font-medium text-ink">{wizard.strategy.label}</div>
          <p className="mt-0.5 text-[13px] text-ink-3">{wizard.strategy.hint}</p>
          <div className="mt-3 grid gap-2 sm:grid-cols-3">
            {STRATEGIES.map((value) => {
              const option = strategyLabels[value]
              const active = strategy === value
              return (
                <button
                  key={value}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setStrategy(value)}
                  className={cn(
                    'rounded-md border px-3 py-3 text-left transition-colors',
                    active
                      ? 'border-brand bg-brand-soft'
                      : 'border-line bg-bg hover:border-line-strong hover:bg-bg-soft/60',
                  )}
                >
                  <span className="flex items-center justify-between gap-2">
                    <span className="text-[13px] font-medium text-ink">{option.label}</span>
                    {active ? <Tag tone="brand">{copy.pages.consoleShared.confirm}</Tag> : null}
                  </span>
                  <span className="mt-1 block text-[13px] leading-5 text-ink-3">
                    {option.hint}
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      ) : null}

      {step === 3 ? (
        <div className="space-y-4">
          <Field
            label={wizard.spend.label}
            htmlFor="wizard-spend-cap"
            hint={wizard.spend.hint}
          >
            <div className="flex items-center gap-2">
              <Input
                id="wizard-spend-cap"
                inputMode="decimal"
                placeholder={wizard.spend.placeholder}
                value={spendCap}
                onChange={(event) => setSpendCap(event.target.value)}
              />
              <span className="shrink-0 text-[13px] text-ink-3">{wizard.spend.currency}</span>
            </div>
          </Field>

          <dl className="divide-y divide-line border-y border-line text-[13px]">
            <ReviewRow label={wizard.review.name} value={name.trim() || copy.pages.consoleShared.none} />
            <ReviewRow
              label={wizard.review.models}
              value={copy.pages.consoleKeys.modelsUnit(selectedCount)}
            />
            <ReviewRow label={wizard.review.strategy} value={strategyLabels[strategy].label} />
            <ReviewRow
              label={wizard.review.spend}
              value={spendCap.trim() ? `${spendCap.trim()} ${wizard.spend.currency}` : wizard.review.noCap}
            />
          </dl>
        </div>
      ) : null}
    </Dialog>
  )
}

function ReviewRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2">
      <dt className="text-ink-3">{label}</dt>
      <dd className="min-w-0 truncate text-right font-medium text-ink">{value}</dd>
    </div>
  )
}
