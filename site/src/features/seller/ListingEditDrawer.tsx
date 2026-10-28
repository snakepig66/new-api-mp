/**
 * 渠道编辑抽屉（ShareLLM 风格的七区块面板）。
 *
 * 契约：列表页渲染
 *   <ListingEditDrawer listingId={id} onClose={fn} />
 * 右侧 lg 抽屉 + 左侧固定区块导航；底部 SaveBar 只提交有改动的字段，
 * 每个区块走自己的接口（基本信息/限制 → updateListingSettings 的部分字段，
 * 模型 → setListingModelState 逐行，请求头 → submitListingHeaders），不拼大包。
 * 保存失败时抽屉保持打开、高亮失败区块并 toast 原因；成功后失效
 * ['marketplace','seller']。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { formatDateTime, formatTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Drawer,
  Input,
  Multiplier,
  PasswordInput,
  SaveBar,
  Switch,
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
  modelChangeErrors,
  pauseListing,
  replaceListingKey,
  restoreListingModels,
  resumeListing,
  sellerListingDetailQuery,
  sellerListingHeadersQuery,
  sellerListingModelsQuery,
  setListingModelState,
  setListingModelsState,
  submitListingHeaders,
  updateListingSettings,
  verifyListing,
  vendorName,
  type SellerHeaderEntry,
  type SellerListingDetail,
  type SellerListingModelsResponse,
  type SellerModelRow,
  type SellerModelsRestoreResult,
  type SellerProbe,
  type SetListingModelStateInput,
  type UpdateListingSettingsInput,
} from '@/features/seller/api'
import { AsyncError, Field, Skeleton, notify } from '@/features/seller/components'
import { probeFailReasonLabel, sellerErrorMessage } from '@/features/seller/errors'
import { historyProbeLatencyMs, probeTypeLabel } from '@/features/seller/probe'
import {
  isListingKeyReplaceable,
  isListingPausable,
  isListingReverifiable,
  isListingSellerResumable,
  listingStatusView,
  pauseReasonView,
} from '@/features/seller/status'
import { VerificationSummary } from '@/features/seller/verification'

const page = copy.pages.sellerListingEdit
const common = copy.pages.sellerCommon

type SectionKey = 'overview' | 'basic' | 'key' | 'models' | 'limits' | 'verify' | 'advanced'

const SECTION_ORDER: SectionKey[] = [
  'overview',
  'basic',
  'key',
  'models',
  'limits',
  'verify',
  'advanced',
]

function sectionId(key: SectionKey): string {
  return `listing-edit-${key}`
}

function sectionLabel(key: SectionKey): string {
  return page.nav[key]
}

/** 0.300 风格的输入初值；milli 始终来自后端整数。 */
function milliText(milli: number): string {
  return (milli / 1000).toFixed(3)
}

/** "0.300" → 300；非法输入返回 null。 */
function parseMilli(text: string): number | null {
  const parsed = Number(text)
  if (!Number.isFinite(parsed) || text.trim() === '') return null
  return Math.round(parsed * 1000)
}

/** 微元整数 → 输入框里的元文本（100_000_000 → "100"）。仅用于初值/回填。 */
function microToYuanText(micro: number | string): string {
  const yuan = Number(micro) / 1_000_000
  if (!Number.isFinite(yuan)) return '0'
  return String(yuan)
}

/** 营业时段 JSON → 「09:00–12:00、13:00–18:00」；空/坏数据回落全天。 */
function hoursText(hours: string): string {
  if (!hours.trim()) return page.limits.allDay
  try {
    const windows = JSON.parse(hours) as { start?: string; end?: string }[]
    if (!Array.isArray(windows) || windows.length === 0) return page.limits.allDay
    const text = windows
      .filter((window) => window.start && window.end)
      .map((window) => `${window.start}–${window.end}`)
      .join('、')
    return text || page.limits.allDay
  } catch {
    return hours
  }
}

function disabledReasonText(reason: string): string {
  return page.models.disabledReasons[reason] ?? (reason || page.models.disabledReasonFallback)
}

interface ModelDraft {
  multiplierText: string
  enabled: boolean
}

interface HeaderRow {
  name: string
  value: string
}

function headerSignature(rows: HeaderRow[]): string {
  return JSON.stringify(rows.map((row) => [row.name.trim(), row.value]))
}

function InfoField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="rounded-md bg-bg-soft px-3.5 py-3">
      <dt className="text-[12px] text-ink-3">{label}</dt>
      <dd className="mt-1 text-[14px] leading-5 text-ink">{children}</dd>
    </div>
  )
}

function Section({
  id,
  title,
  lead,
  failing,
  children,
}: {
  id: SectionKey
  title: string
  lead?: string
  failing?: boolean
  children: ReactNode
}) {
  return (
    <section
      id={sectionId(id)}
      aria-label={title}
      className={cn(
        'scroll-mt-2 rounded-md border border-line bg-bg p-5',
        failing && 'border-danger/40 ring-2 ring-danger/30',
      )}
    >
      <h3 className="text-[15px] font-semibold tracking-tight text-ink">{title}</h3>
      {lead ? <p className="mt-1 text-[13px] leading-5 text-ink-3">{lead}</p> : null}
      <div className="mt-4 space-y-4">{children}</div>
    </section>
  )
}

// ---------------------------------------------------------------------------
// 换 Key 弹窗：复用列表页 KeyDialog 的形态（密码输入 + 末 4 位预览）。
// ---------------------------------------------------------------------------

function ReplaceKeyDialog({
  listingId,
  open,
  onClose,
}: {
  listingId: number
  open: boolean
  onClose: () => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [apiKey, setApiKey] = useState('')
  const [error, setError] = useState<string | null>(null)

  const replace = useMutation({
    mutationFn: (key: string) => replaceListingKey(listingId, key),
    onSuccess: () => {
      setApiKey('')
      setError(null)
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
      notify(toast, { title: common.toast.keyUpdated, tone: 'success' })
      onClose()
    },
    onError: (err) => setError(sellerErrorMessage(err)),
  })

  useEffect(() => {
    if (open) {
      setApiKey('')
      setError(null)
    }
  }, [open])

  const trimmed = apiKey.trim()
  const tail = trimmed.length >= 4 ? trimmed.slice(-4) : ''
  const preview = tail ? page.key.dialog.preview(tail) : page.key.dialog.previewEmpty

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={page.key.dialog.title}
      description={page.key.dialog.lead}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {copy.actions.close}
          </Button>
          <Button
            disabled={trimmed.length < 8}
            loading={replace.isPending}
            onClick={() => replace.mutate(trimmed)}
          >
            {replace.isPending ? page.key.dialog.submitting : page.key.dialog.submit}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field
          label={page.key.dialog.label}
          htmlFor="listing-edit-replacement-key"
          hint={page.key.dialog.hint}
          error={error ?? undefined}
        >
          <PasswordInput
            id="listing-edit-replacement-key"
            autoComplete="new-password"
            spellCheck={false}
            placeholder={page.key.dialog.placeholder}
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>

        <div className="rounded-md bg-bg-soft px-4 py-3">
          <div className="text-[13px] text-ink-3">{common.keyPreviewHint}</div>
          <div className="mt-1 tabular-nums text-[14px] text-ink">{preview}</div>
        </div>
      </div>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// 模型价格表：每个模型一张 计费项 的小表，官方价/买家价/到手价。
// ---------------------------------------------------------------------------

const PRICE_DIMENSIONS: {
  key: 'input_micro_per_mtok' | 'output_micro_per_mtok' | 'cache_read_micro_per_mtok' | 'cache_write_micro_per_mtok'
  label: string
}[] = [
  { key: 'input_micro_per_mtok', label: page.models.dimensions.input },
  { key: 'output_micro_per_mtok', label: page.models.dimensions.output },
  { key: 'cache_read_micro_per_mtok', label: page.models.dimensions.cache_read },
  { key: 'cache_write_micro_per_mtok', label: page.models.dimensions.cache_write },
]

function ModelPriceTable({ row, feePercent }: { row: SellerModelRow; feePercent: number }) {
  const officialPrices = row.official_prices.flat
  const buyerPrices = row.buyer_prices.flat
  if (officialPrices === undefined && buyerPrices === undefined) {
    return <p className="mt-3 text-[13px] text-ink-3">{page.models.noPrices}</p>
  }
  return (
    <div className="mt-3">
      <Table minWidth={440} size="compact">
        <TableHead>
          <TableRow interactive={false}>
            <TableHeaderCell>{page.models.priceColumns.dimension}</TableHeaderCell>
            <TableHeaderCell numeric>{page.models.priceColumns.official}</TableHeaderCell>
            <TableHeaderCell numeric>{page.models.priceColumns.buyer}</TableHeaderCell>
            <TableHeaderCell numeric>{page.models.priceColumns.net}</TableHeaderCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {PRICE_DIMENSIONS.map((dimension) => {
            const official = officialPrices?.[dimension.key]
            const buyer = buyerPrices?.[dimension.key]
            // 到手价 = 买家价 × (1 - 平台费)。整数微元上取整，仅用于展示。
            const net =
              buyer !== undefined ? Math.round((buyer * (100 - feePercent)) / 100) : undefined
            return (
              <TableRow key={dimension.key} interactive={false}>
                <TableCell className="text-[13px]">{dimension.label}</TableCell>
                <TableCell numeric className="text-[13px] text-ink-3">
                  {official === undefined ? '—' : formatYuan(official)}
                </TableCell>
                <TableCell numeric className="text-[13px]">
                  {buyer === undefined ? '—' : formatYuan(buyer)}
                </TableCell>
                <TableCell numeric className="text-[13px] text-ink-2">
                  {net === undefined ? '—' : formatYuan(net)}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

// ---------------------------------------------------------------------------
// 主组件
// ---------------------------------------------------------------------------

export function ListingEditDrawer({
  listingId,
  onClose,
}: {
  listingId: number
  onClose: () => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const validId = Number.isInteger(listingId) && listingId > 0

  const detail = useQuery({ ...sellerListingDetailQuery(listingId), enabled: validId })
  const models = useQuery({ ...sellerListingModelsQuery(listingId), enabled: validId })
  const headers = useQuery({ ...sellerListingHeadersQuery(listingId), enabled: validId })

  // -- 本地草稿 ------------------------------------------------------------
  const [activeSection, setActiveSection] = useState<SectionKey>('overview')
  const [failingSection, setFailingSection] = useState<SectionKey | null>(null)
  const [sectionErrors, setSectionErrors] = useState<Partial<Record<SectionKey, string>>>({})
  const [saving, setSaving] = useState(false)

  const [labelDraft, setLabelDraft] = useState('')
  const [concurrencyDraft, setConcurrencyDraft] = useState('')
  const [rpmDraft, setRpmDraft] = useState('')
  const [dailyCapDraft, setDailyCapDraft] = useState('')

  const [modelDrafts, setModelDrafts] = useState<Record<string, ModelDraft>>({})
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({})
  const [savingModel, setSavingModel] = useState<string | null>(null)
  const [batchText, setBatchText] = useState('')
  const [restoreResult, setRestoreResult] = useState<SellerModelsRestoreResult | null>(null)

  const [headerRows, setHeaderRows] = useState<HeaderRow[]>([])
  const [headerInitial, setHeaderInitial] = useState('[]')
  const [confirmClear, setConfirmClear] = useState(false)
  const [clearing, setClearing] = useState(false)

  const [keyDialogOpen, setKeyDialogOpen] = useState(false)

  const listing = detail.data

  // 详情首次载入（或切换渠道）时回填草稿；之后的刷新不会覆盖用户输入。
  const [initializedFor, setInitializedFor] = useState<number | null>(null)
  useEffect(() => {
    if (!listing || initializedFor === listing.id) return
    setInitializedFor(listing.id)
    setLabelDraft(listing.label ?? '')
    setConcurrencyDraft(String(listing.concurrency_limit))
    setRpmDraft(String(listing.rpm_limit))
    setDailyCapDraft(microToYuanText(listing.daily_cap_micro))
  }, [listing, initializedFor])

  const headersView = headers.data
  const [headersInitializedFor, setHeadersInitializedFor] = useState<number | null>(null)
  useEffect(() => {
    if (!headersView || headersInitializedFor === listingId) return
    setHeadersInitializedFor(listingId)
    resetHeaderRows(headersView.headers ?? [])
  }, [headersView, headersInitializedFor, listingId])

  function resetHeaderRows(entries: SellerHeaderEntry[]) {
    const rows: HeaderRow[] = entries.map((entry) => ({ name: entry.name, value: '' }))
    setHeaderRows(rows)
    setHeaderInitial(headerSignature(rows))
  }

  function updateHeaderRow(index: number, patch: Partial<HeaderRow>) {
    setHeaderRows((current) =>
      current.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch } : row)),
    )
  }

  function removeHeaderRow(index: number) {
    setHeaderRows((current) => current.filter((_, rowIndex) => rowIndex !== index))
  }

  function addHeaderRow() {
    setHeaderRows((current) =>
      current.length >= 20 ? current : [...current, { name: '', value: '' }],
    )
  }

  // -- 脏状态 --------------------------------------------------------------
  const trimmedLabel = labelDraft.trim()
  const labelInvalid = trimmedLabel.length > 64
  const labelDirty = listing ? trimmedLabel !== (listing.label ?? '') : false

  const concurrencyValue = Number(concurrencyDraft)
  const rpmValue = Number(rpmDraft)
  const dailyCapYuan = Number(dailyCapDraft)
  const concurrencyValid =
    concurrencyDraft.trim() !== '' && Number.isInteger(concurrencyValue) && concurrencyValue >= 0
  const rpmValid = rpmDraft.trim() !== '' && Number.isInteger(rpmValue) && rpmValue >= 0
  const dailyCapValid = dailyCapDraft.trim() !== '' && Number.isFinite(dailyCapYuan) && dailyCapYuan >= 0
  const dailyCapMicro = Math.round(dailyCapYuan * 1_000_000)
  const concurrencyDirty = listing ? concurrencyValid && concurrencyValue !== listing.concurrency_limit : false
  const rpmDirty = listing ? rpmValid && rpmValue !== listing.rpm_limit : false
  const dailyCapDirty = listing
    ? dailyCapValid && dailyCapMicro !== Number(listing.daily_cap_micro)
    : false
  const limitsDirty = concurrencyDirty || rpmDirty || dailyCapDirty

  const modelRows = models.data?.items ?? []

  function rowDraft(row: SellerModelRow): ModelDraft | undefined {
    return modelDrafts[row.model]
  }

  function rowMultiplierRange(row: SellerModelRow): { min: number; max: number } {
    const fallbackMin = models.data?.multiplier_min_milli || 1
    const fallbackMax = models.data?.multiplier_max_milli || 1000
    return {
      min: row.multiplier_min_milli > 0 ? row.multiplier_min_milli : fallbackMin,
      max: row.multiplier_max_milli > 0 ? row.multiplier_max_milli : fallbackMax,
    }
  }

  function rowMultiplierValid(row: SellerModelRow): boolean {
    const draft = rowDraft(row)
    if (!draft) return true
    const milli = parseMilli(draft.multiplierText)
    if (milli === null) return false
    const { min, max } = rowMultiplierRange(row)
    return milli >= min && milli <= max
  }

  function rowDirty(row: SellerModelRow): boolean {
    const draft = rowDraft(row)
    if (!draft) return false
    const milli = parseMilli(draft.multiplierText)
    const multiplierChanged = milli !== null && milli !== row.multiplier_milli
    return multiplierChanged || draft.enabled !== row.enabled
  }

  function rowPayload(row: SellerModelRow): SetListingModelStateInput {
    const draft = rowDraft(row)
    const payload: SetListingModelStateInput = {}
    if (!draft) return payload
    const milli = parseMilli(draft.multiplierText)
    if (milli !== null && milli !== row.multiplier_milli) payload.multiplier_milli = milli
    if (draft.enabled !== row.enabled) payload.enabled = draft.enabled
    return payload
  }

  function updateDraft(row: SellerModelRow, patch: Partial<ModelDraft>) {
    setModelDrafts((current) => ({
      ...current,
      [row.model]: {
        multiplierText: current[row.model]?.multiplierText ?? milliText(row.multiplier_milli),
        enabled: current[row.model]?.enabled ?? row.enabled,
        ...patch,
      },
    }))
  }

  const dirtyModelRows = modelRows.filter((row) => rowDirty(row))

  const headersDirty = headerSignature(headerRows) !== headerInitial

  function headerRowIssues(): string | null {
    for (const row of headerRows) {
      const name = row.name.trim()
      if (!name && !row.value.trim()) continue
      if (!name) return page.advanced.headerValueRequired
      if (/\s/.test(name)) return page.advanced.headerNameInvalid
      if (!row.value.trim()) return page.advanced.headerValueRequired
    }
    return null
  }

  function buildHeaderObject(): Record<string, string> {
    const out: Record<string, string> = {}
    for (const row of headerRows) {
      const name = row.name.trim()
      const value = row.value.trim()
      if (name && value) out[name] = value
    }
    return out
  }

  const dirtySections: SectionKey[] = []
  if (labelDirty) dirtySections.push('basic')
  if (limitsDirty) dirtySections.push('limits')
  if (dirtyModelRows.length > 0) dirtySections.push('models')
  if (headersDirty) dirtySections.push('advanced')
  const anyDirty = dirtySections.length > 0

  const saveBlocked =
    (labelDirty && labelInvalid) ||
    (concurrencyDirty && !concurrencyValid) ||
    (rpmDirty && !rpmValid) ||
    (dailyCapDirty && !dailyCapValid) ||
    dirtyModelRows.some((row) => !rowMultiplierValid(row)) ||
    (headersDirty && headerRowIssues() !== null)

  // -- 保存 ----------------------------------------------------------------
  function setSectionError(key: SectionKey, message: string) {
    setSectionErrors((current) => ({ ...current, [key]: message }))
  }

  function scrollToSection(key: SectionKey) {
    setActiveSection(key)
    if (typeof document === 'undefined') return
    const element = document.getElementById(sectionId(key))
    if (element && typeof element.scrollIntoView === 'function') {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  }

  function applyModelChangeRow(model: string, updated: {
    model: string
    enabled: boolean
    multiplier_milli: number
    pending_multiplier_milli: number
    pending_effective_at?: string
    auto_disabled: boolean
    disabled_reason: string
  }) {
    queryClient.setQueryData<SellerListingModelsResponse>(
      sellerListingModelsQuery(listingId).queryKey,
      (current) =>
        current
          ? {
              ...current,
              items: current.items.map((item) =>
                item.model === model ? { ...item, ...updated } : item,
              ),
            }
          : current,
    )
  }

  function modelRowError(row: SellerModelRow, error: unknown): string {
    const rejected = modelChangeErrors(error)
    const hit = rejected.find((item) => item.model === row.model) ?? rejected[0]
    if (hit) {
      return hit.earliest_change_at
        ? page.models.rejected(hit.message, formatDateTime(hit.earliest_change_at))
        : hit.message
    }
    return sellerErrorMessage(error)
  }

  async function saveRow(row: SellerModelRow): Promise<{ ok: boolean; message?: string }> {
    const payload = rowPayload(row)
    if (Object.keys(payload).length === 0) return { ok: true }
    setRowErrors((current) => {
      const next = { ...current }
      delete next[row.model]
      return next
    })
    setSavingModel(row.model)
    try {
      const updated = await setListingModelState(listingId, row.model, payload)
      applyModelChangeRow(row.model, updated)
      setModelDrafts((current) => {
        const next = { ...current }
        delete next[row.model]
        return next
      })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
      return { ok: true }
    } catch (error) {
      const message = modelRowError(row, error)
      setRowErrors((current) => ({ ...current, [row.model]: message }))
      return { ok: false, message }
    } finally {
      setSavingModel(null)
    }
  }

  async function submitHeadersFlow(): Promise<{
    outcome: 'saved' | 'confirm' | 'failed' | 'noop'
    message?: string
  }> {
    const built = buildHeaderObject()
    const liveCount = headersView?.headers.length ?? 0
    if (Object.keys(built).length === 0) {
      if (liveCount > 0) {
        setConfirmClear(true)
        return { outcome: 'confirm' }
      }
      // 没有存量也没有新值：把空行收掉即可。
      resetHeaderRows([])
      return { outcome: 'noop' }
    }
    try {
      const result = await submitListingHeaders(listingId, built)
      resetHeaderRows(result.headers ?? [])
      notify(toast, { title: page.advanced.headersSubmitted, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
      return { outcome: 'saved' }
    } catch (error) {
      const message = sellerErrorMessage(error)
      setSectionError('advanced', message)
      return { outcome: 'failed', message }
    }
  }

  async function confirmClearHeaders() {
    setClearing(true)
    try {
      const result = await submitListingHeaders(listingId, {})
      resetHeaderRows(result.headers ?? [])
      notify(toast, { title: page.advanced.headersCleared, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    } catch (error) {
      setSectionError('advanced', sellerErrorMessage(error))
    } finally {
      setClearing(false)
      setConfirmClear(false)
    }
  }

  async function saveAll() {
    if (!listing || saving) return
    setSaving(true)
    setFailingSection(null)
    setSectionErrors({})
    const failed: SectionKey[] = []
    let firstMessage: string | null = null
    let confirmOpened = false

    if (labelDirty && !labelInvalid) {
      try {
        const updated = await updateListingSettings(listingId, { label: trimmedLabel })
        setLabelDraft(updated.label ?? trimmedLabel)
      } catch (error) {
        failed.push('basic')
        const message = sellerErrorMessage(error)
        setSectionError('basic', message)
        firstMessage ??= message
      }
    }

    if (limitsDirty) {
      const payload: UpdateListingSettingsInput = {}
      if (concurrencyDirty) payload.concurrency_limit = concurrencyValue
      if (rpmDirty) payload.rpm_limit = rpmValue
      if (dailyCapDirty) payload.daily_cap_micro = dailyCapMicro
      try {
        const updated = await updateListingSettings(listingId, payload)
        setConcurrencyDraft(String(updated.concurrency_limit))
        setRpmDraft(String(updated.rpm_limit))
        setDailyCapDraft(microToYuanText(updated.daily_cap_micro))
      } catch (error) {
        failed.push('limits')
        const message = sellerErrorMessage(error)
        setSectionError('limits', message)
        firstMessage ??= message
      }
    }

    for (const row of dirtyModelRows) {
      const result = await saveRow(row)
      if (!result.ok) {
        failed.push('models')
        firstMessage ??= result.message ?? null
      }
    }

    if (headersDirty && headersView?.custom_channel && headerRowIssues() === null) {
      const { outcome, message } = await submitHeadersFlow()
      if (outcome === 'failed') {
        failed.push('advanced')
        firstMessage ??= message ?? null
      } else if (outcome === 'confirm') {
        confirmOpened = true
      }
    }

    setSaving(false)
    if (failed.length === 0) {
      if (!confirmOpened) {
        notify(toast, { title: page.saveBar.saved, tone: 'success' })
      }
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    } else {
      setFailingSection(failed[0])
      notify(toast, {
        title:
          firstMessage ??
          page.saveBar.failed(sectionLabel(failed[0])),
        tone: 'error',
      })
      scrollToSection(failed[0])
    }
  }

  // -- 批量 / 巡检 / 启停 --------------------------------------------------
  function applyBatchErrors(error: unknown) {
    const rejected = modelChangeErrors(error)
    if (rejected.length > 0) {
      setRowErrors((current) => ({
        ...current,
        ...Object.fromEntries(
          rejected.map((item) => [
            item.model,
            item.earliest_change_at
              ? page.models.rejected(item.message, formatDateTime(item.earliest_change_at))
              : item.message,
          ]),
        ),
      }))
      return
    }
    notify(toast, { title: sellerErrorMessage(error), tone: 'error' })
  }

  const batchApply = useMutation({
    mutationFn: (milli: number) => setListingModelsState(listingId, { multiplier_milli: milli }),
    onSuccess: () => {
      setModelDrafts({})
      setRowErrors({})
      notify(toast, { title: page.models.batch.applied, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    },
    onError: (error) => applyBatchErrors(error),
  })

  const enableAll = useMutation({
    mutationFn: () => setListingModelsState(listingId, { enabled: true }),
    onSuccess: () => {
      setModelDrafts({})
      setRowErrors({})
      notify(toast, { title: page.models.batch.enabledAll, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    },
    onError: (error) => applyBatchErrors(error),
  })

  const restoreAll = useMutation({
    mutationFn: () => restoreListingModels(listingId),
    onSuccess: (result) => {
      setRestoreResult(result)
      setModelDrafts({})
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })

  const verify = useMutation({
    mutationFn: () => verifyListing(listingId),
    onSuccess: () => {
      // 乐观置为验真中，让详情查询的 3s 轮询立刻接上。
      queryClient.setQueryData<SellerListingDetail>(
        sellerListingDetailQuery(listingId).queryKey,
        (row) => (row ? { ...row, status: 'verifying' } : row),
      )
      notify(toast, { title: common.toast.verifyStarted, tone: 'success' })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })

  const pause = useMutation({
    mutationFn: () => pauseListing(listingId),
    onSuccess: () => {
      notify(toast, { title: common.toast.paused, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })

  const resume = useMutation({
    mutationFn: () => resumeListing(listingId),
    onSuccess: () => {
      notify(toast, { title: common.toast.resumed, tone: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
    },
    onError: (error) => notify(toast, { title: sellerErrorMessage(error), tone: 'error' }),
  })

  // -- 渲染 ----------------------------------------------------------------
  function handleOpenChange(next: boolean) {
    if (!next) onClose()
  }

  if (!validId) {
    return (
      <AsyncError
        title={page.loadError}
        reason={common.errors.listingNotFound}
        retryLabel={copy.actions.retry}
      />
    )
  }

  if (detail.isPending) {
    return (
      <Drawer
        open
        onOpenChange={handleOpenChange}
        title={page.title}
        description={page.lead}
        width="lg"
      >
        <div className="space-y-3" aria-busy="true" aria-live="polite">
          {Array.from({ length: 6 }, (_, index) => (
            <Skeleton key={index} className="h-20 w-full" />
          ))}
        </div>
      </Drawer>
    )
  }

  if (detail.isError || !listing) {
    return (
      <Drawer
        open
        onOpenChange={handleOpenChange}
        title={page.title}
        description={page.lead}
        width="lg"
      >
        <AsyncError
          title={page.loadError}
          reason={sellerErrorMessage(detail.error)}
          onRetry={() => void detail.refetch()}
          retryLabel={copy.actions.retry}
        />
      </Drawer>
    )
  }

  const status = listingStatusView(listing.status)
  const feePercent = models.data?.platform_fee_percent ?? 0
  const batchMilli = parseMilli(batchText)
  const globalRange = {
    min: models.data?.multiplier_min_milli || 1,
    max: models.data?.multiplier_max_milli || 1000,
  }
  const batchValid =
    batchMilli !== null && batchMilli >= globalRange.min && batchMilli <= globalRange.max

  const nav = (
    <nav aria-label={page.navLabel} className="sticky top-0 z-10 w-28 shrink-0 pb-2">
      <ul className="space-y-1">
        {SECTION_ORDER.map((key) => (
          <li key={key}>
            <button
              type="button"
              aria-current={activeSection === key ? 'true' : undefined}
              onClick={() => scrollToSection(key)}
              className={cn(
                'w-full rounded-md px-2.5 py-1.5 text-left text-[13px] leading-5 transition-colors',
                activeSection === key
                  ? 'bg-brand-soft font-medium text-brand-ink'
                  : 'text-ink-3 hover:bg-bg-soft hover:text-ink',
              )}
            >
              {sectionLabel(key)}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  )

  return (
    <>
      <Drawer
        open
        onOpenChange={handleOpenChange}
        title={page.title}
        description={`${vendorName(listing.vendor)} · #${listing.id}`}
        width="lg"
        footer={
          <SaveBar
            dirty={anyDirty}
            saving={saving}
            onSave={() => void saveAll()}
            onCancel={onClose}
            saveDisabled={saveBlocked}
            saveLabel={page.saveBar.save}
            savingLabel={page.saveBar.saving}
            cancelLabel={page.saveBar.close}
            hint={
              anyDirty
                ? page.saveBar.dirty(dirtySections.map(sectionLabel).join('、'))
                : undefined
            }
          />
        }
      >
        <div className="flex items-start gap-5">
          {nav}
          <div className="min-w-0 flex-1 space-y-6">
            {/* 1. 概览 */}
            <Section id="overview" title={page.overview.title} lead={page.overview.lead}>
              <dl className="grid gap-3 sm:grid-cols-2">
                <InfoField label={page.overview.fields.status}>
                  <Tag tone={status.tone} dot>
                    {status.label}
                  </Tag>
                </InfoField>
                <InfoField label={page.overview.fields.fingerprint}>
                  <span className="font-mono">{listing.key_fingerprint8 || '—'}</span>
                </InfoField>
                <InfoField label={page.overview.fields.vendor}>
                  {vendorName(listing.vendor)}
                </InfoField>
                <InfoField label={page.overview.fields.label}>
                  {listing.label?.trim() || page.overview.labelEmpty}
                </InfoField>
                <InfoField label={page.overview.fields.created}>
                  {formatDateTime(listing.created_at)}
                </InfoField>
              </dl>
              <div>
                <h4 className="text-[13px] font-medium text-ink">
                  {page.overview.verificationTitle}
                </h4>
                <div className="mt-2">
                  {listing.verification ? (
                    <VerificationSummary verification={listing.verification} compact />
                  ) : (
                    <p className="text-[13px] text-ink-3">{page.overview.verificationEmpty}</p>
                  )}
                </div>
              </div>
            </Section>

            {/* 2. 基本信息 */}
            <Section id="basic" title={page.basic.title} lead={page.basic.lead}>
              <Field
                label={page.basic.label}
                htmlFor="listing-edit-label"
                hint={page.basic.labelHint}
                error={labelInvalid ? page.basic.labelInvalid : sectionErrors.basic}
              >
                <Input
                  id="listing-edit-label"
                  maxLength={64}
                  placeholder={page.basic.labelPlaceholder}
                  value={labelDraft}
                  invalid={labelInvalid}
                  onChange={(event) => setLabelDraft(event.target.value)}
                />
              </Field>
              <dl className="grid gap-3 sm:grid-cols-2">
                <InfoField label={page.basic.vendor}>{vendorName(listing.vendor)}</InfoField>
                <InfoField label={page.basic.modelsCount}>
                  {page.basic.modelsCountText(listing.models.length)}
                </InfoField>
              </dl>
            </Section>

            {/* 3. 密钥 */}
            <Section id="key" title={page.key.title} lead={page.key.lead}>
              <InfoField label={page.key.fingerprint}>
                <span className="font-mono">{listing.key_fingerprint8 || '—'}</span>
              </InfoField>
              <div>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={!isListingKeyReplaceable(listing.status)}
                  onClick={() => setKeyDialogOpen(true)}
                >
                  {page.key.replace}
                </Button>
              </div>
            </Section>

            {/* 4. 模型与价格 */}
            <Section
              id="models"
              title={page.models.title}
              lead={page.models.lead}
              failing={failingSection === 'models'}
            >
              <p className="text-[13px] leading-5 text-ink-3">{page.models.priceRule}</p>
              {models.data ? (
                <p className="text-[13px] leading-5 text-ink-3">
                  {page.models.feeNote(String(models.data.platform_fee_percent))}
                </p>
              ) : null}
              {sectionErrors.models ? (
                <p role="alert" className="text-[13px] text-danger">
                  {sectionErrors.models}
                </p>
              ) : null}

              {models.isPending ? (
                <div className="space-y-2" aria-busy="true" aria-live="polite">
                  {Array.from({ length: 2 }, (_, index) => (
                    <Skeleton key={index} className="h-24 w-full" />
                  ))}
                </div>
              ) : models.isError ? (
                <AsyncError
                  title={page.models.loadError}
                  reason={sellerErrorMessage(models.error)}
                  onRetry={() => void models.refetch()}
                  retryLabel={copy.actions.retry}
                />
              ) : modelRows.length === 0 ? (
                <p className="text-[13px] text-ink-3">{page.models.empty}</p>
              ) : (
                <>
                  <div className="flex flex-wrap items-end gap-3 rounded-md bg-bg-soft px-3.5 py-3">
                    <div className="w-32">
                      <label
                        htmlFor="listing-edit-batch-multiplier"
                        className="block text-[13px] font-medium text-ink"
                      >
                        {page.models.batch.multiplier}
                      </label>
                      <Input
                        id="listing-edit-batch-multiplier"
                        className="mt-1 h-9"
                        inputMode="decimal"
                        placeholder={page.models.batch.multiplierPlaceholder}
                        value={batchText}
                        onChange={(event) => setBatchText(event.target.value)}
                      />
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={!batchValid}
                      loading={batchApply.isPending}
                      onClick={() => {
                        if (batchMilli !== null) batchApply.mutate(batchMilli)
                      }}
                    >
                      {page.models.batch.apply}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      loading={enableAll.isPending}
                      onClick={() => enableAll.mutate()}
                    >
                      {page.models.batch.enableAll}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      loading={restoreAll.isPending}
                      onClick={() => restoreAll.mutate()}
                    >
                      {page.models.batch.restore}
                    </Button>
                  </div>

                  {restoreResult ? (
                    <div role="status" className="rounded-md border border-line bg-bg-soft px-3.5 py-3">
                      <p className="text-[13px] text-ink">
                        {page.models.batch.restoreResult(restoreResult.restored)}
                      </p>
                      {restoreResult.still_failing.length > 0 ? (
                        <div className="mt-1">
                          <p className="text-[13px] text-danger">
                            {page.models.batch.stillFailing(restoreResult.still_failing.length)}
                          </p>
                          <ul className="mt-1 space-y-1">
                            {restoreResult.still_failing.map((item) => (
                              <li key={item.model} className="text-[13px] leading-5 text-ink-3">
                                <span className="font-mono">{item.model}</span>
                                {item.message ? `：${item.message}` : ''}
                              </li>
                            ))}
                          </ul>
                        </div>
                      ) : null}
                    </div>
                  ) : null}

                  <div className="space-y-4">
                    {modelRows.map((row) => {
                      const draft = rowDraft(row)
                      const range = rowMultiplierRange(row)
                      const multiplierValid = rowMultiplierValid(row)
                      const dirty = rowDirty(row)
                      return (
                        <div key={row.model} className="rounded-md border border-line p-4">
                          <div className="flex flex-wrap items-center justify-between gap-3">
                            <div className="flex min-w-0 items-center gap-3">
                              <Switch
                                checked={draft?.enabled ?? row.enabled}
                                onCheckedChange={(next) => updateDraft(row, { enabled: next })}
                                aria-label={page.models.enabledLabel(row.model)}
                              />
                              <span className="min-w-0 truncate font-mono text-[13px] text-ink">
                                {row.model}
                              </span>
                              {row.auto_disabled ? (
                                <Tag tone="danger" dot>
                                  {page.models.disabledTag}
                                </Tag>
                              ) : null}
                            </div>
                            <div className="flex items-center gap-2">
                              <Input
                                aria-label={page.models.multiplierLabel(row.model)}
                                className="h-9 w-24 text-right"
                                inputMode="decimal"
                                invalid={!multiplierValid}
                                value={draft?.multiplierText ?? milliText(row.multiplier_milli)}
                                onChange={(event) =>
                                  updateDraft(row, { multiplierText: event.target.value })
                                }
                              />
                              <span className="text-[13px] text-ink-3">x</span>
                              <Button
                                variant="secondary"
                                size="sm"
                                aria-label={page.models.saveLabel(row.model)}
                                disabled={!dirty || !multiplierValid}
                                loading={savingModel === row.model}
                                onClick={() => void saveRow(row)}
                              >
                                {page.models.save}
                              </Button>
                            </div>
                          </div>

                          {!multiplierValid ? (
                            <p role="alert" className="mt-2 text-[13px] text-danger">
                              {page.models.multiplierRange(
                                (range.min / 1000).toFixed(3),
                                (range.max / 1000).toFixed(3),
                              )}
                            </p>
                          ) : null}

                          {row.pending_multiplier_milli > 0 && row.pending_effective_at ? (
                            <p className="mt-2 text-[13px] text-warn" role="status">
                              {page.models.pendingLine(
                                formatTime(row.pending_effective_at),
                                milliText(row.pending_multiplier_milli),
                              )}
                            </p>
                          ) : null}

                          {row.auto_disabled ? (
                            <div className="mt-3 rounded-md border border-danger/25 bg-danger/5 px-3.5 py-3">
                              <h4 className="text-[13px] font-medium text-danger">
                                {page.models.disabledTitle}
                              </h4>
                              <dl className="mt-2 grid gap-2 sm:grid-cols-2">
                                <InfoField label={page.models.disabledFields.reason}>
                                  {disabledReasonText(row.disabled_reason)}
                                </InfoField>
                                <InfoField label={page.models.disabledFields.upstreamStatus}>
                                  {row.disabled_upstream_status > 0
                                    ? String(row.disabled_upstream_status)
                                    : '—'}
                                </InfoField>
                                <InfoField label={page.models.disabledFields.upstreamMessage}>
                                  {row.disabled_upstream_message || '—'}
                                </InfoField>
                                <InfoField label={page.models.disabledFields.at}>
                                  {row.disabled_at ? formatDateTime(row.disabled_at) : '—'}
                                </InfoField>
                              </dl>
                            </div>
                          ) : null}

                          {rowErrors[row.model] ? (
                            <p role="alert" className="mt-2 text-[13px] text-danger">
                              {rowErrors[row.model]}
                            </p>
                          ) : null}

                          <ModelPriceTable row={row} feePercent={feePercent} />
                        </div>
                      )
                    })}
                  </div>
                </>
              )}
            </Section>

            {/* 5. 限制 */}
            <Section
              id="limits"
              title={page.limits.title}
              lead={page.limits.lead}
              failing={failingSection === 'limits'}
            >
              <div className="grid gap-4 sm:grid-cols-2">
                <Field
                  label={page.limits.concurrency}
                  htmlFor="listing-edit-concurrency"
                  hint={page.limits.concurrencyHint}
                  error={concurrencyValid ? undefined : page.limits.invalidNumber}
                >
                  <Input
                    id="listing-edit-concurrency"
                    type="number"
                    min={0}
                    value={concurrencyDraft}
                    invalid={!concurrencyValid}
                    onChange={(event) => setConcurrencyDraft(event.target.value)}
                  />
                </Field>
                <Field
                  label={page.limits.rpm}
                  htmlFor="listing-edit-rpm"
                  hint={page.limits.rpmHint}
                  error={rpmValid ? undefined : page.limits.invalidNumber}
                >
                  <Input
                    id="listing-edit-rpm"
                    type="number"
                    min={0}
                    value={rpmDraft}
                    invalid={!rpmValid}
                    onChange={(event) => setRpmDraft(event.target.value)}
                  />
                </Field>
                <Field
                  label={page.limits.dailyCap}
                  htmlFor="listing-edit-daily-cap"
                  hint={page.limits.dailyCapHint}
                  error={dailyCapValid ? undefined : page.limits.invalidAmount}
                >
                  <Input
                    id="listing-edit-daily-cap"
                    inputMode="decimal"
                    value={dailyCapDraft}
                    invalid={!dailyCapValid}
                    onChange={(event) => setDailyCapDraft(event.target.value)}
                  />
                </Field>
              </div>
              <InfoField label={page.limits.hours}>
                <span>{hoursText(listing.hours)}</span>
                <p className="mt-1 text-[12px] leading-4 text-ink-3">{page.limits.hoursHint}</p>
              </InfoField>
              {sectionErrors.limits ? (
                <p role="alert" className="text-[13px] text-danger">
                  {sectionErrors.limits}
                </p>
              ) : null}
            </Section>

            {/* 6. 验真与巡检 */}
            <Section id="verify" title={page.verify.title} lead={page.verify.lead}>
              <div className="flex flex-wrap gap-2">
                {isListingReverifiable(listing) || listing.status === 'verifying' ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    loading={verify.isPending}
                    disabled={listing.status === 'verifying'}
                    onClick={() => verify.mutate()}
                  >
                    {verify.isPending ? page.verify.rechecking : page.verify.recheck}
                  </Button>
                ) : null}
                {isListingPausable(listing.status) ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    loading={pause.isPending}
                    onClick={() => pause.mutate()}
                  >
                    {page.verify.pause}
                  </Button>
                ) : null}
                {isListingSellerResumable(listing) ? (
                  <Button size="sm" loading={resume.isPending} onClick={() => resume.mutate()}>
                    {page.verify.resume}
                  </Button>
                ) : null}
              </div>

              {listing.status === 'paused' ? (
                <dl className="grid gap-3 sm:grid-cols-2">
                  <InfoField label={page.verify.pauseReason}>
                    <span className="text-warn">
                      {pauseReasonView(listing.pause_reason || 'manual')}
                    </span>
                  </InfoField>
                  <InfoField label={page.verify.resumeAt}>
                    {listing.resume_at ? formatDateTime(listing.resume_at) : '—'}
                  </InfoField>
                </dl>
              ) : (
                <p className="text-[13px] text-ink-3">{page.verify.notPaused}</p>
              )}

              <div>
                {listing.probes.length === 0 ? (
                  <p className="text-[13px] text-ink-3">{page.verify.empty}</p>
                ) : (
                  <Table minWidth={460} size="compact">
                    <TableHead>
                      <TableRow interactive={false}>
                        <TableHeaderCell>{page.verify.columns.time}</TableHeaderCell>
                        <TableHeaderCell>{page.verify.columns.type}</TableHeaderCell>
                        <TableHeaderCell>{page.verify.columns.result}</TableHeaderCell>
                        <TableHeaderCell numeric>{page.verify.columns.latency}</TableHeaderCell>
                        <TableHeaderCell>{page.verify.columns.reason}</TableHeaderCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {listing.probes.map((probe: SellerProbe) => {
                        const latency = historyProbeLatencyMs(probe)
                        const reason = probeFailReasonOf(probe)
                        return (
                          <TableRow key={probe.id} interactive={false}>
                            <TableCell className="whitespace-nowrap text-[13px] text-ink-3">
                              {formatDateTime(probe.created_at)}
                            </TableCell>
                            <TableCell className="text-[13px] text-ink-3">
                              {probeTypeLabel(probe.type)}
                            </TableCell>
                            <TableCell>
                              <Tag tone={probe.ok ? 'brand' : 'danger'} dot>
                                {probe.ok ? page.verify.passed : page.verify.failed}
                              </Tag>
                            </TableCell>
                            <TableCell numeric className="text-[13px] text-ink-3">
                              {latency !== null ? `${latency} ms` : '—'}
                            </TableCell>
                            <TableCell className="text-[13px] text-ink-3">
                              {reason ?? '—'}
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                )}
              </div>
            </Section>

            {/* 7. 高级 */}
            <Section
              id="advanced"
              title={page.advanced.title}
              lead={page.advanced.lead}
              failing={failingSection === 'advanced'}
            >
              <div>
                <h4 className="text-[13px] font-medium text-ink">{page.advanced.groupTitle}</h4>
                {listing.group ? (
                  <>
                    <dl className="mt-2 grid gap-3 sm:grid-cols-2">
                      <InfoField label={page.advanced.groupStrategy}>
                        {page.advanced.groupStrategies[listing.group.key_strategy] ??
                          listing.group.key_strategy}
                      </InfoField>
                      <InfoField label={page.advanced.groupAffinity}>
                        {listing.group.affinity_enabled
                          ? page.advanced.groupAffinityOn
                          : page.advanced.groupAffinityOff}
                      </InfoField>
                      <InfoField label={page.advanced.groupWeight}>
                        {(listing.weight ?? 0) > 0 ? listing.weight : 100}
                      </InfoField>
                    </dl>
                    <div className="mt-3">
                      <p className="mb-2 text-[13px] text-ink-3">
                        {page.advanced.groupSiblings}
                      </p>
                      <Table minWidth={400} size="compact">
                        <TableHead>
                          <TableRow interactive={false}>
                            <TableHeaderCell>
                              {page.advanced.siblingColumns.key}
                            </TableHeaderCell>
                            <TableHeaderCell>
                              {page.advanced.siblingColumns.status}
                            </TableHeaderCell>
                            <TableHeaderCell numeric>
                              {page.advanced.siblingColumns.weight}
                            </TableHeaderCell>
                            <TableHeaderCell>
                              {page.advanced.siblingColumns.multiplier}
                            </TableHeaderCell>
                          </TableRow>
                        </TableHead>
                        <TableBody>
                          {listing.group.listings.map((sibling) => (
                            <TableRow key={sibling.id} interactive={false}>
                              <TableCell className="font-mono text-[13px] text-ink-3">
                                {sibling.key_fingerprint8 || '—'}
                                {sibling.id === listing.id ? (
                                  <Tag tone="brand" className="ml-2">
                                    {page.advanced.currentKey}
                                  </Tag>
                                ) : null}
                              </TableCell>
                              <TableCell>
                                <Tag tone={listingStatusView(sibling.status).tone} dot>
                                  {listingStatusView(sibling.status).label}
                                </Tag>
                              </TableCell>
                              <TableCell numeric className="text-[13px] text-ink-3">
                                {sibling.weight > 0 ? sibling.weight : 100}
                              </TableCell>
                              <TableCell>
                                <Multiplier milli={sibling.multiplier_milli} showSaving={false} />
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  </>
                ) : (
                  <p className="mt-1 text-[13px] text-ink-3">{page.advanced.groupEmpty}</p>
                )}
              </div>

              {headersView?.custom_channel ? (
                <div className="border-t border-line pt-4">
                  <h4 className="text-[13px] font-medium text-ink">
                    {page.advanced.headersTitle}
                  </h4>
                  <p className="mt-1 text-[13px] leading-5 text-ink-3">
                    {page.advanced.headersLead}
                  </p>

                  {headersView.pending ? (
                    <p className="rounded-md border border-warn/30 bg-warn/5 px-3.5 py-2 text-[13px] leading-5 text-ink-2">
                      {page.advanced.pendingHeaders(
                        headersView.pending.version,
                        headersView.pending.submitted_at
                          ? formatDateTime(headersView.pending.submitted_at)
                          : '',
                      )}
                    </p>
                  ) : null}

                  {headersView.headers.length > 0 ? (
                    <ul className="space-y-1">
                      {headersView.headers.map((entry) => (
                        <li
                          key={entry.name}
                          className="flex items-center justify-between gap-3 rounded-md bg-bg-soft px-3.5 py-2 text-[13px]"
                        >
                          <span className="min-w-0 truncate font-mono text-ink">{entry.name}</span>
                          <span className="font-mono text-ink-3">{entry.value_masked}</span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-[13px] text-ink-3">{page.advanced.headersEmpty}</p>
                  )}
                  <p className="text-[12px] leading-4 text-ink-3">
                    {page.advanced.headersMaskedHint}
                  </p>

                  <div className="space-y-2">
                    {headerRows.map((row, index) => (
                      <div key={index} className="flex items-center gap-2">
                        <Input
                          aria-label={page.advanced.headerNameLabel(index + 1)}
                          className="h-9"
                          value={row.name}
                          onChange={(event) =>
                            updateHeaderRow(index, { name: event.target.value })
                          }
                        />
                        <Input
                          aria-label={page.advanced.headerValueLabel(index + 1)}
                          className="h-9"
                          placeholder={page.advanced.headerValuePlaceholder}
                          value={row.value}
                          onChange={(event) =>
                            updateHeaderRow(index, { value: event.target.value })
                          }
                        />
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={page.advanced.removeHeaderLabel(index + 1)}
                          onClick={() => removeHeaderRow(index)}
                        >
                          ×
                        </Button>
                      </div>
                    ))}
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={headerRows.length >= 20}
                        onClick={addHeaderRow}
                      >
                        {page.advanced.addHeader}
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={!headersDirty || headerRowIssues() !== null}
                        onClick={() => void submitHeadersFlow()}
                      >
                        {page.advanced.headersSubmit}
                      </Button>
                    </div>
                    <p className="text-[12px] leading-4 text-ink-3">
                      {page.advanced.headerLimit}
                    </p>
                  </div>

                  {headersDirty && headerRowIssues() ? (
                    <p role="alert" className="text-[13px] text-danger">
                      {headerRowIssues()}
                    </p>
                  ) : null}
                  {sectionErrors.advanced ? (
                    <p role="alert" className="text-[13px] text-danger">
                      {sectionErrors.advanced}
                    </p>
                  ) : null}
                </div>
              ) : null}
            </Section>
          </div>
        </div>
      </Drawer>

      {keyDialogOpen ? (
        <ReplaceKeyDialog
          listingId={listingId}
          open
          onClose={() => setKeyDialogOpen(false)}
        />
      ) : null}

      <Dialog
        open={confirmClear}
        onOpenChange={(next) => {
          if (!next) setConfirmClear(false)
        }}
        title={page.advanced.clearConfirmTitle}
        description={page.advanced.clearConfirmLead}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmClear(false)}>
              {page.advanced.clearCancel}
            </Button>
            <Button loading={clearing} onClick={() => void confirmClearHeaders()}>
              {page.advanced.clearConfirm}
            </Button>
          </>
        }
      />
    </>
  )
}

/** 巡检/验真记录里的失败原因（旧数据可能没有）。 */
function probeFailReasonOf(probe: SellerProbe): string | null {
  if (probe.ok) return null
  const detail = probe.detail as { fail_reason?: string } | null
  const reason =
    detail && typeof detail === 'object' && typeof detail.fail_reason === 'string'
      ? detail.fail_reason
      : ''
  return reason ? probeFailReasonLabel(reason) : null
}
