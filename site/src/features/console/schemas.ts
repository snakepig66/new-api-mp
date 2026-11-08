/**
 * Form validation for the console's key wizard, key detail and wallet.
 * Schemas live here so the same rules can be unit tested without a DOM.
 */

import { z } from 'zod'

import { copy } from '@/copy'

import {
  milliToMultiplierInput,
  parseMultiplierToMilli,
  parseSellerIds,
  parseYuanToMicro,
} from './format'
import type { KeyPrefInput, Strategy } from './types'
import { STRATEGIES } from './types'

const wizardCopy = copy.pages.consoleKeys.wizard

/** Key name: trimmed, 1–50 characters. */
export const keyNameSchema = z
  .string()
  .trim()
  .min(1, wizardCopy.errors.nameRequired)
  .max(50, wizardCopy.errors.nameTooLong)

/** Spend cap as typed: blank means no cap, otherwise a non-negative yuan amount. */
export const spendCapSchema = z
  .string()
  .refine(
    (value) => !value.trim() || parseYuanToMicro(value) !== null,
    wizardCopy.errors.spendInvalid,
  )

/** Per-model multiplier cap as typed: blank or 0 means no cap. */
export const multiplierCapSchema = z
  .string()
  .refine(
    (value) => parseMultiplierToMilli(value) !== null,
    wizardCopy.errors.capInvalid,
  )

export interface ModelSelectionEntry {
  model: string
  cap: string
}

export type ModelSelectionResult =
  | { ok: true; models: { model: string; price_cap_milli: number }[] }
  | { ok: false; error: string; model?: string }

/**
 * Validate a wizard model selection (model id -> typed cap) and convert the
 * caps to milli. At least one model is required.
 */
export function buildModelSelection(selection: Record<string, string>): ModelSelectionResult {
  const entries = Object.entries(selection)
  if (entries.length === 0) {
    return { ok: false, error: wizardCopy.errors.modelsRequired }
  }
  const models: { model: string; price_cap_milli: number }[] = []
  for (const [model, cap] of entries) {
    const milli = parseMultiplierToMilli(cap)
    if (milli === null) {
      return { ok: false, error: wizardCopy.errors.capInvalid, model }
    }
    models.push({ model, price_cap_milli: milli })
  }
  return { ok: true, models }
}

/** Full wizard payload after the four steps have been validated. */
export const keyWizardSchema = z.object({
  name: keyNameSchema,
  models: z.array(
    z.object({
      model: z.string().min(1),
      price_cap_milli: z.number().int().min(0).max(1000),
    }),
  ).min(1, wizardCopy.errors.modelsRequired),
  strategy: z.enum(['cheapest', 'balanced', 'stable']),
  spendCap: spendCapSchema,
})

export type KeyWizardValues = z.infer<typeof keyWizardSchema>

export interface PrefFormValues {
  strategy: Strategy
  modelScope: 'all' | 'selected'
  priceCap: string
  rpm: string
  prefer: string
  block: string
  only: string
  allowFallback: boolean
  officialOnly: boolean
}

const strategySchema = z.enum(['cheapest', 'balanced', 'stable'])

/**
 * Validate the key-detail preference form. Seller id fields are raw text;
 * invalid tokens, a multi-value "only seller" and the only/block conflict are
 * all rejected with path-scoped messages.
 */
export const prefFormSchema = z
  .object({
    strategy: strategySchema,
    modelScope: z.enum(['all', 'selected']),
    priceCap: z.string(),
    rpm: z.string(),
    prefer: z.string(),
    block: z.string(),
    only: z.string(),
    allowFallback: z.boolean(),
    officialOnly: z.boolean(),
  })
  .superRefine((value, ctx) => {
    if (value.priceCap.trim() && parseMultiplierToMilli(value.priceCap) === null) {
      ctx.addIssue({
        code: 'custom',
        path: ['priceCap'],
        message: copy.pages.consoleKeyDetail.errors.priceCapInvalid,
      })
    }
    if (value.rpm.trim() && !/^\d{1,7}$/.test(value.rpm.trim())) {
      ctx.addIssue({
        code: 'custom',
        path: ['rpm'],
        message: copy.pages.consoleKeyDetail.errors.rpmInvalid,
      })
    }
    const prefer = parseSellerIds(value.prefer)
    if (prefer.invalid.length > 0) {
      ctx.addIssue({
        code: 'custom',
        path: ['prefer'],
        message: copy.pages.consoleKeyDetail.errors.sellerInvalid,
      })
    }
    const block = parseSellerIds(value.block)
    if (block.invalid.length > 0) {
      ctx.addIssue({
        code: 'custom',
        path: ['block'],
        message: copy.pages.consoleKeyDetail.errors.sellerInvalid,
      })
    }
    const only = parseSellerIds(value.only)
    if (only.invalid.length > 0 || only.ids.length > 1) {
      ctx.addIssue({
        code: 'custom',
        path: ['only'],
        message: copy.pages.consoleKeyDetail.errors.onlyInvalid,
      })
    }
    if (only.ids.length === 1 && (prefer.ids.length > 0 || block.ids.length > 0)) {
      ctx.addIssue({
        code: 'custom',
        path: ['only'],
        message: copy.pages.consoleKeyDetail.errors.onlyConflict,
      })
    }
  })

/** Convert the validated form to the PUT /key/pref body. */
export function prefFormToInput(tokenId: number, values: PrefFormValues): KeyPrefInput {
  const only = parseSellerIds(values.only)
  return {
    token_id: tokenId,
    strategy: values.strategy,
    model_scope: values.modelScope,
    price_cap_milli: parseMultiplierToMilli(values.priceCap) ?? 0,
    prefer_seller_ids: parseSellerIds(values.prefer).ids,
    block_seller_ids: parseSellerIds(values.block).ids,
    only_seller_id: only.ids[0] ?? 0,
    allow_fallback: values.allowFallback,
    rpm: values.rpm.trim() ? Number(values.rpm.trim()) : 0,
    official_only: values.officialOnly,
  }
}

/** Initial form values from an API preference row (or defaults). */
export function prefToFormValues(
  pref:
    | {
        strategy: Strategy
        model_scope?: 'all' | 'selected'
        price_cap_milli: number
        prefer_seller_ids: number[]
        block_seller_ids: number[]
        only_seller_id: number
        allow_fallback: boolean
        rpm: number
        official_only?: boolean
      }
    | null
    | undefined,
): PrefFormValues {
  return {
    strategy: pref && STRATEGIES.includes(pref.strategy) ? pref.strategy : 'balanced',
    modelScope: pref?.model_scope === 'all' ? 'all' : 'selected',
    priceCap: pref ? milliToMultiplierInput(pref.price_cap_milli) : '',
    rpm: pref && pref.rpm > 0 ? String(pref.rpm) : '',
    prefer: pref ? pref.prefer_seller_ids.join(', ') : '',
    block: pref ? pref.block_seller_ids.join(', ') : '',
    only: pref && pref.only_seller_id > 0 ? String(pref.only_seller_id) : '',
    allowFallback: pref?.allow_fallback ?? false,
    officialOnly: pref?.official_only ?? false,
  }
}

/** Redemption code: trimmed, non-empty, max 128 characters. */
export const redeemCodeSchema = z
  .string()
  .trim()
  .min(1, copy.pages.consoleWallet.redeem.required)
  .max(128, copy.pages.consoleWallet.redeem.tooLong)
