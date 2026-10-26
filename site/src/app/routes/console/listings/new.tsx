import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
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
  PasswordInput,
  Price,
  Tag,
  useToast,
} from '@/ui'

import {
  createListing,
  marketplaceStatusQuery,
  probeListing,
  sellerChannelKind,
  sellerListingsQuery,
  squareModelForSeller,
  vendorsQuery,
  type ProbeResult,
  type SellerQuotaWindow,
  type SellerVendor,
} from '@/features/seller/api'
import { Field, notify, Skeleton, selectClass } from '@/features/seller/components'
import {
  duplicateKeyIssue,
  probeFailReasonLabel,
  sellerErrorMessage,
  type DuplicateKeyIssue,
} from '@/features/seller/errors'
import { parseYuanToMicro } from '@/features/seller/money-input'
import {
  compareMultiplier,
  DEFAULT_FEE_PERCENT,
  previewMultiplier,
} from '@/features/seller/preview'
import {
  canLeaveStep,
  canSubmit,
  previousWizardStep,
  type WizardSnapshot,
  type WizardStep,
} from '@/features/seller/wizard'

export const Route = createFileRoute('/console/listings/new')({
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
    plan_terms_accepted: z.boolean(),
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

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']

/** Human-readable plan quota window from the channel's quota_window JSON. */
function planWindowText(window: SellerQuotaWindow | undefined): string | null {
  if (!window) return null
  if (window.mode === 'weekly' && typeof window.weekday === 'number') {
    const weekday = WEEKDAYS[window.weekday] ?? String(window.weekday)
    const hour = String(window.hour ?? 0).padStart(2, '0')
    const minute = String(window.minute ?? 0).padStart(2, '0')
    return page.plan.windowWeekly(weekday, `${hour}:${minute}`)
  }
  const seconds = window.period_seconds
  if (typeof seconds !== 'number' || seconds <= 0) return null
  if (window.mode === 'rolling') {
    return page.plan.windowRolling(String(Math.max(1, Math.round(seconds / 60))))
  }
  const hours = seconds / 3600
  const hoursText = Number.isInteger(hours) ? String(hours) : hours.toFixed(1)
  return page.plan.windowFixed(hoursText, window.reset_timezone ?? 'UTC')
}

function windowsValid(windows: { start: string; end: string }[]): boolean {
  return windows.every(
    (window) => timeRe.test(window.start) && timeRe.test(window.end) && window.start < window.end,
  )
}

function StepRail({ step, onJump }: { step: number; onJump: (next: WizardStep) => void }) {
  return (
    <ol className="mt-6 grid grid-cols-2 gap-3 rounded-md border border-line bg-bg p-4 sm:grid-cols-4">
      {page.steps.map((title, index) => {
        const completed = index < step
        const active = index === step
        return (
          <li
            key={title}
            aria-current={active ? 'step' : undefined}
            className={cn(
              'rounded-md border p-3.5',
              active ? 'border-brand bg-brand-soft/50' : 'border-line bg-bg',
            )}
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
                  'grid h-6 w-6 shrink-0 place-items-center rounded-full border text-[13px] font-medium',
                  completed
                    ? 'border-transparent bg-brand-soft text-brand-ink'
                    : active
                      ? 'border-transparent bg-brand text-white'
                      : 'border-line bg-bg-soft text-ink-3',
                )}
              >
                {completed ? (
                  <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
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
                  'text-[14px] font-medium',
                  completed || active ? 'text-ink' : 'text-ink-3',
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
        <Tag tone={probe.key_valid ? 'brand' : 'danger'} dot>
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

      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <div className="rounded-md bg-bg-soft px-4 py-3">
          <dt className="text-[13px] text-ink-3">{page.verify.balance}</dt>
          <dd className="mt-0.5 tabular-nums text-[14px] text-ink">
            {probe.balance_micro === null || probe.balance_micro === undefined
              ? page.verify.balanceUnknown
              : formatYuan(probe.balance_micro)}
          </dd>
        </div>
        <div className="rounded-md bg-bg-soft px-4 py-3">
          <dt className="text-[13px] text-ink-3">{page.verify.latency}</dt>
          <dd className="mt-0.5 tabular-nums text-[14px] text-ink">
            {probe.latency_ms} ms
          </dd>
        </div>
        <div className="rounded-md bg-bg-soft px-4 py-3">
          <dt className="text-[13px] text-ink-3">{page.verify.cache}</dt>
          <dd className="mt-0.5 text-[14px] text-ink">
            {probe.prompt_cache ? page.verify.cacheYes : page.verify.cacheNo}
          </dd>
        </div>
      </dl>

      {probe.fail_reason ? (
        <div className="rounded-md border border-warn/25 bg-warn/10 px-4 py-3 text-[13px] leading-5 text-warn">
          {page.verify.failReason}：{probeFailReasonLabel(probe.fail_reason)}
        </div>
      ) : null}

      {probe.missing_models.length > 0 ? (
        <p className="text-[13px] leading-5 text-warn">
          {page.verify.missing(probe.missing_models.join('、'))}
        </p>
      ) : null}
    </div>
  )
}

function VendorGroup({
  title,
  vendors,
  disabled = false,
  selectedId,
  badge,
  emptyLabel,
  onSelect,
}: {
  title: string
  vendors: SellerVendor[]
  disabled?: boolean
  selectedId: string
  badge?: string
  emptyLabel?: string
  onSelect: (id: string) => void
}) {
  return (
    <div>
      <h4 className="text-[13px] font-medium text-ink-3">{title}</h4>
      {vendors.length === 0 ? (
        <p className="mt-2 text-[13px] leading-5 text-ink-3">{emptyLabel}</p>
      ) : (
        <div className="mt-2 grid gap-2 sm:grid-cols-3">
          {vendors.map((vendor) => {
            const checked = !disabled && selectedId === vendor.id
            return (
              <label
                key={vendor.id}
                aria-disabled={disabled || undefined}
                className={cn(
                  'flex items-center justify-between gap-3 rounded-md border px-4 py-3.5 transition-colors',
                  disabled
                    ? 'cursor-not-allowed border-line bg-bg-soft opacity-70'
                    : 'cursor-pointer',
                  checked
                    ? 'border-brand bg-brand-soft/60'
                    : !disabled && 'border-line bg-bg hover:border-brand/40',
                )}
              >
                <span className="min-w-0">
                  <span className="flex items-center gap-1.5">
                    <span className="block truncate text-[13px] font-medium text-ink">
                      {vendor.display_name}
                    </span>
                    {badge ? <Tag>{badge}</Tag> : null}
                  </span>
                  <span className="block truncate tabular-nums text-[13px] text-ink-3">
                    {vendor.id}
                  </span>
                </span>
                <input
                  type="radio"
                  name="vendor_id"
                  value={vendor.id}
                  checked={checked}
                  disabled={disabled}
                  onChange={() => onSelect(vendor.id)}
                  className="h-4 w-4 shrink-0 accent-brand"
                />
              </label>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function NewListingPage() {
  const toast = useToast()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [step, setStep] = useState<WizardStep>(0)
  const [multiplierText, setMultiplierText] = useState('0.500')
  const [previewModel, setPreviewModel] = useState('')
  const [duplicate, setDuplicate] = useState<DuplicateKeyIssue | null>(null)

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
      plan_terms_accepted: false,
    },
  })
  const values = form.watch()
  const { fields, append, remove } = useFieldArray({ control: form.control, name: 'hours' })

  const vendors = useQuery(vendorsQuery())
  const marketplaceStatus = useQuery(marketplaceStatusQuery())
  // The backend is the single source for the split; the fallback only covers
  // the brief window before the status query resolves.
  const feePercent = marketplaceStatus.data?.platform_fee_percent ?? DEFAULT_FEE_PERCENT
  const allVendors = vendors.data ?? []
  const officialVendors = allVendors.filter((vendor) => sellerChannelKind(vendor) === 'official')
  const planVendors = allVendors.filter((vendor) => sellerChannelKind(vendor) === 'plan')
  const thirdPartyVendors = allVendors.filter(
    (vendor) => sellerChannelKind(vendor) === 'third_party',
  )
  const vendorList = [...officialVendors, ...planVendors, ...thirdPartyVendors]
  const selectedVendor = vendorList.find((vendor) => vendor.id === values.vendor_id)
  const vendorModels = selectedVendor?.models ?? []
  const vendorHasModels = Boolean(selectedVendor && vendorModels.length > 0)
  const requiresPlanTerms = Boolean(
    selectedVendor && sellerChannelKind(selectedVendor) === 'plan',
  )
  const quotaWindowText = requiresPlanTerms
    ? planWindowText(selectedVendor?.quota_window)
    : null

  const probe = useMutation({
    mutationFn: probeListing,
    onSuccess: (result) => {
      // The writable catalog is the channel's certified model list; keep only
      // the models the probe says this key can actually call. `verified_models`
      // is the pool contract, `models` the legacy alias.
      const certified = result.verified_models ?? result.models
      const available = certified.filter((model) => vendorModels.includes(model))
      form.setValue('models', available, { shouldValidate: true })
    },
  })

  const create = useMutation({
    mutationFn: createListing,
    onSuccess: () => {
      setDuplicate(null)
      notify(toast, { title: page.success, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: sellerListingsQuery().queryKey })
      void navigate({ to: '/console/listings' })
    },
    onError: (error) => setDuplicate(duplicateKeyIssue(error)),
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
    requiresPlanTerms,
    planTermsAccepted: values.plan_terms_accepted,
  }

  const effectivePreviewModel =
    previewModel && values.models.includes(previewModel) ? previewModel : values.models[0] ?? ''
  const market = useQuery({
    ...squareModelForSeller(effectivePreviewModel),
    enabled: step === 3 && Boolean(effectivePreviewModel),
  })

  const tier = market.data?.tier ?? 'flat'
  const officialPrice = market.data?.official_prices?.[tier]?.input_micro_per_mtok
  // The backend Quote applies both the listing multiplier and the channel's
  // price coefficient; use the probe's coefficient for the same buyer price.
  const coefficientMilli = probe.data?.coefficients?.[effectivePreviewModel] ?? 1000
  const preview =
    officialPrice !== undefined && officialPrice !== null
      ? previewMultiplier(
          officialPrice,
          values.multiplier_milli,
          feePercent,
          coefficientMilli,
        )
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
      if (!vendorHasModels) return
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
      if (!vendorHasModels) return
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
    setDuplicate(null)
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
      plan_terms_accepted: requiresPlanTerms ? data.plan_terms_accepted : false,
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

  function selectVendor(id: string) {
    form.setValue('vendor_id', id, { shouldValidate: true, shouldDirty: true })
    form.setValue('models', [], { shouldValidate: true })
    form.setValue('plan_terms_accepted', false)
    setDuplicate(null)
    probe.reset()
  }

  const submitError = create.isError ? sellerErrorMessage(create.error) : null
  const probedModels = probe.data?.verified_models ?? probe.data?.models ?? []
  // Certified models the key did not expose on GET /models. They are shown
  // disabled above; this line names them and explains the missing-model case.
  const uncallableModels = vendorModels.filter((model) => !probedModels.includes(model))

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />
      <StepRail step={step} onJump={handleJump} />

      <form
        className="mt-6 rounded-md border border-line bg-bg p-6"
        onSubmit={form.handleSubmit(onSubmit)}
      >
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-4">
          <div>
            <h2 className="text-[16px] font-semibold tracking-tight text-ink">
              {page.steps[step]}
            </h2>
            <p className="mt-1 text-[13px] leading-5 text-ink-3">{page.stepLead[step]}</p>
          </div>
          <span className="tabular-nums text-[13px] text-ink-3">0{step + 1} / 04</span>
        </header>

        <div className="pt-5">
          {step === 0 ? (
            <div className="space-y-6">
              <Field
                label={page.vendor.label}
                hint={page.vendor.hint}
                error={form.formState.errors.vendor_id?.message}
              >
                {vendors.isPending ? (
                  <div className="grid gap-2 sm:grid-cols-3" aria-busy="true" aria-live="polite">
                    <span className="sr-only">{page.vendor.loading}</span>
                    {Array.from({ length: 6 }, (_, index) => (
                      <Skeleton key={index} className="h-[62px] w-full" />
                    ))}
                  </div>
                ) : vendors.isError ? (
                  <div className="rounded-md border border-danger/25 bg-danger/10 px-4 py-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <p className="text-[13px] leading-5 text-danger">
                        {page.vendor.loadFailed}
                      </p>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => void vendors.refetch()}
                      >
                        {copy.actions.retry}
                      </Button>
                    </div>
                  </div>
                ) : vendorList.length === 0 && thirdPartyVendors.length === 0 ? (
                  <p className="text-[13px] leading-5 text-ink-3">{page.vendor.empty}</p>
                ) : (
                  <div
                    role="radiogroup"
                    aria-label={page.vendor.label}
                    className="space-y-5"
                  >
                    <VendorGroup
                      title={page.channel.groups.official}
                      vendors={officialVendors}
                      selectedId={values.vendor_id}
                      badge={page.channel.official.badge}
                      emptyLabel={page.vendor.groupEmpty}
                      onSelect={selectVendor}
                    />
                    <VendorGroup
                      title={page.channel.groups.plan}
                      vendors={planVendors}
                      selectedId={values.vendor_id}
                      badge={page.channel.plan.badge}
                      emptyLabel={page.vendor.groupEmpty}
                      onSelect={selectVendor}
                    />
                    <VendorGroup
                      title={page.channel.groups.thirdParty}
                      vendors={thirdPartyVendors}
                      selectedId={values.vendor_id}
                      badge={page.channel.thirdParty.badge}
                      emptyLabel={page.channel.thirdParty.description}
                      onSelect={selectVendor}
                    />
                  </div>
                )}
              </Field>

              {selectedVendor && !vendorHasModels ? (
                <p className="text-[13px] leading-5 text-warn" role="status">
                  {page.vendor.modelsEmpty}
                </p>
              ) : null}

              {requiresPlanTerms ? (
                <div className="rounded-md border border-brand/25 bg-brand-soft/40 px-4 py-3">
                  <h3 className="text-[13px] font-medium text-ink">{page.plan.title}</h3>
                  <p className="mt-1 text-[13px] leading-5 text-ink-2">{page.plan.windows}</p>
                  {quotaWindowText ? (
                    <p className="mt-1 text-[13px] leading-5 text-ink-2">{quotaWindowText}</p>
                  ) : null}
                  {selectedVendor?.terms_url ? (
                    <a
                      href={selectedVendor.terms_url}
                      target="_blank"
                      rel="noreferrer"
                      className="mt-1 inline-block text-[13px] text-brand-ink underline"
                    >
                      {page.plan.termsLink}
                    </a>
                  ) : null}
                </div>
              ) : null}
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
                <PasswordInput
                  id="new-listing-key"
                  autoComplete="new-password"
                  spellCheck={false}
                  placeholder={page.key.placeholder}
                  invalid={Boolean(form.formState.errors.api_key)}
                  {...form.register('api_key')}
                />
              </Field>
              <div className="rounded-md bg-bg-soft px-4 py-3 text-[13px]">
                <span className="text-ink-3">
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
                  <p className="text-[13px] text-ink-3">{page.verify.probing}</p>
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
                      <span className="text-[13px] text-ink-3">
                        {page.verify.selected(values.models.length, vendorModels.length)}
                      </span>
                    </div>
                    <p className="mt-1 text-[13px] text-ink-3">{page.verify.modelsHint}</p>

                    {vendorModels.length === 0 ? (
                      <p className="mt-3 text-[13px] text-warn" role="status">
                        {page.vendor.modelsEmpty}
                      </p>
                    ) : (
                      <div className="mt-3 grid gap-2 sm:grid-cols-2">
                        {vendorModels.map((model) => {
                          const callable = probedModels.includes(model)
                          return (
                            <label
                              key={model}
                              className={cn(
                                'flex items-center gap-2 rounded-md border border-line px-3 py-2 text-[13px]',
                                callable
                                  ? 'cursor-pointer text-ink hover:border-brand/40'
                                  : 'cursor-not-allowed text-ink-3 opacity-70',
                              )}
                            >
                              <input
                                type="checkbox"
                                checked={values.models.includes(model)}
                                disabled={!callable}
                                onChange={(event) => toggleModel(model, event.target.checked)}
                                className="h-4 w-4 accent-brand"
                              />
                              <span className="tabular-nums">{model}</span>
                            </label>
                          )
                        })}
                      </div>
                    )}
                    {probe.data && uncallableModels.length > 0 ? (
                      <p className="mt-2 text-[13px] leading-5 text-warn" role="status">
                        {page.verify.missing(uncallableModels.join('、'))}
                      </p>
                    ) : null}
                    {form.formState.errors.models?.message ? (
                      <p className="mt-2 text-[13px] text-danger" role="alert">
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
                          className="h-1.5 w-full cursor-pointer accent-brand"
                          aria-label={page.pricing.label}
                        />
                        <Input
                          aria-label={page.pricing.inputLabel}
                          className="w-24 shrink-0 text-right"
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
                    <p className="mt-1 text-[13px] text-danger" role="alert">
                      {form.formState.errors.multiplier_milli.message}
                    </p>
                  ) : null}

                  {requiresPlanTerms ? (
                    <div className="mt-4 rounded-md border border-brand/25 bg-brand-soft/40 px-4 py-3">
                      <label className="flex items-start gap-2 text-[13px] text-ink">
                        <input
                          type="checkbox"
                          className="mt-0.5 h-4 w-4 shrink-0 accent-brand"
                          {...form.register('plan_terms_accepted')}
                        />
                        <span>{page.plan.terms}</span>
                      </label>
                      <p className="mt-1 pl-6 text-[13px] leading-5 text-ink-3">
                        {page.plan.termsHint}
                      </p>
                      {!values.plan_terms_accepted ? (
                        <p className="mt-1 pl-6 text-[13px] text-warn" role="status">
                          {page.plan.required}
                        </p>
                      ) : null}
                    </div>
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
                        {...form.register('daily_cap_yuan')}
                      />
                    </Field>
                  </div>
                </div>

                <div className="border-t border-line pt-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div>
                      <h3 className="text-[13px] font-medium text-ink">{page.pricing.hours}</h3>
                      <p className="mt-1 text-[13px] text-ink-3">{page.pricing.hoursHint}</p>
                    </div>
                    <label className="flex items-center gap-2 text-[13px] text-ink">
                      <input
                        type="checkbox"
                        className="h-4 w-4 accent-brand"
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
                        <p className="text-[13px] text-ink-3">{page.pricing.noWindow}</p>
                      ) : null}
                      {fields.map((field, index) => (
                        <div key={field.id} className="flex flex-wrap items-end gap-2">
                          <Field
                            label={page.pricing.start}
                            className="w-[132px]"
                            error={form.formState.errors.hours?.[index]?.start?.message}
                          >
                            <Input type="time" {...form.register(`hours.${index}.start`)} />
                          </Field>
                          <span className="pb-2 text-ink-3" aria-hidden="true">
                            –
                          </span>
                          <Field
                            label={page.pricing.end}
                            className="w-[132px]"
                            error={form.formState.errors.hours?.[index]?.end?.message}
                          >
                            <Input type="time" {...form.register(`hours.${index}.end`)} />
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
                    <p className="mt-2 text-[13px] text-ink-3">{page.pricing.noWindow}</p>
                  )}
                </div>
              </div>

              <aside className="rounded-md bg-bg-soft p-5">
                <h3 className="text-[14px] font-semibold text-ink">{page.pricing.title}</h3>

                {values.models.length > 1 ? (
                  <label className="mt-3 block">
                    <span className="block text-[13px] text-ink-3">
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
                  <div className="mt-2 tabular-nums text-[13px] text-ink-3">
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
                      <dt className="text-[13px] text-ink-3">{page.pricing.official}</dt>
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
                      <dt className="text-[13px] text-ink-3">{page.pricing.coefficient}</dt>
                      <dd className="tabular-nums text-[13px] text-ink-3">
                        ×{formatMultiplier(coefficientMilli)}
                      </dd>
                    </div>
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-[13px] text-ink-3">{page.pricing.buyer}</dt>
                      <dd>
                        <Price value={preview.buyerMicro} tier={tier} size="sm" unit={null} />
                      </dd>
                    </div>
                    <p className="text-[13px] leading-5 text-ink-3">
                      {page.pricing.buyerHint}
                    </p>
                    <div className="flex items-baseline justify-between gap-3 border-t border-line pt-3">
                      <dt className="text-[13px] text-ink-3">{page.pricing.seller}</dt>
                      <dd className="text-[18px] font-semibold tabular-nums text-brand-ink">
                        {formatYuan(preview.sellerMicro)}
                      </dd>
                    </div>
                    <p className="text-[13px] leading-5 text-ink-3">
                      {page.pricing.sellerHint(preview.feePercent)}
                    </p>
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-[13px] text-ink-3">{page.pricing.fee}</dt>
                      <dd className="text-[13px] tabular-nums text-ink-3">
                        {formatYuan(preview.feeMicro)} ·{' '}
                        {page.pricing.feeRate(preview.feePercent)}
                      </dd>
                    </div>
                  </dl>
                ) : (
                  <p className="mt-4 text-[13px] leading-5 text-ink-3">
                    {page.pricing.noOfficial}
                  </p>
                )}

                <div className="mt-4 border-t border-line pt-3">
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="text-[13px] text-ink-3">{page.pricing.marketLowest}</span>
                    <span>
                      {marketMin ? (
                        <Multiplier milli={marketMin} showSaving={false} />
                      ) : (
                        <span className="text-[13px] text-ink-3">—</span>
                      )}
                    </span>
                  </div>
                  {comparison ? (
                    <p
                      className={cn(
                        'mt-2 text-[13px]',
                        comparison === 'lower'
                          ? 'text-brand-ink'
                          : comparison === 'higher'
                            ? 'text-warn'
                            : 'text-ink-3',
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
                    <p className="mt-2 text-[13px] text-ink-3">{page.pricing.noMarket}</p>
                  )}
                </div>

                <div className="mt-4 border-t border-line pt-3 text-[13px] leading-5 text-ink-3">
                  {page.pricing.summary(
                    formatMultiplier(values.multiplier_milli),
                    savingPercent(values.multiplier_milli),
                  )}
                  <span className="mt-1 block text-[13px] text-ink-3/80">
                    {page.pricing.tierNote(copy.tiers[tier])}
                  </span>
                </div>
              </aside>
            </div>
          ) : null}

          {duplicate ? (
            duplicate.kind === 'own' ? (
              <div
                className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-warn/30 bg-warn/10 px-4 py-3"
                role="alert"
              >
                <p className="text-[13px] leading-5 text-warn">
                  {duplicate.listingId
                    ? page.duplicate.own(duplicate.listingId)
                    : copy.pages.sellerCommon.errors.duplicateKeyOwn}
                </p>
                {duplicate.listingId ? (
                  <Link
                    to="/console/listings/$id"
                    params={{ id: String(duplicate.listingId) }}
                    className="text-[13px] font-medium text-brand-ink underline"
                  >
                    {page.duplicate.ownLink}
                  </Link>
                ) : (
                  <Link
                    to="/console/listings"
                    className="text-[13px] font-medium text-brand-ink underline"
                  >
                    {page.duplicate.ownLink}
                  </Link>
                )}
              </div>
            ) : (
              <p className="mt-4 text-[13px] text-danger" role="alert">
                {page.duplicate.other}
              </p>
            )
          ) : submitError ? (
            <p className="mt-4 text-[13px] text-danger" role="alert">
              {submitError}
            </p>
          ) : null}
        </div>

        <footer className="mt-6 flex items-center justify-between gap-2 border-t border-line pt-4">
          <div className="text-[13px] text-ink-3">
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
                disabled={
                  !canLeaveStep(step, snapshot) ||
                  ((step === 0 || step === 2) && !vendorHasModels) ||
                  probe.isPending
                }
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
