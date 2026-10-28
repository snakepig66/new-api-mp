/**
 * 新建渠道向导的步骤状态机（纯函数，方便单测）。
 *
 * 五步：地址与 Key → 模型 → 限制 → 高级 → 验真。页面用 react-hook-form
 * 保存字段，这里只决定「能否离开当前步 / 能否跳到某一步 / 能否提交」以及
 * 验真矩阵的计算。
 */

import type { ProbeResult, TrustPoolInfo } from './api'
import { parseYuanToMicro } from './money-input'

export const WIZARD_STEP_COUNT = 5
export const WIZARD_LAST_STEP = 4

/** Stable step identifiers for navigation and tests. */
export const WIZARD_STEP_IDS = ['url-key', 'models', 'limits', 'advanced', 'verify'] as const
export type WizardStepId = (typeof WIZARD_STEP_IDS)[number]

/** Sentinel vendor id for a seller-defined custom upstream (custom_channel.go). */
export const CUSTOM_VENDOR_ID = 'custom'

export type WizardStep = 0 | 1 | 2 | 3 | 4

export interface WizardSnapshot {
  /** Absolute https upstream address typed by the seller. */
  url: string
  /** Registered channel slug; CUSTOM_VENDOR_ID when the address is unregistered. */
  vendorId: string
  /** Set only when vendorId is the custom sentinel. */
  customBaseURL?: string
  /** One key per line, already trimmed and de-duplicated. */
  apiKeys: string[]
  /** Trust-pool recognition of the address (null before the first probe). */
  trust: TrustPoolInfo | null
  /** Probe of the first key (the recognition probe). */
  probe: ProbeResult | null
  /** Models at least one probed key can call, or the channel catalogue. */
  availableModels: string[]
  selectedModels: string[]
  /** Millisecond multiplier (100 = 0.1x, 1000 = 1.0x) per selected model. */
  modelMultipliers: Record<string, number>
  concurrency: number
  rpm: number
  dailyCapYuan: string
  hoursValid: boolean
  /** False when an unlisted address carries a malformed request-header line. */
  headersValid?: boolean
  /** True when the selected channel kind is `plan`. */
  requiresPlanTerms?: boolean
  /** The seller ticked 「我确认该套餐条款允许共享」. */
  planTermsAccepted?: boolean
}

export const DEFAULT_MULTIPLIER_MILLI = 500

export const EMPTY_WIZARD_SNAPSHOT: WizardSnapshot = {
  url: '',
  vendorId: '',
  apiKeys: [],
  trust: null,
  probe: null,
  availableModels: [],
  selectedModels: [],
  modelMultipliers: {},
  concurrency: 5,
  rpm: 0,
  dailyCapYuan: '0',
  hoursValid: true,
}

/** Split a multi-line key input into unique, trimmed keys. */
export function parseApiKeys(raw: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of raw.split(/\r?\n/)) {
    const key = line.trim()
    if (!key || seen.has(key)) continue
    seen.add(key)
    out.push(key)
  }
  return out
}

function validKey(key: string): boolean {
  return key.trim().length >= 8 && !/\s/.test(key.trim())
}

/** Every key line must look like a usable credential. */
export function validApiKeys(keys: readonly string[]): boolean {
  return keys.length > 0 && keys.every(validKey)
}

/**
 * A custom upstream must be an absolute https URL with a hostname and no
 * query/fragment/userinfo. The backend re-validates (and the egress guard
 * still judges every dial); this only keeps the wizard's own gate consistent
 * with what the API accepts.
 */
export function validCustomURL(raw: string): boolean {
  const text = raw.trim()
  if (!/^https:\/\/[a-zA-Z0-9][a-zA-Z0-9.-]*(:\d+)?(\/[^\s?#]*)?$/.test(text)) return false
  try {
    const parsed = new URL(text)
    return (
      parsed.protocol === 'https:' &&
      Boolean(parsed.hostname) &&
      !parsed.username &&
      !parsed.password &&
      !parsed.search &&
      !parsed.hash
    )
  } catch {
    return false
  }
}

/** 0.001x – 1.0x (1..1000 milli), the backend listing contract. */
export function validModelMultiplier(milli: number): boolean {
  return Number.isInteger(milli) && milli >= 1 && milli <= 1000
}

function modelsValid(snapshot: WizardSnapshot): boolean {
  if (snapshot.selectedModels.length === 0) return false
  const available = new Set(snapshot.availableModels)
  return snapshot.selectedModels.every(
    (model) =>
      available.has(model) && validModelMultiplier(snapshot.modelMultipliers[model]),
  )
}

function limitsValid(snapshot: WizardSnapshot): boolean {
  const dailyCap = parseYuanToMicro(snapshot.dailyCapYuan)
  return (
    Number.isInteger(snapshot.concurrency) &&
    snapshot.concurrency >= 1 &&
    snapshot.concurrency <= 1000 &&
    Number.isInteger(snapshot.rpm) &&
    snapshot.rpm >= 0 &&
    snapshot.rpm <= 1_000_000 &&
    dailyCap !== null &&
    dailyCap <= 1_000_000_000_000n &&
    snapshot.hoursValid &&
    (!snapshot.requiresPlanTerms || Boolean(snapshot.planTermsAccepted))
  )
}

export function canLeaveStep(step: WizardStep, snapshot: WizardSnapshot): boolean {
  switch (step) {
    case 0:
      return (
        validCustomURL(snapshot.url) &&
        validApiKeys(snapshot.apiKeys) &&
        snapshot.trust?.trust !== 'blocked'
      )
    case 1:
      return modelsValid(snapshot)
    case 2:
      return limitsValid(snapshot)
    case 3:
      return snapshot.headersValid !== false
    case 4:
      return canSubmit(snapshot)
  }
}

export function canSubmit(snapshot: WizardSnapshot): boolean {
  return modelsValid(snapshot) && limitsValid(snapshot) && snapshot.headersValid !== false
}

export function nextWizardStep(current: WizardStep, snapshot: WizardSnapshot): WizardStep {
  if (current >= WIZARD_LAST_STEP || !canLeaveStep(current, snapshot)) return current
  return (current + 1) as WizardStep
}

export function previousWizardStep(current: WizardStep): WizardStep {
  return (current <= 0 ? 0 : current - 1) as WizardStep
}

/** Steps are sequential: a later step can only be reached when all earlier gates pass. */
export function canJumpTo(target: WizardStep, snapshot: WizardSnapshot): boolean {
  for (let step = 0; step < target; step += 1) {
    if (!canLeaveStep(step as WizardStep, snapshot)) return false
  }
  return target >= 0 && target <= WIZARD_LAST_STEP
}

/** Parse the advanced-step request-header textarea (`key: value` or `key=value`). */
export function validRequestHeaders(text: string): boolean {
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (!line) continue
    const separator = line.includes(':') ? ':' : line.includes('=') ? '=' : ''
    if (!separator) return false
    const name = line.slice(0, line.indexOf(separator)).trim()
    const value = line.slice(line.indexOf(separator) + 1).trim()
    if (!name || !value || /\s/.test(name)) return false
  }
  return true
}

export interface VerificationCell {
  key: string
  model: string
  endpoint: string
  ok: boolean
  failReason: string
}

/**
 * Build the Key × model × endpoint verification matrix. The backend probe
 * certifies model callability (not each protocol separately), so every
 * endpoint of a callable model shares the same result; the matrix still lists
 * one row per endpoint so the seller sees what the address claims to serve.
 */
export function buildVerificationMatrix(
  keys: readonly string[],
  models: readonly string[],
  endpoints: readonly string[],
  probes: Readonly<Record<string, ProbeResult | undefined>>,
): VerificationCell[] {
  const protocolEndpoints = endpoints.length > 0 ? endpoints : ['openai_chat']
  const cells: VerificationCell[] = []
  for (const key of keys) {
    const probe = probes[key]
    const callable = new Set(probe ? probe.verified_models ?? probe.models : [])
    for (const model of models) {
      let ok = false
      let failReason = ''
      if (!probe) {
        failReason = '尚未检测'
      } else if (!probe.key_valid) {
        failReason = probe.fail_reason || 'Key 无效'
      } else if (!callable.has(model)) {
        failReason = probe.fail_reason || '该模型不在这个 Key 的可用列表中'
      } else {
        ok = true
      }
      for (const endpoint of protocolEndpoints) {
        cells.push({ key, model, endpoint, ok, failReason })
      }
    }
  }
  return cells
}

/** Mask a key for the verification table (last 4 characters only). */
export function maskApiKey(key: string): string {
  return key.length <= 4 ? '••••' : `••••${key.slice(-4)}`
}
