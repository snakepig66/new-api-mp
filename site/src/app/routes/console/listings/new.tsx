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
  ModelName,
  PageHeader,
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
  probeListing,
  sellerChannelKind,
  sellerCommonAddressesQuery,
  sellerGroupsQuery,
  sellerListingsQuery,
  sellerVendorsQuery,
  type ProbeResult,
  type SellerQuotaWindow,
  type SellerVendor,
  type SellerCommonAddress,
  type CreateListingInput,
} from '@/features/seller/api'
import type {
  PoolModelOption,
  SellerModelConfig,
  UpstreamModelOption,
} from '@/features/seller/api'
import { ChannelModelsEditor } from '@/features/seller/ChannelModelsEditor'
import { Field, notify, Skeleton } from '@/features/seller/components'
import {
  duplicateKeyIssue,
  sellerErrorMessage,
  type DuplicateKeyIssue,
} from '@/features/seller/errors'
import { parseYuanToMicro } from '@/features/seller/money-input'
import { MultiplierSlider } from '@/features/seller/MultiplierSlider'
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
  firstStart: z.string(),
  firstEnd: z.string(),
  secondStart: z.string(),
  secondEnd: z.string(),
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
        ['firstStart', 'firstEnd'],
        ['secondStart', 'secondEnd'],
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
  firstStart: '09:00',
  firstEnd: '12:00',
  secondStart: '12:00',
  secondEnd: '18:00',
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
      [group.firstStart, group.firstEnd],
      [group.secondStart, group.secondEnd],
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

function StepRail({
  step,
  onJump,
}: {
  step: number
  onJump: (next: WizardStep) => void
}) {
  return (
    <ol className="mt-6 grid grid-cols-1 gap-3 glass p-4 sm:grid-cols-3">
      {page.steps.map((title, index) => {
        const completed = index < step
        const active = index === step
        const disabled = index > step
        return (
          <li
            key={title}
            aria-current={active ? 'step' : undefined}
            className={cn(
              'rounded-md border p-3.5',
              active ? 'border-brand bg-brand-soft/50' : 'border-white/70 bg-white/45 backdrop-blur-md',
            )}
          >
            <button
              type="button"
              disabled={disabled}
              onClick={() => onJump(index as WizardStep)}
              className={cn(
                'flex w-full items-center gap-2 text-left',
                disabled ? 'cursor-not-allowed' : 'cursor-pointer',
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  'grid h-6 w-6 shrink-0 place-items-center rounded-full border text-meta font-medium',
                  completed
                    ? 'border-transparent bg-brand-soft text-brand-ink'
                    : active
                      ? 'border-transparent bg-brand text-white'
                      : 'glass-inset text-ink-3',
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
                  'text-body font-medium',
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
    return <span className="text-meta leading-5 text-danger">{error}</span>
  }
  if (!trust) {
    return <span className="text-meta text-ink-3">{page.trust.idle}</span>
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
      <h4 className="text-meta font-medium text-ink-3">{page.url.commonTitle}</h4>
      <p className="mt-1 text-meta text-ink-3">{page.url.commonHint}</p>
      {pending ? (
        <div className="mt-2 flex flex-wrap gap-2" aria-busy="true">
          {Array.from({ length: 4 }, (_, index) => (
            <Skeleton key={index} className="h-9 w-44" />
          ))}
        </div>
      ) : error ? (
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <span className="text-meta text-danger">{page.url.commonError}</span>
          <Button variant="secondary" size="sm" onClick={onRetry}>
            {page.actions.retry}
          </Button>
        </div>
      ) : addresses.length === 0 ? (
        <p className="mt-2 text-meta text-ink-3">{page.url.commonEmpty}</p>
      ) : (
        <div className="mt-2 flex flex-wrap gap-2">
          {addresses.map((address) => (
            <button
              key={address.host}
              type="button"
              onClick={() => onPick(address)}
              className="flex items-center gap-2 glass px-3 py-2 text-left text-meta transition-colors hover:border-brand/40"
            >
              <span className="font-medium text-ink">{address.vendor_name || address.host}</span>
              <span className="font-mono text-caption text-ink-3">{address.host}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
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
    <div className="glass-inset p-4">
      <label className="flex items-center gap-2 text-meta font-medium text-ink">
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
            ['first', 'firstStart', 'firstEnd'],
            ['second', 'secondStart', 'secondEnd'],
          ] as const
        ).map(([slot, startKey, endKey]) => {
          const slotLabel = slot === 'first' ? page.limits.first : page.limits.second
          return (
            <div key={slot}>
              <span className="block text-meta text-ink-3">{slotLabel}</span>
              <div className="mt-1 flex items-center gap-2">
                <Input
                  type="time"
                  aria-label={`${label}${slotLabel}${page.limits.start}`}
                  {...register(`availability.${day}.${startKey}` as const)}
                />
                <span className="text-ink-3" aria-hidden="true">
                  –
                </span>
                <Input
                  type="time"
                  aria-label={`${label}${slotLabel}${page.limits.end}`}
                  {...register(`availability.${day}.${endKey}` as const)}
                />
              </div>
            </div>
          )
        })}
      </div>
      {error ? (
        <p className="mt-2 text-meta text-danger" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}

function simplifyProbeError(reason?: string): string {
  if (!reason) return '检测未通过'
  const lower = reason.toLowerCase()
  if (
    lower.includes('401') ||
    lower.includes('403') ||
    lower.includes('unauthorized') ||
    lower.includes('forbidden') ||
    lower.includes('key 无效') ||
    lower.includes('无权')
  ) {
    return 'Key 无效或没权限'
  }
  if (
    lower.includes('402') ||
    lower.includes('balance') ||
    lower.includes('quota') ||
    lower.includes('insufficient') ||
    lower.includes('余额') ||
    lower.includes('欠费')
  ) {
    return '上游余额不足'
  }
  if (
    lower.includes('timeout') ||
    lower.includes('timed out') ||
    lower.includes('超时') ||
    lower.includes('connect') ||
    lower.includes('connection') ||
    lower.includes('dns') ||
    lower.includes('no such host') ||
    lower.includes('refused') ||
    lower.includes('unreachable') ||
    lower.includes('连不上')
  ) {
    return '地址连不上'
  }
  return reason
}

export function NewListingPage() {
  const toast = useToast()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [step, setStep] = useState<WizardStep>(0)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [multipliers, setMultipliers] = useState<Record<string, number>>({})
  const [upstreamAliases, setUpstreamAliases] = useState<Record<string, string>>({})
  // P4: per-key weight (weighted_round_robin) keyed by the raw key text.
  const [weights, setWeights] = useState<Record<string, number>>({})
  const [batchMultiplier, setBatchMultiplier] = useState('0.8')
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

  const probeInput = (key: string, modelsToProbe?: string[]) => ({
    vendor_id: matchedVendor?.id ?? '',
    api_key: key,
    models: modelsToProbe ?? values.models ?? [],
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
  // ShareLLM-style catalogue: every upstream name with its suggested pool
  // match, plus the full priced pool the seller may pick from directly.
  const upstreamOptions = useMemo(() => {
    const seen = new Map<string, UpstreamModelOption>()
    const sources = [recognition, ...Object.values(keyProbes)]
    for (const probe of sources) {
      for (const row of probe?.upstream_models ?? []) {
        if (!seen.has(row.upstream)) seen.set(row.upstream, row)
      }
    }
    return Array.from(seen.values())
  }, [recognition, keyProbes])
  const poolOptions = useMemo(() => {
    const sources = [recognition, ...Object.values(keyProbes)]
    for (const probe of sources) {
      if (probe?.pool_models && probe.pool_models.length > 0) return probe.pool_models
    }
    return [] as PoolModelOption[]
  }, [recognition, keyProbes])
  const poolIds = useMemo(() => new Set(poolOptions.map((p) => p.model)), [poolOptions])
  const suggestedAlias = useMemo(() => {
    const out: Record<string, string> = {}
    for (const row of upstreamOptions) {
      if (row.suggested_model && !out[row.suggested_model]) out[row.suggested_model] = row.upstream
    }
    return out
  }, [upstreamOptions])
  const aliasFor = (model: string) => upstreamAliases[model] ?? suggestedAlias[model] ?? model
  const suggestedModels = useMemo(() => {
    const union = new Set<string>()
    Object.values(keyProbes).forEach((probe) => {
      for (const model of probe.verified_models ?? []) union.add(model)
    })
    if (recognition?.verified_models) {
      for (const model of recognition.verified_models) union.add(model)
    }
    return Array.from(union).sort()
  }, [keyProbes, recognition])
  // Pool picks the upstream matcher did not suggest still count as available.
  const availableModels = useMemo(() => {
    const union = new Set(suggestedModels)
    for (const model of values.models ?? []) if (poolIds.has(model)) union.add(model)
    return Array.from(union).sort()
  }, [suggestedModels, values.models, poolIds])

  // Only preselect models that have been detected as available (verified_models).
  useEffect(() => {
    if (suggestedModels.length > 0) {
      const current = form.getValues('models') ?? []
      if (current.length === 0) {
        form.setValue('models', suggestedModels, { shouldValidate: true })
        setMultipliers((previous) => {
          const updated = { ...previous }
          for (const model of suggestedModels) {
            if (!validModelMultiplier(updated[model])) {
              updated[model] = DEFAULT_MULTIPLIER_MILLI
            }
          }
          return updated
        })
      }
    }
  }, [suggestedModels, form])

  // Automatically probe all keys when a valid URL and API keys are entered.
  useEffect(() => {
    if (!validCustomURL(values.url) || !validApiKeys(keys)) return
    const timer = setTimeout(() => {
      void probeAllKeys()
    }, PROBE_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [values.url, values.api_keys])

  const flatHours = flattenHours(values.availability)
  const headersValid = trust?.trust === 'unlisted' ? validRequestHeaders(values.headers) : true
  const hasPassedKey = keys.some((k) => keyProbes[k]?.key_valid)
  const snapshot: WizardSnapshot = {
    url: values.url,
    vendorId: matchedVendor?.id ?? (isCustomAddress ? CUSTOM_VENDOR_ID : ''),
    customBaseURL: matchedVendor ? undefined : values.url.trim(),
    apiKeys: keys,
    hasPassedKey,
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

  async function probeOneKey(key: string, force: boolean, modelsToProbe?: string[]): Promise<void> {
    if (!force && keyProbes[key]) return
    try {
      const result = await probeListing(probeInput(key, modelsToProbe))
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

  async function probeAllKeys(force = false, modelsToProbe?: string[]): Promise<void> {
    if (probingKeys || !validApiKeys(keys) || !validCustomURL(values.url)) return
    setProbingKeys(true)
    try {
      for (const key of keys) {
        await probeOneKey(key, force, modelsToProbe)
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
      void probeAllKeys(true)
      return
    }
    if (step === 1) {
      void form.trigger(['models', 'concurrency', 'rpm', 'daily_cap_yuan', 'headers'])
      setStep(2)
      void probeAllKeys(true, values.models)
      return
    }
  }

  function handleJump(next: WizardStep) {
    if (next <= step) {
      setStep(next)
    } else if (next === 1 && canLeaveStep(0, snapshot)) {
      setStep(1)
    } else if (next === 2 && canLeaveStep(0, snapshot) && canLeaveStep(1, snapshot)) {
      setStep(2)
      void probeAllKeys(true, values.models)
    }
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
      const callable = new Set(probe ? (probe.verified_models ?? probe.models ?? []) : [])
      const models =
        mode === 'passing'
          ? data.models.filter(
              (model) => probe?.key_valid && (callable.has(model) || poolIds.has(model)),
            )
          : [...data.models]
      if (models.length === 0) continue
      const weight = Math.min(10000, Math.max(1, Math.round(weights[key] ?? 100)))
      const model_configs = models.map((model) => ({
        model,
        upstream_model: aliasFor(model),
        multiplier_milli: multipliers[model] ?? DEFAULT_MULTIPLIER_MILLI,
      }))
      payloads.push({ ...base, api_key: key, models, weight, model_configs })
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
    const callable = new Set(probe.verified_models ?? probe.models ?? [])
    return values.models.some((model) => callable.has(model) || poolIds.has(model))
  })
  const stepRequired = useMemo(() => {
    if (step === 0) {
      const filled = (validCustomURL(values.url) ? 1 : 0) + (hasPassedKey ? 1 : 0)
      return { filled, total: 2, label: page.requiredCount(filled, 2) }
    }
    if (step === 1) {
      const filled = values.models.length > 0 ? 1 : 0
      return { filled, total: 1, label: page.requiredCount(filled, 1) }
    }
    return { filled: 1, total: 1, label: '全部就绪' }
  }, [step, values.url, hasPassedKey, values.models])

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <span title={page.requiredHint}>
            <Tag tone={stepRequired.filled === stepRequired.total ? 'brand' : 'neutral'} dot>
              {stepRequired.label}
            </Tag>
          </span>
        }
      />
      <StepRail
        step={step}
        onJump={handleJump}
      />

      <form
        className="mt-6 glass p-6"
        onSubmit={(event) => event.preventDefault()}
      >
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-4">
          <div>
            <h2 className="text-title font-semibold tracking-tight text-ink">
              {page.steps[step]}
            </h2>
            <p className="mt-1 text-meta leading-5 text-ink-3">{page.stepLead[step]}</p>
          </div>
          <span className="tabular-nums text-meta text-ink-3">0{step + 1} / 03</span>
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
                    className="w-full rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-4 py-3 font-mono text-body text-ink transition-colors placeholder:text-ink-3 focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20 aria-[invalid=true]:border-danger"
                    {...form.register('api_keys')}
                  />
                </Field>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-meta text-ink-3">
                    {keys.length > 0 ? page.key.count(keys.length) : page.key.empty}
                  </span>
                  <TrustBadge
                    trust={trust}
                    recognizing={recognizing}
                    error={recognizeError}
                  />
                </div>
                <div className="mt-3 space-y-2">
                  <div className="flex items-center justify-between text-meta text-ink-3">
                    <span>Key 连通性测试</span>
                    {keys.length > 0 ? (
                      <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        disabled={probingKeys || !validCustomURL(values.url)}
                        loading={probingKeys}
                        onClick={() => void probeAllKeys(true)}
                      >
                        检测全部 Key
                      </Button>
                    ) : null}
                  </div>
                  {keys.length > 0 ? (
                    <div className="divide-y divide-line/60 rounded-md border border-line bg-white/50">
                      {keys.map((k) => {
                        const probe = keyProbes[k]
                        const isOk = probe?.key_valid
                        const isProbing = probingKeys && !probe
                        return (
                          <div key={k} className="flex items-center justify-between px-3 py-2 text-meta">
                            <span className="font-mono text-ink">{maskApiKey(k)}</span>
                            <div className="flex items-center gap-2">
                              {isProbing ? (
                                <span className="text-caption text-ink-3">测试中…</span>
                              ) : isOk ? (
                                <span className="flex items-center gap-1 font-medium text-emerald-600">
                                  <span aria-hidden="true">✓</span>
                                  <span>连通正常</span>
                                  {probe.latency_ms > 0 ? (
                                    <span className="text-caption text-ink-3">({probe.latency_ms}ms)</span>
                                  ) : null}
                                </span>
                              ) : probe ? (
                                <span className="flex items-center gap-1 font-medium text-rose-600">
                                  <span aria-hidden="true">✕</span>
                                  <span>{simplifyProbeError(probe.fail_reason)}</span>
                                </span>
                              ) : (
                                <span className="text-caption text-ink-3">等待检测</span>
                              )}
                              <button
                                type="button"
                                className="text-caption text-brand hover:underline disabled:opacity-50"
                                disabled={probingKeys || !validCustomURL(values.url)}
                                onClick={() => void probeOneKey(k, true)}
                              >
                                重测
                              </button>
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  ) : (
                    <p className="text-caption text-ink-3">粘贴 Key 后将自动向上游发送 max_tokens=1 探活请求测试连通性。</p>
                  )}
                  {!hasPassedKey && keys.length > 0 && !probingKeys ? (
                    <p className="text-caption text-warn">至少一个 Key 通过连通性测试才能进入下一步。</p>
                  ) : null}
                </div>
                {trust?.trust === 'blocked' ? (
                  <p className="text-meta leading-5 text-danger" role="alert">
                    {page.trust.blocked}
                  </p>
                ) : trust?.trust === 'watch' || trust?.trust === 'unlisted' ? (
                  <p className="text-meta leading-5 text-warn" role="status">
                    {trust.trust === 'watch' ? page.trust.watch : page.trust.unlisted}
                  </p>
                ) : null}

                {existingGroup ? (
                  <div className="glass-inset px-4 py-3">
                    <h3 className="text-meta font-semibold text-ink">
                      {page.group.existingTitle}
                    </h3>
                    <p className="mt-1 text-meta leading-5 text-ink-3">
                      {page.group.existingHint(
                        existingGroup.key_count,
                        existingGroup.key_strategy,
                      )}
                    </p>
                    <div className="mt-2 flex flex-wrap gap-4">
                      <label className="flex items-center gap-2 text-meta text-ink">
                        <input type="radio" checked readOnly className="h-4 w-4 accent-brand" />
                        {page.group.join}
                      </label>
                      <label className="flex items-center gap-2 text-meta text-ink-3">
                        <input
                          type="radio"
                          disabled
                          aria-label={page.group.createNew}
                          className="h-4 w-4"
                        />
                        {page.group.createNew}
                      </label>
                    </div>
                    <p className="mt-1 text-caption leading-5 text-ink-3">
                      {page.group.createNewHint}
                    </p>
                  </div>
                ) : null}

                {keys.length > 1 ? (
                  <div>
                    <h3 className="text-meta font-semibold text-ink">
                      {page.group.keyOrderTitle}
                    </h3>
                    <p className="mt-1 text-meta leading-5 text-ink-3">
                      {values.key_strategy === 'primary_backup'
                        ? page.group.orderHint
                        : page.group.weightHint}
                    </p>
                    <ul className="mt-2 space-y-2">
                      {keys.map((key, index) => (
                        <li
                          key={`${index}-${key.slice(-6)}`}
                          className="flex flex-wrap items-center justify-between gap-3 glass-inset px-4 py-2"
                        >
                          <span className="font-mono text-caption text-ink-3">
                            {maskApiKey(key)}
                          </span>
                          {values.key_strategy === 'primary_backup' ? (
                            <Tag>{page.group.order(index + 1)}</Tag>
                          ) : (
                            <label className="flex items-center gap-2 text-meta text-ink-3">
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
            <div className="space-y-6">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h3 className="text-body font-semibold text-ink">{page.models.title}</h3>
                  <p className="mt-1 text-meta text-ink-3">{page.models.lead}</p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-meta text-ink-3">{page.models.batchLabel}</span>
                  <MultiplierSlider
                    aria-label={page.models.batchLabel}
                    value={batchMultiplier}
                    onChange={(_val, text) => {
                      setBatchMultiplier(text)
                      setBatchApplied(false)
                    }}
                  />
                  <Button variant="secondary" size="sm" onClick={applyBatchMultiplier}>
                    {page.models.applyAll}
                  </Button>
                  {batchApplied ? (
                    <span className="text-meta text-brand-ink" role="status">
                      {page.models.applied}
                    </span>
                  ) : null}
                </div>
              </div>

              {values.models.length === 0 ? (
                <div className="rounded-md border border-brand/20 bg-brand-soft/40 p-3 text-meta text-ink-2">
                  从模型池里勾选你的 Key 能用的模型
                </div>
              ) : null}

              {probingKeys ? (
                <div className="flex items-center gap-2" aria-busy="true" aria-live="polite">
                  <Skeleton className="h-4 w-4 rounded-full" />
                  <span className="text-meta text-ink-3">{page.models.loading}</span>
                </div>
              ) : !probed || (upstreamOptions.length === 0 && poolOptions.length === 0) ? (
                <p className="text-meta leading-5 text-warn" role="status">
                  {page.models.noModels}
                </p>
              ) : (
                <ChannelModelsEditor
                  upstream={upstreamOptions}
                  pool={poolOptions}
                  coefficients={recognition?.coefficients}
                  onFetchUpstream={() => void probeAllKeys(true)}
                  fetching={probingKeys}
                  value={values.models.map(
                    (model): SellerModelConfig => ({
                      model,
                      upstream_model: aliasFor(model),
                      multiplier_milli: multipliers[model] ?? DEFAULT_MULTIPLIER_MILLI,
                    }),
                  )}
                  onChange={(next) => {
                    form.setValue(
                      'models',
                      next.map((c) => c.model),
                      { shouldValidate: true, shouldDirty: true },
                    )
                    setMultipliers((current) => {
                      const updated = { ...current }
                      for (const c of next) updated[c.model] = c.multiplier_milli
                      return updated
                    })
                    setUpstreamAliases((current) => {
                      const updated = { ...current }
                      for (const c of next) updated[c.model] = c.upstream_model
                      return updated
                    })
                  }}
                />
              )}
              {Object.keys(keyProbes).length < keys.length && !probingKeys ? (
                <p className="text-meta text-ink-3" role="status">
                  {page.models.partial}
                </p>
              ) : null}
              <div className="flex flex-wrap items-center justify-end gap-2">
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
                <p className="text-meta leading-5 text-warn" role="status">
                  {page.models.mixedMultiplier}
                </p>
              ) : null}
              {form.formState.errors.models?.message ? (
                <p className="text-meta text-danger" role="alert">
                  {form.formState.errors.models.message}
                </p>
              ) : null}

              {/* Collapsed Advanced Section */}
              <details
                className="group border-t border-line pt-4"
                open={advancedOpen}
                onToggle={(e) => setAdvancedOpen((e.target as HTMLDetailsElement).open)}
              >
                <summary className="flex cursor-pointer select-none items-center justify-between text-meta font-medium text-ink hover:text-brand-ink">
                  <span>高级选填（并发限制、限额、时段与请求头等）</span>
                  <span className="text-caption text-ink-3 transition-transform group-open:rotate-180">▼</span>
                </summary>
                <div className="mt-4 space-y-6">
                  <div>
                    <h4 className="text-meta font-medium text-ink">{page.limits.title}</h4>
                    <p className="mt-0.5 text-meta text-ink-3">{page.limits.lead}</p>
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
                    <h4 className="text-meta font-medium text-ink">{page.limits.hoursTitle}</h4>
                    <p className="mt-1 text-meta text-ink-3">{page.limits.hoursLead}</p>
                    <div className="mt-3 grid gap-3">
                      {DAY_KEYS.map((day) => (
                        <AvailabilityGroup
                          key={day}
                          day={day}
                          label={page.limits.dayTypes[day]}
                          register={form.register}
                          error={
                            form.formState.errors.availability?.[day]?.secondEnd?.message ??
                            form.formState.errors.availability?.[day]?.firstEnd?.message
                          }
                        />
                      ))}
                    </div>
                    {!flatHours.any ? (
                      <p className="mt-2 text-meta text-ink-3">{page.limits.noHours}</p>
                    ) : null}
                  </div>

                  {requiresPlanTerms ? (
                    <div className="rounded-md border border-brand/25 bg-brand-soft/40 px-4 py-3">
                      <h4 className="text-meta font-medium text-ink">{page.plan.title}</h4>
                      <p className="mt-1 text-meta leading-5 text-ink-2">{page.plan.windows}</p>
                      {quotaWindowText ? (
                        <p className="mt-1 text-meta leading-5 text-ink-2">{quotaWindowText}</p>
                      ) : null}
                      {matchedVendor?.terms_url ? (
                        <a
                          href={matchedVendor.terms_url}
                          target="_blank"
                          rel="noreferrer"
                          className="mt-1 inline-block text-meta text-brand-ink underline"
                        >
                          {page.plan.termsLink}
                        </a>
                      ) : null}
                      <label className="mt-3 flex items-start gap-2 text-meta text-ink">
                        <input
                          type="checkbox"
                          className="mt-0.5 h-4 w-4 shrink-0 accent-brand"
                          {...form.register('plan_terms_accepted')}
                        />
                        <span>{page.plan.terms}</span>
                      </label>
                      <p className="mt-1 pl-6 text-meta leading-5 text-ink-3">
                        {page.plan.termsHint}
                      </p>
                      {!values.plan_terms_accepted ? (
                        <p className="mt-1 pl-6 text-meta text-warn" role="status">
                          {page.plan.required}
                        </p>
                      ) : null}
                    </div>
                  ) : null}

                  <fieldset className="border-t border-line pt-4">
                    <legend className="text-meta font-medium text-ink">
                      {page.advanced.keyPolicy}
                    </legend>
                    <p className="mt-1 text-meta text-ink-3">{page.advanced.keyPolicyHint}</p>
                    <div className="mt-2 flex flex-wrap gap-4">
                      <label className="flex items-center gap-2 text-meta text-ink">
                        <input
                          type="radio"
                          value="weighted"
                          className="h-4 w-4 accent-brand"
                          {...form.register('key_strategy')}
                        />
                        {page.advanced.weighted}
                      </label>
                      <label className="flex items-center gap-2 text-meta text-ink">
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
                    <label className="flex items-center gap-2 text-meta font-medium text-ink">
                      <input
                        type="checkbox"
                        className="h-4 w-4 accent-brand"
                        {...form.register('affinity')}
                      />
                      {page.advanced.affinity}
                    </label>
                    <p className="mt-1 pl-6 text-meta text-ink-3">
                      {page.advanced.affinityHint}
                    </p>
                  </div>

                  <div className="border-t border-line pt-4">
                    <h4 className="text-meta font-medium text-ink">{page.advanced.headers}</h4>
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
                          className="w-full rounded-md border border-white/70 bg-white/55 backdrop-blur-md px-4 py-3 font-mono text-body text-ink transition-colors placeholder:text-ink-3 focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20 aria-[invalid=true]:border-danger"
                          {...form.register('headers')}
                        />
                      </Field>
                    ) : (
                      <p className="mt-2 text-meta text-ink-3">{page.advanced.headersLocked}</p>
                    )}
                  </div>

                  <div className="border-t border-line pt-4">
                    <h4 className="text-meta font-medium text-ink">
                      {page.advanced.endpoints}
                    </h4>
                    <p className="mt-1 text-meta text-ink-3">
                      {page.advanced.endpointsHint}
                    </p>
                    <div className="mt-2 flex flex-wrap gap-4">
                      {(['openai_chat', 'openai_responses', 'anthropic_messages'] as const).map(
                        (endpoint) => (
                          <label
                            key={endpoint}
                            className="flex items-center gap-2 text-meta text-ink"
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
              </details>
            </div>
          ) : null}

          {step === 2 ? (
            <div className="space-y-5">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h3 className="text-body font-semibold text-ink">{page.verify.title}</h3>
                  <p className="mt-1 text-meta text-ink-3">{page.verify.lead}</p>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-meta tabular-nums text-ink-3">
                    {page.verify.passed(passedCount, verification.length)}
                  </span>
                  <Button
                    variant="secondary"
                    size="sm"
                    loading={probingKeys}
                    onClick={() => void probeAllKeys(true, values.models)}
                  >
                    {probingKeys ? page.verify.rechecking : page.verify.recheck}
                  </Button>
                </div>
              </div>

              {keys.length === 0 || values.models.length === 0 ? (
                <p className="text-meta text-ink-3">{page.verify.noSelection}</p>
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
                        <TableCell className="font-mono text-meta">
                          {maskApiKey(cell.key)}
                        </TableCell>
                        <TableCell className="font-mono text-meta"><ModelName model={cell.model} vendor={matchedVendor?.id} size="sm" /></TableCell>
                        <TableCell>{endpointLabel(cell.endpoint)}</TableCell>
                        <TableCell>
                          <Tag tone={cell.ok ? 'brand' : 'danger'} dot>
                            {cell.ok ? page.verify.pass : page.verify.fail}
                          </Tag>
                        </TableCell>
                        <TableCell className="text-meta text-ink-3">
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
              <p className="text-meta leading-5 text-ink-3">{page.verify.endpointNote}</p>

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
              <p className="text-meta leading-5 text-ink-3">{page.verify.publishHint}</p>
              <p className="text-meta leading-5 text-ink-3">{page.verify.draftHint}</p>
            </div>
          ) : null}

          {duplicate ? (
            duplicate.kind === 'own' ? (
              <div
                className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-warn/30 bg-warn/10 px-4 py-3"
                role="alert"
              >
                <p className="text-meta leading-5 text-warn">
                  {duplicate.listingId
                    ? page.duplicate.own(duplicate.listingId)
                    : copy.pages.sellerCommon.errors.duplicateKeyOwn}
                </p>
                {duplicate.listingId ? (
                  <Link
                    to="/console/listings/$id"
                    params={{ id: String(duplicate.listingId) }}
                    className="text-meta font-medium text-brand-ink underline"
                  >
                    {page.duplicate.ownLink}
                  </Link>
                ) : (
                  <Link
                    to="/console/listings"
                    className="text-meta font-medium text-brand-ink underline"
                  >
                    {page.duplicate.ownLink}
                  </Link>
                )}
              </div>
            ) : (
              <p className="mt-4 text-meta text-danger" role="alert">
                {page.duplicate.other}
              </p>
            )
          ) : submitError ? (
            <p className="mt-4 text-meta text-danger" role="alert">
              {submitError}
            </p>
          ) : null}
        </div>

        <footer className="mt-6 flex items-center justify-between gap-2 border-t border-line pt-4">
          <div className="text-meta text-ink-3">
            {step === 0 && !hasPassedKey && keys.length > 0 ? (
              <span className="text-warn">需至少一个 Key 通过连通性测试</span>
            ) : null}
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
            {step < 2 ? (
              <Button
                variant="primary"
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
