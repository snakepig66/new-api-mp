/**
 * 挂单向导的步骤状态机（纯函数，方便单测）。
 *
 * 四步：厂商 → Key → 检测并勾选模型 → 倍率与限额。页面用 react-hook-form
 * 保存字段，这里只决定「能否离开当前步 / 能否跳到某一步 / 能否提交」。
 */

import type { ProbeResult } from './api'
import { parseYuanToMicro } from './money-input'

export const WIZARD_STEP_COUNT = 4
export const WIZARD_LAST_STEP = 3

export type WizardStep = 0 | 1 | 2 | 3

export interface WizardSnapshot {
  vendorId: string
  apiKey: string
  probe: ProbeResult | null
  selectedModels: string[]
  multiplierMilli: number
  concurrency: number
  rpm: number
  dailyCapYuan: string
  hoursValid: boolean
  /** True when the selected channel kind is `plan`. */
  requiresPlanTerms?: boolean
  /** The seller ticked 「我确认该套餐条款允许共享」. */
  planTermsAccepted?: boolean
}

export const EMPTY_WIZARD_SNAPSHOT: WizardSnapshot = {
  vendorId: '',
  apiKey: '',
  probe: null,
  selectedModels: [],
  multiplierMilli: 500,
  concurrency: 5,
  rpm: 0,
  dailyCapYuan: '0',
  hoursValid: true,
}

function validKey(key: string): boolean {
  return key.trim().length >= 8 && !/\s/.test(key.trim())
}

function validMultiplier(milli: number): boolean {
  return Number.isInteger(milli) && milli >= 1 && milli <= 1000
}

function validModels(snapshot: WizardSnapshot): boolean {
  if (!snapshot.probe?.ok || snapshot.selectedModels.length === 0) return false
  return snapshot.selectedModels.every((model) => snapshot.probe?.models.includes(model))
}

export function canLeaveStep(step: WizardStep, snapshot: WizardSnapshot): boolean {
  switch (step) {
    case 0:
      return snapshot.vendorId.trim().length > 0
    case 1:
      return validKey(snapshot.apiKey)
    case 2:
      return validModels(snapshot)
    case 3:
      return canSubmit(snapshot)
  }
}

export function canSubmit(snapshot: WizardSnapshot): boolean {
  const dailyCap = parseYuanToMicro(snapshot.dailyCapYuan)
  return (
    validMultiplier(snapshot.multiplierMilli) &&
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
