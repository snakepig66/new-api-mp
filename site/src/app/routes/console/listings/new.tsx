import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'
import { useForm, type UseFormRegister } from 'react-hook-form'
import { z } from 'zod'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import {
  Button,
  Input,
  PageHeader,
  Price,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  useToast,
} from '@/ui'

import {
  createListing,
  marketplaceStatusQuery,
  probeListing,
  sellerChannelKind,
  sellerCommonAddressesQuery,
  sellerGroupsQuery,
  sellerListingsQuery,
  sellerVendorsQuery,
  squareModelForSeller,
  type ProbeResult,
  type SellerQuotaWindow,
  type SellerVendor,
  type SellerCommonAddress,
  type CreateListingInput,
} from '@/features/seller/api'
import { Field, notify, Skeleton } from '@/features/seller/components'
import {
  duplicateKeyIssue,
  sellerErrorMessage,
  type DuplicateKeyIssue,
} from '@/features/seller/errors'
import { parseYuanToMicro } from '@/features/seller/money-input'
import { DEFAULT_FEE_PERCENT, previewMultiplier } from '@/features/seller/preview'
import {
  buildVerificationMatrix,
  canLeaveStep,
  canSubmit,
  CUSTOM_VENDOR_ID,
  DEFAULT_MULTIPLIER_MILLI,
  maskApiKey,
  parseApiKeys,
  previousWizardStep,
  validApiKeys,
  validCustomURL,
  validModelMultiplier,
  validRequestHeaders,
  type VerificationCell,
  type WizardSnapshot,
  type WizardStep,
} from '@/features/seller/wizard'

export const Route = createFileRoute('/console/listings/new')({
  component: NewListingPage,
})

const page = copy.pages.sellerListingNew
const validation = page.validation
const timeRe = /^([01]\d|2[0-3]):[0-5]\d$/
const PROBE_DEBOUNCE_MS = 500
const DAY_KEYS = ['weekday', 'weekend', 'holiday'] as const
type DayKey = (typeof DAY_KEYS)[number]

const windowGroupSchema = z.object({
  enabled: z.boolean(),
  peakStart: z.string(),
  peakEnd: z.string(),
  idleStart: z.string(),
  idleEnd: z.string(),
})

const wizardSchema = z
  .object({
    url: z
      .string()
      .trim()
      .min(1, validation.url)
      .refine((value) => validCustomURL(value), validation.url),
    api_keys: z.string().refine((value) => validApiKeys(parseApiKeys(value)), validation.key),
    models: z.array(z.string()).min(1, validation.models),
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
    availability: z.object({
      weekday: windowGroupSchema,
      weekend: windowGroupSchema,
      holiday: windowGroupSchema,
    }),
    key_strategy: z.enum(['weighted', 'primary_backup']),
    affinity: z.boolean(),
    headers: z.string(),
    extra_endpoints: z.array(z.string()),
    plan_terms_accepted: z.boolean(),
  })
  .superRefine((value, ctx) => {
    for (const day of DAY_KEYS) {
      const group = value.availability[day]
      if (!group.enabled) continue
      for (const [startKey, endKey] of [
        ['peakStart', 'peakEnd'],
        ['idleStart', 'idleEnd'],
      ] as const) {
        const start = group[startKey]
        const end = group[endKey]
        if (!timeRe.test(start) || !timeRe.test(end) || end <= start) {
          ctx.addIssue({
            code: 'custom',
            path: ['availability', day, endKey],
            message: validation.hours,
          })
        }
      }
    }
    if (value.headers.trim() && !validRequestHeaders(value.headers)) {
      ctx.addIssue({ code: 'custom', path: ['headers'], message: validation.headers })
    }
  })

type WizardForm = z.infer<typeof wizardSchema>

const emptyWindowGroup = {
  enabled: false,
  peakStart: '09:00',
  peakEnd: '12:00',
  idleStart: '12:00',
  idleEnd: '18:00',
}

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

function hostOf(raw: string): string | null {
  if (!raw) return null
  try {
    return new URL(raw).hostname.toLowerCase()
  } catch {
    return null
  }
}

/** The registered channel whose protocol host matches the typed address. */
function vendorForURL(raw: string, vendors: readonly SellerVendor[]): SellerVendor | undefined {
  const host = hostOf(raw.trim())
  if (!host) return undefined
  return vendors.find((vendor) => {
    return (
      hostOf(vendor.protocols.openai) === host || hostOf(vendor.protocols.anthropic) === host
    )
  })
}

function defaultEndpointsFor(vendor: SellerVendor | undefined): string[] {
  if (!vendor) return ['openai_chat']
  const out: string[] = []
  if (vendor.protocols.openai) out.push('openai_chat')
  if (vendor.protocols.anthropic) out.push('anthropic_messages')
  return out.length > 0 ? out : ['openai_chat']
}

interface FlatHours {
  windows: { start: string; end: string }[]
  valid: boolean
  any: boolean
}

/** Flatten the day-type groups into the backend's [{start,end}] hours contract. */
function flattenHours(availability: WizardForm['availability']): FlatHours {
  const windows: { start: string; end: string }[] = []
  let valid = true
  let any = false
  for (const day of DAY_KEYS) {
    const group = availability[day]
    if (!group.enabled) continue
    any = true
    for (const [start, end] of [
      [group.peakStart, group.peakEnd],
      [group.idleStart, group.idleEnd],
    ]) {
      if (!timeRe.test(start) || !timeRe.test(end) || end <= start) {
        valid = false
        continue
      }
      if (!windows.some((w) => w.start === start && w.end === end)) {
        windows.push({ start, end })
      }
    }
  }
  return { windows, valid, any }
}

function StepRail({ step, onJump }: { step: number; onJump: (next: WizardStep) => void }) {
  return (
    <ol className="mt-6 grid grid-cols-2 gap-3 rounded-md border border-line bg-bg p-4 sm:grid-cols-5">
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

function endpointLabel(endpoint: string): string {
  return page.advanced.endpointNames[endpoint] ?? endpoint
}

function TrustBadge({
  trust,
  recognizing,
  error,
}: {
  trust: ProbeResult['trust_pool']
  recognizing: boolean
  error: string | null
}) {
  if (recognizing) {
    return (
      <Tag dot aria-live="polite">
        {page.trust.probing}
      </Tag>
    )
  }
  if (error) {
    return <span className="text-[13px] leading-5 text-danger">{error}</span>
  }
  if (!trust) {
    return <span className="text-[13px] text-ink-3">{page.trust.idle}</span>
  }
  const tone = trust.trust === 'blocked' ? 'danger' : trust.trust === 'trusted' ? 'brand' : 'neutral'
  return (
    <div className="flex flex-wrap items-center gap-2" aria-live="polite">
      <Tag tone={tone} dot>
        {trust.badge_text || page.trust.unlisted}
      </Tag>
      {trust.endpoints.length > 0
        ? trust.endpoints.map((endpoint) => (
            <Tag key={endpoint} tone="neutral">
              {endpointLabel(endpoint)}
            </Tag>
          ))
        : null}
    </div>
  )
}

function CommonAddressList({
  addresses,
  pending,
  error,
  onPick,
  onRetry,
}: {
  addresses: SellerCommonAddress[]
  pending: boolean
  error: boolean
  onPick: (address: SellerCommonAddress) => void
  onRetry: () => void
}) {
  return (
    <div>
      <h4 className="text-[13px] font-medium text-ink-3">{page.url.commonTitle}</h4>
      <p className="mt-1 text-[13px] text-ink-3">{page.url.commonHint}</p>
      {pending ? (
        <div className="mt-2 flex flex-wrap gap-2" aria-busy="true">
          {Array.from({ length: 4 }, (_, index) => (
            <Skeleton key={index} className="h-9 w-44" />
          ))}
        </div>
      ) : error ? (
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <span className="text-[13px] text-danger">{page.url.commonError}</span>
          <Button variant="secondary" size="sm" onClick={onRetry}>
            {page.actions.retry}
          </Button>
        </div>
      ) : addresses.length === 0 ? (
        <p className="mt-2 text-[13px] text-ink-3">{page.url.commonEmpty}</p>
      ) : (
        <div className="mt-2 flex flex-wrap gap-2">
          {addresses.map((address) => (
            <button
              key={address.host}
              type="button"
              onClick={() => onPick(address)}
              className="flex items-center gap-2 rounded-md border border-line bg-bg px-3 py-2 text-left text-[13px] transition-colors hover:border-brand/40"
            >
              <span className="font-medium text-ink">{address.vendor_name || address.host}</span>
              <span className="font-mono text-[12px] text-ink-3">{address.host}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function BuyerPriceCell({
  model,
  multiplierMilli,
  coefficientMilli,
  feePercent,
}: {
  model: string
  multiplierMilli: number
  coefficientMilli: number
  feePercent: number
}) {
  const market = useQuery(squareModelForSeller(model))
  const tier = market.data?.tier ?? 'flat'
  const officialPrice = market.data?.official_prices?.[tier]?.input_micro_per_mtok
  if (market.isPending) {
    return <Skeleton className="h-4 w-20" />
  }
  if (officialPrice === undefined || officialPrice === null) {
    return <span className="text-[13px] text-ink-3">{page.models.buyerUnavailable}</span>
  }
  const preview = previewMultiplier(officialPrice, multiplierMilli, feePercent, coefficientMilli)
  if (!preview) {
    return <span className="text-[13px] text-ink-3">{page.models.buyerUnavailable}</span>
  }
  return <Price value={preview.buyerMicro} tier={tier} size="sm" unit={null} />
}

function AvailabilityGroup({
  day,
  label,
  register,
  error,
}: {
  day: DayKey
  label: string
  register: UseFormRegister<WizardForm>
  error?: string
}) {
  return (
    <div className="rounded-md border border-line p-4">
      <label className="flex items-center gap-2 text-[13px] font-medium text-ink">
        <input
          type="checkbox"
          className="h-4 w-4 accent-brand"
          {...register(`availability.${day}.enabled`)}
        />
        {page.limits.enable(label)}
      </label>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {(
          [
            ['peak', 'peakStart', 'peakEnd'],
            ['idle', 'idleStart', 'idleEnd'],
          ] as const
        ).map(([tier, startKey, endKey]) => (
          <div key={tier}>
            <span className="block text-[13px] text-ink-3">
              {tier === 'peak' ? page.limits.peak : page.limits.idle}
            </span>
            <div className="mt-1 flex items-center gap-2">
              <Input
                type="time"
                aria-label={`${label}${tier === 'peak' ? page.limits.peak : page.limits.idle}${page.limits.start}`}
                {...register(`availability.${day}.${startKey}` as const)}
              />
              <span className="text-ink-3" aria-hidden="true">
                –
              </span>
              <Input
                type="time"
                aria-label={`${label}${tier === 'peak' ? page.limits.peak : page.limits.idle}${page.limits.end}`}
                {...register(`availability.${day}.${endKey}` as const)}
              />
            </div>
          </div>
        ))}
      </div>
      {error ? (
        <p className="mt-2 text-[13px] text-danger" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}

export function NewListingPage() {
  const toast = useToast()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [step, setStep] = useState<WizardStep>(0)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [multipliers, setMultipliers] = useState<Record<string, number>>({})
  // P4: per-key weight (weighted_round_robin) keyed by the raw key text.
  const [weights, setWeights] = useState<Record<string, number>>({})
  const [batchMultiplier, setBatchMultiplier] = useState('0.5')
  const [batchApplied, setBatchApplied] = useState(false)
  const [recognition, setRecognition] = useState<ProbeResult | null>(null)
  const [recognizing, setRecognizing] = useState(false)
  const [recognizeError, setRecognizeError] = useState<string | null>(null)
  const [keyProbes, setKeyProbes] = useState<Record<string, ProbeResult>>({})
  const [probeErrors, setProbeErrors] = useState<Record<string, string>>({})
  const [probingKeys, setProbingKeys] = useState(false)
  const [manualEndpoints, setManualEndpoints] = useState<string[]>([])
  const [duplicate, setDuplicate] = useState<DuplicateKeyIssue | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)

  const form = useForm<WizardForm>({
    resolver: zodResolver(wizardSchema),
    mode: 'onTouched',
    defaultValues: {
      url: '',
      api_keys: '',
      models: [],
      concurrency: 5,
      rpm: 0,
      daily_cap_yuan: '0',
      availability: {
        weekday: { ...emptyWindowGroup },
        weekend: { ...emptyWindowGroup },
        holiday: { ...emptyWindowGroup },
      },
      key_strategy: 'weighted',
      affinity: true,
      headers: '',
      extra_endpoints: [],
      plan_terms_accepted: false,
    },
  })
  const values = form.watch()
  const keys = parseApiKeys(values.api_keys)
  const firstKey = keys[0] ?? ''

  const vendors = useQuery(sellerVendorsQuery())
  const commonAddresses = useQuery(sellerCommonAddressesQuery())
  // P4: an existing group for the matched vendor means the new keys join that
  // logical channel instead of creating another one.
  const groups = useQuery(sellerGroupsQuery())
  const marketplaceStatus = useQuery(marketplaceStatusQuery())
  const feePercent = marketplaceStatus.data?.platform_fee_percent ?? DEFAULT_FEE_PERCENT
  const allVendors = useMemo(() => vendors.data ?? [], [vendors.data])
  const matchedVendor = vendorForURL(values.url, allVendors)
  const existingGroup = useMemo(
    () =>
      matchedVendor
        ? (groups.data ?? []).find((group) => group.vendor_slug === matchedVendor.id)
        : undefined,
    [groups.data, matchedVendor],
  )
  const isCustomAddress = Boolean(validCustomURL(values.url) && !matchedVendor)
  const requiresPlanTerms = Boolean(
    matchedVendor && sellerChannelKind(matchedVendor) === 'plan',
  )
  const quotaWindowText = requiresPlanTerms
    ? planWindowText(matchedVendor?.quota_window)
    : null

  const probeInput = (key: string) => ({
    vendor_id: matchedVendor?.id ?? '',
    api_key: key,
    models: [] as string[],
    custom_base_url: matchedVendor ? undefined : values.url.trim(),
  })

  // Live recognition: debounce 500ms after the address and the first key are
  // both usable, then classify the address through the probe endpoint.
  const recognitionSignature = `${values.url.trim()}|${matchedVendor?.id ?? ''}|${firstKey}`
  useEffect(() => {
    setRecognizeError(null)
    if (!validCustomURL(values.url) || !validApiKeys([firstKey])) {
      setRecognition(null)
      return
    }
    let cancelled = false
    const timer = setTimeout(() => {
      setRecognizing(true)
      probeListing(probeInput(firstKey))
        .then((result) => {
          if (cancelled) return
          setRecognition(result)
          setKeyProbes((current) => ({ ...current, [firstKey]: result }))
        })
        .catch((error: unknown) => {
          if (!cancelled) setRecognizeError(sellerErrorMessage(error))
        })
        .finally(() => {
          if (!cancelled) setRecognizing(false)
        })
    }, PROBE_DEBOUNCE_MS)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
    // probeInput is derived from these values; listing them individually keeps
    // the effect from restarting on unrelated form updates.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recognitionSignature])

  const trust = recognition?.trust_pool ?? null
  const trustEndpoints = trust?.endpoints ?? []
  const trustEndpointSignature = trustEndpoints.join('|')

  useEffect(() => {
    setManualEndpoints(trustEndpoints)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trustEndpointSignature])

  const endpoints =
    manualEndpoints.length > 0
      ? manualEndpoints
      : trustEndpoints.length > 0
        ? trustEndpoints
        : defaultEndpointsFor(matchedVendor)

  const probed = Boolean(recognition) || Object.keys(keyProbes).length > 0
  const availableModels = useMemo(() => {
    if (!probed) return matchedVendor?.models ?? []
    const union = new Set<string>()
    Object.values(keyProbes).forEach((probe) => {
      for (const model of probe.verified_models ?? probe.models) union.add(model)
    })
    if (recognition) {
      for (const model of recognition.verified_models ?? recognition.models) union.add(model)
    }
    return Array.from(union).sort()
  }, [probed, keyProbes, recognition, matchedVendor])

  // Keep the selection and multiplier map aligned with what the keys can
  // actually call. When nothing is selected yet, preselect every available
  // model so the common case needs no extra clicks.
  useEffect(() => {
    const available = new Set(availableModels)
    const current = form.getValues('models')
    const kept = current.filter((model) => available.has(model))
    const next = kept.length > 0 ? kept : availableModels
    if (next.length !== current.length || next.some((model, index) => current[index] !== model)) {
      form.setValue('models', next, { shouldValidate: true })
    }
    setMultipliers((previous) => {
      const updated = { ...previous }
      for (const model of availableModels) {
        if (!validModelMultiplier(updated[model])) {
          updated[model] = DEFAULT_MULTIPLIER_MILLI
        }
      }
      return updated
    })
  }, [availableModels, form])

  const flatHours = flattenHours(values.availability)
  const headersValid = trust?.trust === 'unlisted' ? validRequestHeaders(values.headers) : true
  const snapshot: WizardSnapshot = {
    url: values.url,
    vendorId: matchedVendor?.id ?? (isCustomAddress ? CUSTOM_VENDOR_ID : ''),
    customBaseURL: matchedVendor ? undefined : values.url.trim(),
    apiKeys: keys,
    trust,
    probe: recognition,
    availableModels,
    selectedModels: values.models,
    modelMultipliers: multipliers,
    concurrency: values.concurrency,
    rpm: values.rpm,
    dailyCapYuan: values.daily_cap_yuan,
    hoursValid: flatHours.valid,
    headersValid,
    requiresPlanTerms,
    planTermsAccepted: values.plan_terms_accepted,
  }

  const create = useMutation({
    mutationFn: async (payloads: CreateListingInput[]) => {
      for (const payload of payloads) {
        await createListing(payload)
      }
      return payloads.length
    },
    onSuccess: (count) => {
      setDuplicate(null)
      setSubmitError(null)
      notify(toast, {
        title: count > 1 ? page.successCount(count) : page.success,
        tone: 'success',
      })
      void queryClient.invalidateQueries({ queryKey: sellerListingsQuery().queryKey })
      void navigate({ to: '/console/listings' })
    },
    onError: (error) => {
      const issue = duplicateKeyIssue(error)
      if (issue) setDuplicate(issue)
      else setSubmitError(sellerErrorMessage(error))
    },
  })

  async function probeOneKey(key: string, force: boolean): Promise<void> {
    if (!force && keyProbes[key]) return
    try {
      const result = await probeListing(probeInput(key))
      setKeyProbes((current) => ({ ...current, [key]: result }))
      setProbeErrors((current) => {
        const next = { ...current }
        delete next[key]
        return next
      })
    } catch (error) {
      setProbeErrors((current) => ({ ...current, [key]: sellerErrorMessage(error) }))
    }
  }

  async function probeAllKeys(force = false): Promise<void> {
    if (probingKeys || !validApiKeys(keys) || !validCustomURL(values.url)) return
    setProbingKeys(true)
    try {
      for (const key of keys) {
        await probeOneKey(key, force)
      }
    } finally {
      setProbingKeys(false)
    }
  }

  function handleNext() {
    if (!canLeaveStep(step, snapshot)) return
    if (step === 0) {
      void form.trigger(['url', 'api_keys'])
      setStep(1)
      void probeAllKeys()
      return
    }
    if (step === 1) {
      void form.trigger('models')
      setStep(2)
      return
    }
    if (step === 2) {
      void form.trigger(['concurrency', 'rpm', 'daily_cap_yuan', 'availability'])
      setStep(3)
      return
    }
    if (step === 3) {
      void form.trigger('headers')
      setAdvancedOpen(false)
      setStep(4)
    }
  }

  function handleJump(next: WizardStep) {
    if (next <= step) setStep(next)
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

  function setModelMultiplier(model: string, text: string) {
    const parsed = Number(text)
    if (!Number.isFinite(parsed)) return
    const milli = Math.min(1000, Math.max(100, Math.round(parsed * 1000)))
    setMultipliers((current) => ({ ...current, [model]: milli }))
  }

  function applyBatchMultiplier() {
    const parsed = Number(batchMultiplier)
    if (!Number.isFinite(parsed)) return
    const milli = Math.min(1000, Math.max(100, Math.round(parsed * 1000)))
    setMultipliers((current) => {
      const next = { ...current }
      for (const model of availableModels) next[model] = milli
      return next
    })
    setBatchApplied(true)
  }

  function payloadsFor(mode: 'passing' | 'draft'): CreateListingInput[] {
    const data = form.getValues()
    const dailyCap = parseYuanToMicro(data.daily_cap_yuan)
    if (dailyCap === null) return []
    const representative = data.models[0]
    const multiplierMilli = multipliers[representative] ?? DEFAULT_MULTIPLIER_MILLI
    if (!validModelMultiplier(multiplierMilli)) return []
    const hours = flatHours.windows.length > 0 ? JSON.stringify(flatHours.windows) : ''
    const keyStrategy: 'weighted_round_robin' | 'primary_backup' =
      data.key_strategy === 'primary_backup' ? 'primary_backup' : 'weighted_round_robin'
    const base = {
      vendor_id: matchedVendor?.id ?? '',
      multiplier_milli: multiplierMilli,
      concurrency: data.concurrency,
      rpm: data.rpm,
      daily_cap_micro: Number(dailyCap),
      hours,
      plan_terms_accepted: requiresPlanTerms ? data.plan_terms_accepted : false,
      custom_base_url: matchedVendor ? undefined : data.url.trim(),
      key_strategy: keyStrategy,
      affinity_enabled: data.affinity,
    }
    const payloads: CreateListingInput[] = []
    for (const key of keys) {
      const probe = keyProbes[key]
      const callable = new Set(probe ? probe.verified_models ?? probe.models : [])
      const models =
        mode === 'passing'
          ? data.models.filter((model) => probe?.key_valid && callable.has(model))
          : [...data.models]
      if (models.length === 0) continue
      const weight = Math.min(10000, Math.max(1, Math.round(weights[key] ?? 100)))
      payloads.push({ ...base, api_key: key, models, weight })
    }
    return payloads
  }

  function publishPassing() {
    const payloads = payloadsFor('passing')
    if (payloads.length === 0) return
    setDuplicate(null)
    setSubmitError(null)
    create.mutate(payloads)
  }

  function keepAsDraft() {
    const payloads = payloadsFor('draft')
    if (payloads.length === 0) return
    setDuplicate(null)
    setSubmitError(null)
    create.mutate(payloads)
  }

  function pickCommonAddress(address: SellerCommonAddress) {
    form.setValue('url', `https://${address.host}`, { shouldValidate: true, shouldDirty: true })
  }

  const verification = buildVerificationMatrix(keys, values.models, endpoints, keyProbes)
  const passedCount = verification.filter((cell) => cell.ok).length
  const hasPassingModels = keys.some((key) => {
    const probe = keyProbes[key]
    if (!probe?.key_valid) return false
    const callable = new Set(probe.verified_models ?? probe.models)
    return values.models.some((model) => callable.has(model))
  })

  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />
      <StepRail step={step} onJump={handleJump} />

      <form
        className="mt-6 rounded-md border border-line bg-bg p-6"
        onSubmit={(event) => event.preventDefault()}
      >
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-4">
          <div>
            <h2 className="text-[16px] font-semibold tracking-tight text-ink">
              {page.steps[step]}
            </h2>
            <p className="mt-1 text-[13px] leading-5 text-ink-3">{page.stepLead[step]}</p>
          </div>
          <span className="tabular-nums text-[13px] text-ink-3">0{step + 1} / 05</span>
        </header>

        <div className="pt-5">
          {step === 0 ? (
            <div className="space-y-6">
              <div className="max-w-2xl space-y-4">
                <Field
                  label={page.url.label}
                  htmlFor="new-listing-url"
                  hint={page.url.hint}
                  error={form.formState.errors.url?.message}
                >
                  <Input
                    id="new-listing-url"
                    type="url"
                    spellCheck={false}
                    placeholder={page.url.placeholder}
                    invalid={Boolean(form.formState.errors.url)}
                    {...form.register('url')}
                  />
                </Field>
                <Field
                  label={page.key.label}
                  htmlFor="new-listing-keys"
                  hint={page.key.hint}
                  error={form.formState.errors.api_keys?.message}
                >
                  <textarea
                    id="new-listing-keys"
                    rows={4}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={page.key.placeholder}
                    aria-invalid={Boolean(form.formState.errors.api_keys) || undefined}
                    className="w-full rounded-md border border-transparent bg-bg-soft px-4 py-3 font-mono text-[14px] text-ink transition-colors placeholder:text-ink-3 focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft aria-[invalid=true]:border-danger"
                    {...form.register('api_keys')}
                  />
                </Field>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-[13px] text-ink-3">
                    {keys.length > 0 ? page.key.count(keys.length) : page.key.empty}
                  </span>
                  <TrustBadge
                    trust={trust}
                    recognizing={recognizing}
                    error={recognizeError}
                  />
                </div>
                {trust?.trust === 'blocked' ? (
                  <p className="text-[13px] leading-5 text-danger" role="alert">
                    {page.trust.blocked}
                  </p>
                ) : trust?.trust === 'watch' || trust?.trust === 'unlisted' ? (
                  <p className="text-[13px] leading-5 text-warn" role="status">
                    {trust.trust === 'watch' ? page.trust.watch : page.trust.unlisted}
                  </p>
                ) : null}

                {existingGroup ? (
                  <div className="rounded-md border border-line bg-bg-soft px-4 py-3">
                    <h3 className="text-[13px] font-semibold text-ink">
                      {page.group.existingTitle}
                    </h3>
                    <p className="mt-1 text-[13px] leading-5 text-ink-3">
                      {page.group.existingHint(
                        existingGroup.key_count,
                        existingGroup.key_strategy,
                      )}
                    </p>
                    <div className="mt-2 flex flex-wrap gap-4">
                      <label className="flex items-center gap-2 text-[13px] text-ink">
                        <input type="radio" checked readOnly className="h-4 w-4 accent-brand" />
                        {page.group.join}
                      </label>
                      <label className="flex items-center gap-2 text-[13px] text-ink-3">
                        <input
                          type="radio"
                          disabled
                          aria-label={page.group.createNew}
                          className="h-4 w-4"
                        />
                        {page.group.createNew}
                      </label>
                    </div>
                    <p className="mt-1 text-[12px] leading-5 text-ink-3">
                      {page.group.createNewHint}
                    </p>
                  </div>
                ) : null}

                {keys.length > 1 ? (
                  <div>
                    <h3 className="text-[13px] font-semibold text-ink">
                      {page.group.keyOrderTitle}
                    </h3>
                    <p className="mt-1 text-[13px] leading-5 text-ink-3">
                      {values.key_strategy === 'primary_backup'
                        ? page.group.orderHint
                        : page.group.weightHint}
                    </p>
                    <ul className="mt-2 space-y-2">
                      {keys.map((key, index) => (
                        <li
                          key={`${index}-${key.slice(-6)}`}
                          className="flex flex-wrap items-center justify-between gap-3 rounded-md bg-bg-soft px-4 py-2"
                        >
                          <span className="font-mono text-[12px] text-ink-3">
                            {maskApiKey(key)}
                          </span>
                          {values.key_strategy === 'primary_backup' ? (
                            <Tag>{page.group.order(index + 1)}</Tag>
                          ) : (
                            <label className="flex items-center gap-2 text-[13px] text-ink-3">
                              {page.group.weight}
                              <Input
                                className="w-24"
                                type="number"
                                min={1}
                                max={10000}
                                aria-label={page.group.weightLabel(index + 1)}
                                value={weights[key] ?? 100}
                                onChange={(event) => {
                                  const parsed = Number(event.target.value)
                                  setWeights((current) => ({
                                    ...current,
                                    [key]: Number.isFinite(parsed) ? parsed : 100,
                                  }))
                                }}
                              />
                            </label>
                          )}
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}
              </div>

              <CommonAddressList
                addresses={commonAddresses.data ?? []}
                pending={commonAddresses.isPending}
                error={commonAddresses.isError}
                onPick={pickCommonAddress}
                onRetry={() => void commonAddresses.refetch()}
              />
            </div>
          ) : null}

          {step === 1 ? (
            <div className="space-y-5">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h3 className="text-[14px] font-semibold text-ink">{page.models.title}</h3>
                  <p className="mt-1 text-[13px] text-ink-3">{page.models.lead}</p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-2 text-[13px] text-ink-3">
                    {page.models.batchLabel}
                    <Input
                      className="w-24"
                      inputMode="decimal"
                      value={batchMultiplier}
                      onChange={(event) => {
                        setBatchMultiplier(event.target.value)
                        setBatchApplied(false)
                      }}
                    />
                  </label>
                  <Button variant="secondary" size="sm" onClick={applyBatchMultiplier}>
                    {page.models.applyAll}
                  </Button>
                  {batchApplied ? (
                    <span className="text-[13px] text-brand-ink" role="status">
                      {page.models.applied}
                    </span>
                  ) : null}
                </div>
              </div>
              <p className="text-[13px] text-ink-3">{page.models.batchHint}</p>

              {probingKeys ? (
                <div className="flex items-center gap-2" aria-busy="true" aria-live="polite">
                  <Skeleton className="h-4 w-4 rounded-full" />
                  <span className="text-[13px] text-ink-3">{page.models.loading}</span>
                </div>
              ) : availableModels.length === 0 ? (
                <p className="text-[13px] leading-5 text-warn" role="status">
                  {page.models.noModels}
                </p>
              ) : (
                <Table minWidth={760} size="compact">
                  <TableHead>
                    <TableRow>
                      <TableHeaderCell className="w-12">{page.models.columns.select}</TableHeaderCell>
                      <TableHeaderCell>{page.models.columns.model}</TableHeaderCell>
                      <TableHeaderCell>{page.models.columns.endpoint}</TableHeaderCell>
                      <TableHeaderCell>{page.models.columns.multiplier}</TableHeaderCell>
                      <TableHeaderCell>{page.models.columns.buyer}</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {availableModels.map((model) => {
                      const checked = values.models.includes(model)
                      const multiplierMilli = multipliers[model] ?? DEFAULT_MULTIPLIER_MILLI
                      const coefficientMilli = recognition?.coefficients?.[model] ?? 1000
                      return (
                        <TableRow key={model}>
                          <TableCell>
                            <input
                              type="checkbox"
                              aria-label={model}
                              checked={checked}
                              onChange={(event) => toggleModel(model, event.target.checked)}
                              className="h-4 w-4 accent-brand"
                            />
                          </TableCell>
                          <TableCell className="font-mono text-[13px]">{model}</TableCell>
                          <TableCell>
                            <div className="flex flex-wrap gap-1.5">
                              {endpoints.map((endpoint) => (
                                <Tag key={endpoint}>{endpointLabel(endpoint)}</Tag>
                              ))}
                            </div>
                          </TableCell>
                          <TableCell>
                            <Input
                              className="w-24"
                              type="number"
                              min={0.1}
                              max={1}
                              step={0.1}
                              aria-label={page.models.multiplierLabel(model)}
                              value={(multiplierMilli / 1000).toFixed(1)}
                              onChange={(event) => setModelMultiplier(model, event.target.value)}
                            />
                          </TableCell>
                          <TableCell>
                            <BuyerPriceCell
                              model={model}
                              multiplierMilli={multiplierMilli}
                              coefficientMilli={coefficientMilli}
                              feePercent={feePercent}
                            />
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              )}
              {Object.keys(keyProbes).length < keys.length && !probingKeys ? (
                <p className="text-[13px] text-ink-3" role="status">
                  {page.models.partial}
                </p>
              ) : null}
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-[13px] text-ink-3">
                  {page.models.selected(values.models.length)}
                </span>
                <Button
                  variant="secondary"
                  size="sm"
                  loading={probingKeys}
                  onClick={() => void probeAllKeys(true)}
                >
                  {probingKeys ? page.verify.rechecking : page.verify.recheck}
                </Button>
              </div>
              {new Set(values.models.map((model) => multipliers[model])).size > 1 ? (
                <p className="text-[13px] leading-5 text-warn" role="status">
                  {page.models.mixedMultiplier}
                </p>
              ) : null}
              {form.formState.errors.models?.message ? (
                <p className="text-[13px] text-danger" role="alert">
                  {form.formState.errors.models.message}
                </p>
              ) : null}
            </div>
          ) : null}

          {step === 2 ? (
            <div className="max-w-3xl space-y-6">
              <div>
                <h3 className="text-[14px] font-semibold text-ink">{page.limits.title}</h3>
                <p className="mt-1 text-[13px] text-ink-3">{page.limits.lead}</p>
              </div>
              <div className="grid gap-4 sm:grid-cols-3">
                <Field
                  label={page.limits.concurrency}
                  htmlFor="listing-concurrency"
                  hint={page.limits.concurrencyHint}
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
                  label={page.limits.rpm}
                  htmlFor="listing-rpm"
                  hint={page.limits.rpmHint}
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
                  label={page.limits.dailyCap}
                  htmlFor="listing-daily-cap"
                  hint={page.limits.dailyCapHint}
                  error={form.formState.errors.daily_cap_yuan?.message}
                >
                  <Input
                    id="listing-daily-cap"
                    inputMode="decimal"
                    {...form.register('daily_cap_yuan')}
                  />
                </Field>
              </div>

              <div className="border-t border-line pt-4">
                <h3 className="text-[14px] font-semibold text-ink">{page.limits.hoursTitle}</h3>
                <p className="mt-1 text-[13px] text-ink-3">{page.limits.hoursLead}</p>
                <div className="mt-3 grid gap-3">
                  {DAY_KEYS.map((day) => (
                    <AvailabilityGroup
                      key={day}
                      day={day}
                      label={page.limits.dayTypes[day]}
                      register={form.register}
                      error={
                        form.formState.errors.availability?.[day]?.idleEnd?.message ??
                        form.formState.errors.availability?.[day]?.peakEnd?.message
                      }
                    />
                  ))}
                </div>
                {!flatHours.any ? (
                  <p className="mt-2 text-[13px] text-ink-3">{page.limits.noHours}</p>
                ) : null}
              </div>

              {requiresPlanTerms ? (
                <div className="rounded-md border border-brand/25 bg-brand-soft/40 px-4 py-3">
                  <h3 className="text-[13px] font-medium text-ink">{page.plan.title}</h3>
                  <p className="mt-1 text-[13px] leading-5 text-ink-2">{page.plan.windows}</p>
                  {quotaWindowText ? (
                    <p className="mt-1 text-[13px] leading-5 text-ink-2">{quotaWindowText}</p>
                  ) : null}
                  {matchedVendor?.terms_url ? (
                    <a
                      href={matchedVendor.terms_url}
                      target="_blank"
                      rel="noreferrer"
                      className="mt-1 inline-block text-[13px] text-brand-ink underline"
                    >
                      {page.plan.termsLink}
                    </a>
                  ) : null}
                  <label className="mt-3 flex items-start gap-2 text-[13px] text-ink">
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
          ) : null}

          {step === 3 ? (
            <div className="max-w-2xl space-y-5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 className="text-[14px] font-semibold text-ink">{page.advanced.title}</h3>
                  <p className="mt-1 text-[13px] text-ink-3">{page.advanced.lead}</p>
                </div>
                <Button
                  variant="secondary"
                  size="sm"
                  aria-expanded={advancedOpen}
                  onClick={() => setAdvancedOpen((open) => !open)}
                >
                  {advancedOpen ? page.advanced.collapse : page.advanced.expand}
                </Button>
              </div>

              {advancedOpen ? (
                <div className="space-y-6">
                  <fieldset>
                    <legend className="text-[13px] font-medium text-ink">
                      {page.advanced.keyPolicy}
                    </legend>
                    <p className="mt-1 text-[13px] text-ink-3">{page.advanced.keyPolicyHint}</p>
                    <div className="mt-2 flex flex-wrap gap-4">
                      <label className="flex items-center gap-2 text-[13px] text-ink">
                        <input
                          type="radio"
                          value="weighted"
                          className="h-4 w-4 accent-brand"
                          {...form.register('key_strategy')}
                        />
                        {page.advanced.weighted}
                      </label>
                      <label className="flex items-center gap-2 text-[13px] text-ink">
                        <input
                          type="radio"
                          value="primary_backup"
                          className="h-4 w-4 accent-brand"
                          {...form.register('key_strategy')}
                        />
                        {page.advanced.primaryBackup}
                      </label>
                    </div>
                  </fieldset>

                  <div>
                    <label className="flex items-center gap-2 text-[13px] font-medium text-ink">
                      <input
                        type="checkbox"
                        className="h-4 w-4 accent-brand"
                        {...form.register('affinity')}
                      />
                      {page.advanced.affinity}
                    </label>
                    <p className="mt-1 pl-6 text-[13px] text-ink-3">
                      {page.advanced.affinityHint}
                    </p>
                  </div>

                  <div className="border-t border-line pt-4">
                    <h4 className="text-[13px] font-medium text-ink">{page.advanced.headers}</h4>
                    {trust?.trust === 'unlisted' ? (
                      <Field
                        label={page.advanced.headers}
                        htmlFor="new-listing-headers"
                        hint={page.advanced.headersHint}
                        error={form.formState.errors.headers?.message}
                      >
                        <textarea
                          id="new-listing-headers"
                          rows={3}
                          spellCheck={false}
                          placeholder={'X-Custom-Header: value'}
                          className="w-full rounded-md border border-transparent bg-bg-soft px-4 py-3 font-mono text-[14px] text-ink transition-colors placeholder:text-ink-3 focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft aria-[invalid=true]:border-danger"
                          {...form.register('headers')}
                        />
                      </Field>
                    ) : (
                      <p className="mt-2 text-[13px] text-ink-3">{page.advanced.headersLocked}</p>
                    )}
                  </div>

                  <div className="border-t border-line pt-4">
                    <h4 className="text-[13px] font-medium text-ink">
                      {page.advanced.endpoints}
                    </h4>
                    <p className="mt-1 text-[13px] text-ink-3">
                      {page.advanced.endpointsHint}
                    </p>
                    <div className="mt-2 flex flex-wrap gap-4">
                      {(['openai_chat', 'openai_responses', 'anthropic_messages'] as const).map(
                        (endpoint) => (
                          <label
                            key={endpoint}
                            className="flex items-center gap-2 text-[13px] text-ink"
                          >
                            <input
                              type="checkbox"
                              checked={manualEndpoints.includes(endpoint)}
                              onChange={(event) => {
                                setManualEndpoints((current) =>
                                  event.target.checked
                                    ? [...current, endpoint]
                                    : current.filter((item) => item !== endpoint),
                                )
                              }}
                              className="h-4 w-4 accent-brand"
                            />
                            {endpointLabel(endpoint)}
                          </label>
                        ),
                      )}
                    </div>
                  </div>
                </div>
              ) : null}
            </div>
          ) : null}

          {step === 4 ? (
            <div className="space-y-5">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h3 className="text-[14px] font-semibold text-ink">{page.verify.title}</h3>
                  <p className="mt-1 text-[13px] text-ink-3">{page.verify.lead}</p>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-[13px] tabular-nums text-ink-3">
                    {page.verify.passed(passedCount, verification.length)}
                  </span>
                  <Button
                    variant="secondary"
                    size="sm"
                    loading={probingKeys}
                    onClick={() => void probeAllKeys(true)}
                  >
                    {probingKeys ? page.verify.rechecking : page.verify.recheck}
                  </Button>
                </div>
              </div>

              {keys.length === 0 || values.models.length === 0 ? (
                <p className="text-[13px] text-ink-3">{page.verify.noSelection}</p>
              ) : (
                <Table minWidth={760} size="compact">
                  <TableHead>
                    <TableRow>
                      <TableHeaderCell>{page.verify.columns.key}</TableHeaderCell>
                      <TableHeaderCell>{page.verify.columns.model}</TableHeaderCell>
                      <TableHeaderCell>{page.verify.columns.endpoint}</TableHeaderCell>
                      <TableHeaderCell>{page.verify.columns.result}</TableHeaderCell>
                      <TableHeaderCell>{page.verify.columns.reason}</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {verification.map((cell: VerificationCell) => (
                      <TableRow key={`${cell.key}|${cell.model}|${cell.endpoint}`}>
                        <TableCell className="font-mono text-[13px]">
                          {maskApiKey(cell.key)}
                        </TableCell>
                        <TableCell className="font-mono text-[13px]">{cell.model}</TableCell>
                        <TableCell>{endpointLabel(cell.endpoint)}</TableCell>
                        <TableCell>
                          <Tag tone={cell.ok ? 'brand' : 'danger'} dot>
                            {cell.ok ? page.verify.pass : page.verify.fail}
                          </Tag>
                        </TableCell>
                        <TableCell className="text-[13px] text-ink-3">
                          {cell.ok
                            ? '—'
                            : probeErrors[cell.key] ||
                              cell.failReason ||
                              page.verify.notProbed}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
              <p className="text-[13px] leading-5 text-ink-3">{page.verify.endpointNote}</p>

              <div className="flex flex-wrap items-center gap-3 border-t border-line pt-4">
                <Button
                  onClick={publishPassing}
                  loading={create.isPending}
                  disabled={!canSubmit(snapshot) || !hasPassingModels}
                  title={!hasPassingModels ? page.verify.nothingPassed : undefined}
                >
                  {create.isPending ? page.verify.creating : page.verify.publishPassing}
                </Button>
                <Button
                  variant="secondary"
                  onClick={keepAsDraft}
                  disabled={!canSubmit(snapshot) || create.isPending}
                >
                  {page.verify.keepDraft}
                </Button>
              </div>
              <p className="text-[13px] leading-5 text-ink-3">{page.verify.publishHint}</p>
              <p className="text-[13px] leading-5 text-ink-3">{page.verify.draftHint}</p>
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
            {values.models.length > 0 ? page.models.selected(values.models.length) : null}
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
            {step < 4 ? (
              <Button
                onClick={handleNext}
                disabled={!canLeaveStep(step, snapshot) || probingKeys}
              >
                {page.actions.next}
              </Button>
            ) : null}
          </div>
        </footer>
      </form>
    </>
  )
}
