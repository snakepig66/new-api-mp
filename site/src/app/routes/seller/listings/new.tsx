import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatMultiplier, savingPercent } from '@/lib/multiplier'
import { formatYuan } from '@/lib/money'
import {
  Button,
  Empty,
  Input,
  Multiplier,
  PageHeader,
  Price,
  Tag,
  useToast,
} from '@/ui'

import {
  SELLER_VENDORS,
  createListing,
  probeListing,
  sellerListingsQuery,
  squareModelForSeller,
  type ProbeResult,
} from '@/features/seller/api'
import { Field, notify, Skeleton, selectClass } from '@/features/seller/components'
import { probeFailReasonLabel, sellerErrorMessage } from '@/features/seller/errors'
import { parseYuanToMicro } from '@/features/seller/money-input'
import {
  compareMultiplier,
  previewMultiplier,
} from '@/features/seller/preview'
import {
  canLeaveStep,
  canSubmit,
  previousWizardStep,
  type WizardSnapshot,
  type WizardStep,
} from '@/features/seller/wizard'

export const Route = createFileRoute('/seller/listings/new')({
  component: NewListingPage,
})

const page = copy.pages.sellerListingNew
const validation = page.validation
const timeRe = /^([01]\d|2[0-3]):[0-5]\d$/

const hourWindowSchema = z.object({
  start: z.string(),
  end: z.string(),
})

const wizardSchema = z
  .object({
    vendor_id: z.string().min(1, validation.vendor),
    api_key: z
      .string()
      .trim()
      .min(8, validation.key)
      .refine((value) => !/\s/.test(value), validation.key),
    models: z.array(z.string()).min(1, validation.models),
    multiplier_milli: z
      .number()
      .int()
      .min(1, validation.multiplier)
      .max(1000, validation.multiplier),
    concurrency: z
      .number()
      .int(validation.concurrency)
      .min(1, validation.concurrency)
      .max(1000, validation.concurrency),
    rpm: z.number().int(validation.rpm).min(0, validation.rpm).max(1_000_000, validation.rpm),
    daily_cap_yuan: z.string().refine((value) => {
      const micro = parseYuanToMicro(value)
      return micro !== null && micro <= 1_000_000_000_000n
    }, validation.dailyCap),
    use_hours: z.boolean(),
    hours: z.array(hourWindowSchema),
  })
  .superRefine((value, ctx) => {
    if (!value.use_hours) return
    value.hours.forEach((window, index) => {
      if (!timeRe.test(window.start)) {
        ctx.addIssue({
          code: 'custom',
          path: ['hours', index, 'start'],
          message: validation.hours,
        })
      }
      if (!timeRe.test(window.end) || window.end <= window.start) {
        ctx.addIssue({
          code: 'custom',
          path: ['hours', index, 'end'],
          message: validation.hours,
        })
      }
    })
  })

type WizardForm = z.infer<typeof wizardSchema>

function windowsValid(windows: { start: string; end: string }[]): boolean {
  return windows.every(
    (window) => timeRe.test(window.start) && timeRe.test(window.end) && window.start < window.end,
  )
}

function StepRail({ step, onJump }: { step: number; onJump: (next: WizardStep) => void }) {
  return (
    <ol className="mt-6 grid gap-px border border-line bg-line sm:grid-cols-4">
      {page.steps.map((title, index) => {
        const completed = index < step
        const active = index === step
        return (
          <li
            key={title}
            aria-current={active ? 'step' : undefined}
            className={cn('bg-surface p-3.5', active && 'shadow-[inset_0_2px_0_var(--color-brand-dark)]')}
          >
            <button
              type="button"
              disabled={index > step}
              onClick={() => onJump(index as WizardStep)}
              className={cn(
                'flex w-full items-center gap-2 text-left',
                index > step ? 'cursor-not-allowed' : 'cursor-pointer',
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  'grid h-5 w-5 shrink-0 place-items-center rounded-full border text-[10px] font-medium',
                  completed
                    ? 'border-success/30 bg-success-soft text-success'
                    : active
                      ? 'border-brand-dark bg-brand-dark text-white'
                      : 'border-line-strong bg-wash text-muted',
                )}
              >
                {completed ? (
                  <svg width="10" height="10" viewBox="0 0 12 12" fill="none">
                    <path
                      d="M2.5 6.2l2.2 2.2 4.8-5"
                      stroke="currentColor"
                      strokeWidth="1.6"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                ) : (
                  index + 1
                )}
              </span>
              <span
                className={cn(
                  'text-[12px] font-medium',
                  completed || active ? 'text-ink' : 'text-muted',
                )}
              >
                {title}
              </span>
            </button>
          </li>
        )
      })}
    </ol>
  )
}

function ProbeSummary({ probe }: { probe: ProbeResult }) {
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Tag tone={probe.key_valid ? 'success' : 'danger'} dot>
          {probe.key_valid ? page.verify.valid : page.verify.invalid}
        </Tag>
        {probe.prompt_cache ? (
          <Tag tone="brand">
            {page.verify.cache} · {page.verify.cacheYes}
          </Tag>
        ) : (
          <Tag>
            {page.verify.cache} · {page.verify.cacheNo}
          </Tag>
        )}
      </div>

      <dl className="grid grid-cols-2 gap-px border border-line bg-line sm:grid-cols-3">
        <div className="bg-surface px-3 py-2.5">
          <dt className="text-[11px] text-muted">{page.verify.balance}</dt>
          <dd className="mt-0.5 font-numeric text-[13px] tabular-nums text-ink">
            {probe.balance_micro === null || probe.balance_micro === undefined
              ? page.verify.balanceUnknown
              : formatYuan(probe.balance_micro)}
          </dd>
        </div>
        <div className="bg-surface px-3 py-2.5">
          <dt className="text-[11px] text-muted">{page.verify.latency}</dt>
          <dd className="mt-0.5 font-numeric text-[13px] tabular-nums text-ink">
            {probe.latency_ms} ms
          </dd>
        </div>
        <div className="bg-surface px-3 py-2.5">
          <dt className="text-[11px] text-muted">{page.verify.cache}</dt>
          <dd className="mt-0.5 text-[13px] text-ink">
            {probe.prompt_cache ? page.verify.cacheYes : page.verify.cacheNo}
          </dd>
        </div>
      </dl>

      {probe.fail_reason ? (
        <div className="border border-warning/25 bg-warning-soft px-3 py-2 text-[12px] leading-5 text-warning">
          {page.verify.failReason}：{probeFailReasonLabel(probe.fail_reason)}
        </div>
      ) : null}

      {probe.missing_models.length > 0 ? (
        <p className="text-[12px] leading-5 text-warning">
          {page.verify.missing(probe.missing_models.join('、'))}
        </p>
      ) : null}
    </div>
  )
}

function NewListingPage() {
  const toast = useToast()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [step, setStep] = useState<WizardStep>(0)
  const [multiplierText, setMultiplierText] = useState('0.500')
  const [previewModel, setPreviewModel] = useState('')

  const form = useForm<WizardForm>({
    resolver: zodResolver(wizardSchema),
    mode: 'onTouched',
    defaultValues: {
      vendor_id: '',
      api_key: '',
      models: [],
      multiplier_milli: 500,
      concurrency: 5,
      rpm: 0,
      daily_cap_yuan: '0',
      use_hours: false,
      hours: [],
    },
  })
  const values = form.watch()
  const { fields, append, remove } = useFieldArray({ control: form.control, name: 'hours' })

  const probe = useMutation({
    mutationFn: probeListing,
    onSuccess: (result) => {
      form.setValue('models', result.models, { shouldValidate: true })
    },
  })

  const create = useMutation({
    mutationFn: createListing,
    onSuccess: () => {
      notify(toast, { title: page.success, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: sellerListingsQuery().queryKey })
      void navigate({ to: '/seller/listings' })
    },
  })

  const snapshot: WizardSnapshot = {
    vendorId: values.vendor_id,
    apiKey: values.api_key,
    probe: probe.data ?? null,
    selectedModels: values.models,
    multiplierMilli: values.multiplier_milli,
    concurrency: values.concurrency,
    rpm: values.rpm,
    dailyCapYuan: values.daily_cap_yuan,
    hoursValid: !values.use_hours || values.hours.length === 0 || windowsValid(values.hours),
  }

  const effectivePreviewModel =
    previewModel && values.models.includes(previewModel) ? previewModel : values.models[0] ?? ''
  const market = useQuery({
    ...squareModelForSeller(effectivePreviewModel),
    enabled: step === 3 && Boolean(effectivePreviewModel),
  })

  const tier = market.data?.tier ?? 'flat'
  const officialPrice = market.data?.official_prices?.[tier]?.input_micro_per_mtok
  const preview =
    officialPrice !== undefined && officialPrice !== null
      ? previewMultiplier(officialPrice, values.multiplier_milli)
      : null
  const marketMin = market.data?.multiplier_min_milli
  const comparison = compareMultiplier(values.multiplier_milli, marketMin)

  function runProbe() {
    const current = form.getValues()
    probe.mutate({
      vendor_id: current.vendor_id,
      api_key: current.api_key.trim(),
      models: [],
    })
  }

  async function handleNext() {
    if (step === 0) {
      if (!(await form.trigger(['vendor_id']))) return
      setStep(1)
      return
    }
    if (step === 1) {
      if (!(await form.trigger(['api_key']))) return
      setStep(2)
      probe.reset()
      runProbe()
      return
    }
    if (step === 2) {
      if (!(await form.trigger(['models']))) return
      if (!probe.data?.ok) return
      setStep(3)
      return
    }
  }

  function handleJump(next: WizardStep) {
    if (next <= step) setStep(next)
  }

  function onSubmit(data: WizardForm) {
    const dailyCap = parseYuanToMicro(data.daily_cap_yuan)
    if (dailyCap === null) return
    const hours =
      data.use_hours && data.hours.length > 0 ? JSON.stringify(data.hours) : ''
    create.mutate({
      vendor_id: data.vendor_id,
      api_key: data.api_key.trim(),
      models: data.models,
      multiplier_milli: data.multiplier_milli,
      concurrency: data.concurrency,
      rpm: data.rpm,
      daily_cap_micro: Number(dailyCap),
      hours,
    })
  }

  function toggleModel(model: string, checked: boolean) {
    const current = form.getValues('models')
    const next = checked
      ? current.includes(model)
        ? current
        : [...current, model]
      : current.filter((item) => item !== model)
    form.setValue('models', next, { shouldValidate: true, shouldDirty: true })
  }

  const submitError = create.isError ? sellerErrorMessage(create.error) : null
  const probedModels = probe.data?.models ?? []

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />
      <StepRail step={step} onJump={handleJump} />

      <form
        className="mt-6 border border-line bg-surface"
        onSubmit={form.handleSubmit(onSubmit)}
      >
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-5 py-4">
          <div>
            <h2 className="text-[15px] font-semibold tracking-tight text-ink">
              {page.steps[step]}
            </h2>
            <p className="mt-1 text-[12px] leading-5 text-muted">{page.stepLead[step]}</p>
          </div>
          <span className="font-numeric text-[11.5px] text-muted">0{step + 1} / 04</span>
        </header>

        <div className="px-5 py-5">
          {step === 0 ? (
            <div className="space-y-4">
              <Field
                label={page.vendor.label}
                hint={page.vendor.hint}
                error={form.formState.errors.vendor_id?.message}
              >
                <div className="grid gap-2 sm:grid-cols-2">
                  {SELLER_VENDORS.map((vendor) => {
                    const checked = values.vendor_id === vendor.id
                    return (
                      <label
                        key={vendor.id}
                        className={cn(
                          'flex cursor-pointer items-center justify-between gap-3 rounded-[4px] border px-4 py-3 transition-colors',
                          checked
                            ? 'border-brand-dark bg-brand-soft/60'
                            : 'border-line-strong bg-surface hover:border-brand/40',
                        )}
                      >
                        <span>
                          <span className="block text-[13px] font-medium text-ink">
                            {vendor.name}
                          </span>
                          <span className="font-numeric text-[11px] text-muted">
                            {vendor.id}
                          </span>
                        </span>
                        <input
                          type="radio"
                          value={vendor.id}
                          className="h-3.5 w-3.5 accent-[var(--color-brand-dark)]"
                          {...form.register('vendor_id')}
                        />
                      </label>
                    )
                  })}
                </div>
              </Field>
            </div>
          ) : null}

          {step === 1 ? (
            <div className="max-w-xl space-y-4">
              <Field
                label={page.key.label}
                htmlFor="new-listing-key"
                hint={page.key.hint}
                error={form.formState.errors.api_key?.message}
              >
                <Input
                  id="new-listing-key"
                  type="password"
                  mono
                  autoComplete="new-password"
                  spellCheck={false}
                  placeholder={page.key.placeholder}
                  invalid={Boolean(form.formState.errors.api_key)}
                  {...form.register('api_key')}
                />
              </Field>
              <div className="border border-line bg-wash px-3 py-2 text-[12px]">
                <span className="text-muted">
                  {values.api_key.trim().length >= 4
                    ? page.key.preview(values.api_key.trim().slice(-4))
                    : page.key.previewEmpty}
                </span>
              </div>
            </div>
          ) : null}

          {step === 2 ? (
            <div className="space-y-5">
              {probe.isPending ? (
                <div className="space-y-3" aria-busy="true" aria-live="polite">
                  <p className="text-[12.5px] text-muted">{page.verify.probing}</p>
                  <Skeleton className="h-16 w-full" />
                  <Skeleton className="h-10 w-2/3" />
                </div>
              ) : probe.isError ? (
                <Empty
                  title={page.verify.invalid}
                  reason={sellerErrorMessage(probe.error)}
                  next={copy.pages.sellerCommon.probeErrors.generic}
                  action={
                    <Button variant="secondary" onClick={runProbe}>
                      {page.verify.retry}
                    </Button>
                  }
                />
              ) : probe.data ? (
                <>
                  <ProbeSummary probe={probe.data} />

                  <div className="border-t border-line pt-4">
                    <div className="flex flex-wrap items-baseline justify-between gap-2">
                      <h3 className="text-[13px] font-medium text-ink">
                        {page.verify.modelsTitle}
                      </h3>
                      <span className="text-[11.5px] text-muted">
                        {page.verify.selected(values.models.length, probedModels.length)}
                      </span>
                    </div>
                    <p className="mt-1 text-[11.5px] text-muted">{page.verify.modelsHint}</p>

                    {probedModels.length === 0 ? (
                      <p className="mt-3 text-[12.5px] text-warning">
                        {page.verify.modelsEmpty}
                      </p>
                    ) : (
                      <div className="mt-3 grid gap-2 sm:grid-cols-2">
                        {probedModels.map((model) => (
                          <label
                            key={model}
                            className="flex cursor-pointer items-center gap-2 rounded-[4px] border border-line px-3 py-2 text-[12.5px] text-ink hover:border-brand/40"
                          >
                            <input
                              type="checkbox"
                              checked={values.models.includes(model)}
                              onChange={(event) => toggleModel(model, event.target.checked)}
                              className="h-3.5 w-3.5 accent-[var(--color-brand-dark)]"
                            />
                            <span className="font-numeric">{model}</span>
                          </label>
                        ))}
                      </div>
                    )}
                    {form.formState.errors.models?.message ? (
                      <p className="mt-2 text-[11.5px] text-danger" role="alert">
                        {form.formState.errors.models.message}
                      </p>
                    ) : null}
                  </div>

                  <div className="flex justify-end border-t border-line pt-4">
                    <Button
                      variant="secondary"
                      size="sm"
                      loading={probe.isPending}
                      onClick={runProbe}
                    >
                      {probe.isPending ? page.verify.retrying : page.verify.retry}
                    </Button>
                  </div>
                </>
              ) : (
                <Button variant="secondary" onClick={runProbe}>
                  {page.actions.probe}
                </Button>
              )}
            </div>
          ) : null}

          {step === 3 ? (
            <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
              <div className="space-y-6">
                <div>
                  <div className="flex items-end justify-between gap-3">
                    <Field label={page.pricing.label} hint={page.pricing.lead}>
                      <div className="flex items-center gap-3">
                        <input
                          type="range"
                          min={1}
                          max={1000}
                          step={1}
                          value={values.multiplier_milli}
                          onChange={(event) => {
                            const next = Number(event.target.value)
                            setMultiplierText((next / 1000).toFixed(3))
                            form.setValue('multiplier_milli', next, { shouldValidate: true })
                          }}
                          className="h-1.5 w-full cursor-pointer accent-[var(--color-brand-dark)]"
                          aria-label={page.pricing.label}
                        />
                        <Input
                          aria-label={page.pricing.inputLabel}
                          className="w-24 shrink-0 text-right"
                          mono
                          inputMode="decimal"
                          value={multiplierText}
                          onChange={(event) => {
                            const text = event.target.value
                            setMultiplierText(text)
                            const next = Number(text)
                            if (Number.isFinite(next)) {
                              form.setValue(
                                'multiplier_milli',
                                Math.min(1000, Math.max(1, Math.round(next * 1000))),
                                { shouldValidate: true },
                              )
                            }
                          }}
                        />
                      </div>
                    </Field>
                  </div>
                  {form.formState.errors.multiplier_milli?.message ? (
                    <p className="mt-1 text-[11.5px] text-danger" role="alert">
                      {form.formState.errors.multiplier_milli.message}
                    </p>
                  ) : null}
                </div>

                <div className="border-t border-line pt-4">
                  <h3 className="text-[13px] font-medium text-ink">{page.pricing.limitsTitle}</h3>
                  <div className="mt-3 grid gap-4 sm:grid-cols-3">
                    <Field
                      label={page.pricing.concurrency}
                      htmlFor="listing-concurrency"
                      hint={page.pricing.concurrencyHint}
                      error={form.formState.errors.concurrency?.message}
                    >
                      <Input
                        id="listing-concurrency"
                        type="number"
                        min={1}
                        max={1000}
                        mono
                        {...form.register('concurrency', { valueAsNumber: true })}
                      />
                    </Field>
                    <Field
                      label={page.pricing.rpm}
                      htmlFor="listing-rpm"
                      hint={page.pricing.rpmHint}
                      error={form.formState.errors.rpm?.message}
                    >
                      <Input
                        id="listing-rpm"
                        type="number"
                        min={0}
                        max={1_000_000}
                        mono
                        {...form.register('rpm', { valueAsNumber: true })}
                      />
                    </Field>
                    <Field
                      label={page.pricing.dailyCap}
                      htmlFor="listing-daily-cap"
                      hint={page.pricing.dailyCapHint}
                      error={form.formState.errors.daily_cap_yuan?.message}
                    >
                      <Input
                        id="listing-daily-cap"
                        inputMode="decimal"
                        mono
                        {...form.register('daily_cap_yuan')}
                      />
                    </Field>
                  </div>
                </div>

                <div className="border-t border-line pt-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div>
                      <h3 className="text-[13px] font-medium text-ink">{page.pricing.hours}</h3>
                      <p className="mt-1 text-[11.5px] text-muted">{page.pricing.hoursHint}</p>
                    </div>
                    <label className="flex items-center gap-2 text-[12px] text-ink">
                      <input
                        type="checkbox"
                        className="h-3.5 w-3.5 accent-[var(--color-brand-dark)]"
                        {...form.register('use_hours')}
                        onChange={(event) => {
                          const checked = event.target.checked
                          form.setValue('use_hours', checked, { shouldValidate: true })
                          if (checked && fields.length === 0) {
                            append({ start: '09:00', end: '18:00' })
                          }
                        }}
                      />
                      {page.pricing.enableHours}
                    </label>
                  </div>

                  {values.use_hours ? (
                    <div className="mt-3 space-y-2">
                      {fields.length === 0 ? (
                        <p className="text-[12px] text-muted">{page.pricing.noWindow}</p>
                      ) : null}
                      {fields.map((field, index) => (
                        <div key={field.id} className="flex flex-wrap items-end gap-2">
                          <Field
                            label={page.pricing.start}
                            className="w-[132px]"
                            error={form.formState.errors.hours?.[index]?.start?.message}
                          >
                            <Input type="time" mono {...form.register(`hours.${index}.start`)} />
                          </Field>
                          <span className="pb-2 text-muted" aria-hidden="true">
                            –
                          </span>
                          <Field
                            label={page.pricing.end}
                            className="w-[132px]"
                            error={form.formState.errors.hours?.[index]?.end?.message}
                          >
                            <Input type="time" mono {...form.register(`hours.${index}.end`)} />
                          </Field>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="mb-0.5"
                            onClick={() => remove(index)}
                          >
                            {page.pricing.removeWindow}
                          </Button>
                        </div>
                      ))}
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => append({ start: '09:00', end: '18:00' })}
                      >
                        {page.pricing.addWindow}
                      </Button>
                    </div>
                  ) : (
                    <p className="mt-2 text-[12px] text-muted">{page.pricing.noWindow}</p>
                  )}
                </div>
              </div>

              <aside className="border border-line bg-wash/60 p-4">
                <h3 className="text-[12.5px] font-medium text-ink">{page.pricing.title}</h3>

                {values.models.length > 1 ? (
                  <label className="mt-3 block">
                    <span className="block text-[11px] text-muted">
                      {page.pricing.previewModel}
                    </span>
                    <select
                      className={cn(selectClass, 'mt-1')}
                      value={effectivePreviewModel}
                      onChange={(event) => setPreviewModel(event.target.value)}
                    >
                      {values.models.map((model) => (
                        <option key={model} value={model}>
                          {model}
                        </option>
                      ))}
                    </select>
                  </label>
                ) : (
                  <div className="mt-2 font-numeric text-[11.5px] text-muted">
                    {effectivePreviewModel || '—'}
                  </div>
                )}

                {market.isPending ? (
                  <div className="mt-4 space-y-2" aria-busy="true">
                    <Skeleton className="h-4 w-full" />
                    <Skeleton className="h-4 w-2/3" />
                  </div>
                ) : preview ? (
                  <dl className="mt-4 space-y-3">
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-[11.5px] text-muted">{page.pricing.official}</dt>
                      <dd>
                        <Price
                          value={officialPrice ?? 0}
                          tier={tier}
                          size="sm"
                          unit={null}
                        />
                      </dd>
                    </div>
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-[11.5px] text-muted">{page.pricing.buyer}</dt>
                      <dd>
                        <Price value={preview.buyerMicro} tier={tier} size="sm" unit={null} />
                      </dd>
                    </div>
                    <div className="flex items-baseline justify-between gap-3 border-t border-line pt-3">
                      <dt className="text-[11.5px] text-muted">{page.pricing.seller}</dt>
                      <dd className="font-numeric text-[16px] font-medium tabular-nums text-brand-dark">
                        {formatYuan(preview.sellerMicro)}
                      </dd>
                    </div>
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-[11.5px] text-muted">{page.pricing.fee}</dt>
                      <dd className="font-numeric text-[12px] tabular-nums text-muted">
                        {formatYuan(preview.feeMicro)}
                      </dd>
                    </div>
                  </dl>
                ) : (
                  <p className="mt-4 text-[12px] leading-5 text-muted">
                    {page.pricing.noOfficial}
                  </p>
                )}

                <div className="mt-4 border-t border-line pt-3">
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="text-[11.5px] text-muted">{page.pricing.marketLowest}</span>
                    <span>
                      {marketMin ? (
                        <Multiplier milli={marketMin} showSaving={false} />
                      ) : (
                        <span className="text-[12px] text-muted">—</span>
                      )}
                    </span>
                  </div>
                  {comparison ? (
                    <p
                      className={cn(
                        'mt-2 text-[11.5px]',
                        comparison === 'lower'
                          ? 'text-success'
                          : comparison === 'higher'
                            ? 'text-warning'
                            : 'text-muted',
                      )}
                    >
                      {comparison === 'higher'
                        ? page.pricing.compareHigher(
                            formatMultiplier(values.multiplier_milli - (marketMin ?? 0)),
                          )
                        : comparison === 'lower'
                          ? page.pricing.compareLower(
                              formatMultiplier((marketMin ?? 0) - values.multiplier_milli),
                            )
                          : page.pricing.compareSame}
                    </p>
                  ) : (
                    <p className="mt-2 text-[11.5px] text-muted">{page.pricing.noMarket}</p>
                  )}
                </div>

                <div className="mt-4 border-t border-line pt-3 text-[11px] leading-5 text-muted">
                  {page.pricing.summary(
                    formatMultiplier(values.multiplier_milli),
                    savingPercent(values.multiplier_milli),
                  )}
                  <span className="mt-1 block text-[10.5px] text-muted/80">
                    {page.pricing.tierNote(copy.tiers[tier])}
                  </span>
                </div>
              </aside>
            </div>
          ) : null}

          {submitError ? (
            <p className="mt-4 text-[12px] text-danger" role="alert">
              {submitError}
            </p>
          ) : null}
        </div>

        <footer className="flex items-center justify-between gap-2 border-t border-line px-5 py-4">
          <div className="text-[11px] text-muted">
            {values.models.length > 0
              ? page.pricing.selectedCount(values.models.length)
              : null}
          </div>
          <div className="flex items-center gap-2">
            {step > 0 ? (
              <Button
                variant="secondary"
                onClick={() => setStep(previousWizardStep(step))}
                disabled={create.isPending}
              >
                {page.actions.back}
              </Button>
            ) : null}
            {step < 3 ? (
              <Button
                onClick={() => void handleNext()}
                disabled={!canLeaveStep(step, snapshot) || probe.isPending}
              >
                {page.actions.next}
              </Button>
            ) : (
              <Button
                type="submit"
                loading={create.isPending}
                disabled={!canSubmit(snapshot)}
              >
                {create.isPending ? page.pricing.submitting : page.actions.create}
              </Button>
            )}
          </div>
        </footer>
      </form>
    </>
  )
}
