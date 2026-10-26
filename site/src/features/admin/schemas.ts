/**
 * 运营后台的表单校验与输入解析。
 *
 * 这些函数不依赖 DOM，单测可以直接调用：
 *  - 价目 / 按次价表单（zod）：tier 枚举、元金额、版本、生效时间
 *  - 节假日导入：JSON 数组或逐行 `date,kind`
 *  - 生效时间：datetime-local 字符串按 Asia/Shanghai 解析为 RFC 3339
 */

import { z } from 'zod'

import { copy } from '@/copy'
import { parseYuanToMicro } from '@/features/console/format'

import type {
  AdminHoliday,
  CreatePerCallPriceInput,
  CreatePriceInput,
  HolidayKind,
  PerCallItem,
} from './api'

export const PRICE_TIERS = ['peak', 'offpeak', 'flat'] as const
export const PER_CALL_ITEMS = ['search', 'fetch'] as const
export const HOLIDAY_KINDS = ['holiday', 'workday'] as const

export function isPriceTier(value: string): boolean {
  return (PRICE_TIERS as readonly string[]).includes(value)
}

export function isPerCallItem(value: string): boolean {
  return (PER_CALL_ITEMS as readonly string[]).includes(value)
}

export function isHolidayKind(value: string): boolean {
  return (HOLIDAY_KINDS as readonly string[]).includes(value)
}

/** 正整数（十进制）字符串，版本号等整数字段用。 */
const POSITIVE_INTEGER = /^\d+$/

/** 元金额字符串；提交时转成后端整数微元，避免 float 丢精度。 */
function positiveYuanString(required: string, invalid: string) {
  return z
    .string()
    .trim()
    .min(1, required)
    .refine((value) => {
      const micro = parseYuanToMicro(value)
      return micro !== null && micro > 0
    }, invalid)
}

/**
 * 把 datetime-local 的值按 Asia/Shanghai 解析。
 * 接受 `YYYY-MM-DDTHH:mm` / `YYYY-MM-DDTHH:mm:ss`（也兼容空格）。
 * 返回 null 表示格式或日期不合法。
 */
export function parseShanghaiDateTime(value: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?$/.exec(value.trim())
  if (!match) return null
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const hour = Number(match[4])
  const minute = Number(match[5])
  const second = match[6] ? Number(match[6]) : 0
  if (month < 1 || month > 12 || day < 1 || day > 31) return null
  if (hour > 23 || minute > 59 || second > 59) return null

  // Reject dates that roll over, e.g. 2026-02-30.
  const calendar = new Date(Date.UTC(year, month - 1, day))
  if (
    calendar.getUTCFullYear() !== year ||
    calendar.getUTCMonth() !== month - 1 ||
    calendar.getUTCDate() !== day
  ) {
    return null
  }

  // Asia/Shanghai is UTC+08:00 and has no DST; construct the UTC instant
  // directly from the wall-clock reading.
  return new Date(Date.UTC(year, month - 1, day, hour - 8, minute, second))
}

/** datetime-local 值 → RFC 3339（UTC），后端只接受 RFC 3339。 */
export function shanghaiDateTimeToRfc3339(value: string): string | null {
  const date = parseShanghaiDateTime(value)
  return date ? date.toISOString() : null
}

const shanghaiParts = new Intl.DateTimeFormat('en-US', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

/** 把时间点格式化为 Shanghai 的 datetime-local 值（YYYY-MM-DDTHH:mm）。 */
export function toShanghaiInputValue(date: Date): string {
  const parts: Record<string, string> = {}
  for (const part of shanghaiParts.formatToParts(date)) {
    if (part.type !== 'literal') parts[part.type] = part.value
  }
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`
}

/** 默认生效时间：下一分钟，避免预填值在当前分钟内被后端判为过去。 */
export function defaultEffectiveAt(now: number = Date.now()): string {
  return toShanghaiInputValue(new Date(now + 60_000))
}

/** 当前上海时间所在年份，节假日页默认年份用。 */
export function shanghaiYear(now: number = Date.now()): number {
  return Number(toShanghaiInputValue(new Date(now)).slice(0, 4))
}

function isPastMinute(value: string, now: number): boolean {
  const date = parseShanghaiDateTime(value)
  // datetime-local has minute precision; comparing against `now` matches the
  // backend's exact "must not be earlier than now" rule (the default value is
  // the next minute so it never trips this check on open).
  return date ? date.getTime() < now : false
}

// ---------------------------------------------------------------------------
// 价目表表单
// ---------------------------------------------------------------------------

const prices = copy.pages.adminPrices
const perCall = copy.pages.adminPerCallPrices

export interface PriceFormValues {
  model: string
  vendor: string
  tier: string
  input_micro_per_mtok: string
  output_micro_per_mtok: string
  cache_read_micro_per_mtok: string
  cache_write_micro_per_mtok: string
  version: string
  effective_at: string
}

export function emptyPriceForm(now: number = Date.now()): PriceFormValues {
  return {
    model: '',
    vendor: '',
    tier: 'flat',
    input_micro_per_mtok: '',
    output_micro_per_mtok: '',
    cache_read_micro_per_mtok: '',
    cache_write_micro_per_mtok: '',
    version: '',
    effective_at: defaultEffectiveAt(now),
  }
}

/**
 * 价目表单。tier 必须是 peak/offpeak/flat；四个价格按元填写；
 * version 留空表示由后端自动分配；effective_at 按 Asia/Shanghai 解析且
 * 不得早于当前分钟（后端还会做一次精确校验）。
 */
export function priceFormSchema(now: number = Date.now()) {
  return z
    .object({
      model: z
        .string()
        .trim()
        .min(1, prices.errors.modelRequired)
        .max(128, prices.errors.modelTooLong),
      vendor: z
        .string()
        .trim()
        .min(1, prices.errors.vendorRequired)
        .max(64, prices.errors.vendorTooLong),
      tier: z.string().refine(isPriceTier, prices.errors.tierInvalid),
      input_micro_per_mtok: positiveYuanString(
        prices.errors.priceRequired,
        prices.errors.pricePositive,
      ),
      output_micro_per_mtok: positiveYuanString(
        prices.errors.priceRequired,
        prices.errors.pricePositive,
      ),
      cache_read_micro_per_mtok: positiveYuanString(
        prices.errors.priceRequired,
        prices.errors.pricePositive,
      ),
      cache_write_micro_per_mtok: positiveYuanString(
        prices.errors.priceRequired,
        prices.errors.pricePositive,
      ),
      version: z
        .string()
        .trim()
        .refine(
          (value) => value === '' || (POSITIVE_INTEGER.test(value) && BigInt(value) > 0n),
          prices.errors.versionPositive,
        ),
      effective_at: z
        .string()
        .trim()
        .min(1, prices.errors.effectiveAtRequired)
        .refine((value) => parseShanghaiDateTime(value) !== null, prices.errors.effectiveAtInvalid)
        .refine((value) => !isPastMinute(value, now), prices.errors.effectiveAtPast),
    })
}

export type ValidatedPriceForm = z.infer<ReturnType<typeof priceFormSchema>>

export function priceFormToInput(values: PriceFormValues): CreatePriceInput {
  return {
    model: values.model.trim(),
    vendor: values.vendor.trim(),
    tier: values.tier as CreatePriceInput['tier'],
    input_micro_per_mtok: parseYuanToMicro(values.input_micro_per_mtok) ?? 0,
    output_micro_per_mtok: parseYuanToMicro(values.output_micro_per_mtok) ?? 0,
    cache_read_micro_per_mtok: parseYuanToMicro(values.cache_read_micro_per_mtok) ?? 0,
    cache_write_micro_per_mtok: parseYuanToMicro(values.cache_write_micro_per_mtok) ?? 0,
    version: values.version.trim() ? Number(values.version.trim()) : undefined,
    effective_at: values.effective_at.trim()
      ? (shanghaiDateTimeToRfc3339(values.effective_at) ?? undefined)
      : undefined,
  }
}

// ---------------------------------------------------------------------------
// 按次价目表单
// ---------------------------------------------------------------------------

export interface PerCallFormValues {
  vendor: string
  item: string
  unit_price_micro: string
  version: string
  effective_at: string
}

export function emptyPerCallForm(now: number = Date.now()): PerCallFormValues {
  return {
    vendor: '',
    item: 'search',
    unit_price_micro: '',
    version: '',
    effective_at: defaultEffectiveAt(now),
  }
}

export function perCallFormSchema(now: number = Date.now()) {
  return z.object({
    vendor: z
      .string()
      .trim()
      .min(1, perCall.errors.vendorRequired)
      .max(64, perCall.errors.vendorTooLong),
    item: z.string().refine(isPerCallItem, perCall.errors.itemInvalid),
    unit_price_micro: positiveYuanString(
      perCall.errors.priceRequired,
      perCall.errors.pricePositive,
    ),
    version: z
      .string()
      .trim()
      .refine(
        (value) => value === '' || (POSITIVE_INTEGER.test(value) && BigInt(value) > 0n),
        perCall.errors.versionPositive,
      ),
    effective_at: z
      .string()
      .trim()
      .min(1, perCall.errors.effectiveAtRequired)
      .refine((value) => parseShanghaiDateTime(value) !== null, perCall.errors.effectiveAtInvalid)
      .refine((value) => !isPastMinute(value, now), perCall.errors.effectiveAtPast),
  })
}

export type ValidatedPerCallForm = z.infer<ReturnType<typeof perCallFormSchema>>

export function perCallFormToInput(values: PerCallFormValues): CreatePerCallPriceInput {
  return {
    vendor: values.vendor.trim(),
    item: values.item as PerCallItem,
    unit_price_micro: parseYuanToMicro(values.unit_price_micro) ?? 0,
    version: values.version.trim() ? Number(values.version.trim()) : undefined,
    effective_at: values.effective_at.trim()
      ? (shanghaiDateTimeToRfc3339(values.effective_at) ?? undefined)
      : undefined,
  }
}

// ---------------------------------------------------------------------------
// 节假日导入解析
// ---------------------------------------------------------------------------

const holidays = copy.pages.adminHolidays

export function isRealDate(value: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) return false
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  if (month < 1 || month > 12 || day < 1 || day > 31) return false
  const date = new Date(Date.UTC(year, month - 1, day))
  return (
    date.getUTCFullYear() === year &&
    date.getUTCMonth() === month - 1 &&
    date.getUTCDate() === day
  )
}

const holidayEntrySchema = z.object({
  date: z
    .string()
    .trim()
    .regex(/^\d{4}-\d{2}-\d{2}$/, holidays.errors.dateInvalid)
    .refine(isRealDate, holidays.errors.dateInvalid),
  kind: z.string().trim().refine(isHolidayKind, holidays.errors.kindInvalid),
})

export type HolidayParseResult =
  | { ok: true; entries: AdminHoliday[] }
  | { ok: false; error: string }

function firstIssue(result: z.ZodSafeParseResult<z.infer<typeof holidayEntrySchema>>): string {
  return result.success ? '' : (result.error.issues[0]?.message ?? holidays.errors.entryInvalid)
}

/**
 * 解析节假日导入文本：
 *  - 以 `[` 开头按 JSON 数组解析，元素为 {date, kind}
 *  - 否则逐行解析 `YYYY-MM-DD, holiday|workday`（逗号、空白或中文逗号分隔）
 *
 * 重复日期以最后一次为准，返回按日期升序的数组。
 */
export function parseHolidayInput(text: string): HolidayParseResult {
  const trimmed = text.replace(/^\uFEFF/, '').trim()
  if (!trimmed) return { ok: false, error: holidays.errors.empty }

  const entries: AdminHoliday[] = []

  if (trimmed.startsWith('[') || trimmed.startsWith('{')) {
    let parsed: unknown
    try {
      parsed = JSON.parse(trimmed)
    } catch {
      return { ok: false, error: holidays.errors.jsonInvalid }
    }
    if (!Array.isArray(parsed)) return { ok: false, error: holidays.errors.notArray }
    if (parsed.length === 0) return { ok: false, error: holidays.errors.empty }

    for (const [index, item] of parsed.entries()) {
      const result = holidayEntrySchema.safeParse(item)
      if (!result.success) {
        return {
          ok: false,
          error: holidays.errors.itemError(index + 1, firstIssue(result)),
        }
      }
      entries.push({ date: result.data.date, kind: result.data.kind })
    }
  } else {
    const lines = trimmed.split(/\r?\n/)
    let hasContent = false
    for (let index = 0; index < lines.length; index += 1) {
      const value = lines[index].trim()
      if (!value || value.startsWith('#')) continue
      hasContent = true
      const tokens = value.split(/[\s,，]+/).filter(Boolean)
      if (tokens.length !== 2) {
        return { ok: false, error: holidays.errors.lineError(index + 1, holidays.errors.lineFormat) }
      }
      const result = holidayEntrySchema.safeParse({ date: tokens[0], kind: tokens[1] })
      if (!result.success) {
        return {
          ok: false,
          error: holidays.errors.lineError(index + 1, firstIssue(result)),
        }
      }
      entries.push({ date: result.data.date, kind: result.data.kind })
    }
    if (!hasContent) return { ok: false, error: holidays.errors.empty }
  }

  const deduped = new Map<string, AdminHoliday>()
  for (const entry of entries) deduped.set(entry.date, entry)
  const list = [...deduped.values()].sort((a, b) => a.date.localeCompare(b.date))
  if (list.length === 0) return { ok: false, error: holidays.errors.empty }
  return { ok: true, entries: list }
}

export const holidayKindLabels: Record<HolidayKind, string> = {
  holiday: holidays.kind.holiday,
  workday: holidays.kind.workday,
}
