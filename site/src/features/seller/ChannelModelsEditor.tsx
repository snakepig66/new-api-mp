import { useMemo, useState } from 'react'

import { Button } from '@/ui/button'
import { Dialog } from '@/ui/dialog'
import { Input } from '@/ui/input'
import { Tag } from '@/ui/tag'
import { formatYuan } from '@/lib/money'

import type { PoolModelOption, PoolModelPrice, SellerModelConfig, UpstreamModelOption } from './api'

/**
 * ChannelModelsEditor — the ShareLLM-style model picker shared by the new
 * channel wizard and the edit drawer. A seller either fetches the upstream
 * `/models` list (each name auto-matched to a pool model) or picks straight
 * from the platform model pool, then sets a multiplier per model. A model
 * neither the pool nor the upstream knows cannot be added.
 */

export const DEFAULT_MODEL_MULTIPLIER_MILLI = 1000
const MIN_MULTIPLIER_MILLI = 1
const MAX_MULTIPLIER_MILLI = 1000

export const editorText = {
  fetchUpstream: '从上游获取',
  fetching: '获取中…',
  pickPool: '从模型池选择',
  selectAll: '全选',
  clear: '清空',
  upstreamTitle: '上游模型',
  upstreamEmpty: '上游没有返回模型，可以从模型池选择。',
  upstreamName: '上游名',
  standardModel: '平台模型',
  noPrice: '平台暂无此模型价格',
  unmatched: '未匹配，请手动选择',
  selectedTitle: (n: number) => `已选 ${n} 个模型`,
  selectedEmpty: '还没有选择模型。',
  official: '官方参考价',
  multiplier: '倍率',
  derived: '输入 / 输出 / 缓存读 / 缓存写（元/百万 tokens）',
  remove: '移除',
  poolTitle: '从模型池选择',
  poolSearch: '搜索模型或厂商',
  poolConfirm: (n: number) => `添加 ${n} 个`,
  poolCancel: '取消',
  multiplierError: '倍率需在 0.001–1 之间',
  alreadyListed: '已在渠道中',
  editNotice:
    '新增模型验证通过后才接流量；移除的模型立即停止接新请求，进行中的请求不受影响；价格在请求开始时锁定。',
}

/** Clamp-check a multiplier in milli (1 = 0.001×, 1000 = 1×). */
export function validMultiplierMilli(milli: number | undefined): milli is number {
  return (
    typeof milli === 'number' &&
    Number.isInteger(milli) &&
    milli >= MIN_MULTIPLIER_MILLI &&
    milli <= MAX_MULTIPLIER_MILLI
  )
}

/** Parse the seller's multiplier text ("0.85") into milli, or null when invalid. */
export function parseMultiplierText(text: string): number | null {
  const value = Number(text.trim())
  if (!Number.isFinite(value) || text.trim() === '') return null
  const milli = Math.round(value * 1000)
  return validMultiplierMilli(milli) ? milli : null
}

/** The four unit prices a buyer pays: official × coefficient × multiplier. */
export function derivePrices(
  price: PoolModelPrice,
  multiplierMilli: number,
  coefficientMilli = 1000,
): PoolModelPrice {
  const factor = (multiplierMilli / 1000) * (coefficientMilli / 1000)
  return {
    input_micro_per_mtok: Math.round(price.input_micro_per_mtok * factor),
    output_micro_per_mtok: Math.round(price.output_micro_per_mtok * factor),
    cache_read_micro_per_mtok: Math.round(price.cache_read_micro_per_mtok * factor),
    cache_write_micro_per_mtok: Math.round(price.cache_write_micro_per_mtok * factor),
  }
}

/** Upstream rows a seller may tick: matched to a priced pool model not yet listed. */
export function selectableUpstream(
  upstream: UpstreamModelOption[],
  overrides: Record<string, string>,
  pool: PoolModelOption[],
  excluded: string[] = [],
): { upstream: string; model: string }[] {
  const poolIds = new Set(pool.map((p) => p.model))
  const out: { upstream: string; model: string }[] = []
  const seen = new Set<string>()
  for (const row of upstream) {
    const model = overrides[row.upstream] ?? (row.priced ? row.suggested_model : null)
    if (!model || !poolIds.has(model) || excluded.includes(model) || seen.has(model)) continue
    seen.add(model)
    out.push({ upstream: row.upstream, model })
  }
  return out
}

function priceText(p: PoolModelPrice): string {
  return [
    p.input_micro_per_mtok,
    p.output_micro_per_mtok,
    p.cache_read_micro_per_mtok,
    p.cache_write_micro_per_mtok,
  ]
    .map((v) => formatYuan(v))
    .join(' / ')
}

export interface ChannelModelsEditorProps {
  upstream: UpstreamModelOption[]
  pool: PoolModelOption[]
  value: SellerModelConfig[]
  onChange: (next: SellerModelConfig[]) => void
  /** Price coefficient per standard model (1000 = official). */
  coefficients?: Record<string, number>
  /** Models already on the listing (edit mode); they cannot be added twice. */
  excluded?: string[]
  /** When set, shows the「从上游获取」button. */
  onFetchUpstream?: () => void
  fetching?: boolean
  /** Edit mode shows the add/remove/price-lock notice. */
  showEditNotice?: boolean
}

export function ChannelModelsEditor({
  upstream,
  pool,
  value,
  onChange,
  coefficients,
  excluded = [],
  onFetchUpstream,
  fetching = false,
  showEditNotice = false,
}: ChannelModelsEditorProps) {
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  const [multiplierText, setMultiplierText] = useState<Record<string, string>>({})
  const [poolOpen, setPoolOpen] = useState(false)

  const poolById = useMemo(() => new Map(pool.map((p) => [p.model, p])), [pool])
  const selected = new Set(value.map((c) => c.model))
  const selectable = selectableUpstream(upstream, overrides, pool, excluded)

  function addConfigs(items: { model: string; upstream: string }[]) {
    const next = [...value]
    for (const item of items) {
      if (selected.has(item.model) || excluded.includes(item.model)) continue
      next.push({
        model: item.model,
        upstream_model: item.upstream,
        multiplier_milli: DEFAULT_MODEL_MULTIPLIER_MILLI,
      })
      selected.add(item.model)
    }
    onChange(next)
  }

  function removeModel(model: string) {
    onChange(value.filter((c) => c.model !== model))
  }

  function toggleUpstream(upstreamName: string, model: string, checked: boolean) {
    if (checked) addConfigs([{ model, upstream: upstreamName }])
    else onChange(value.filter((c) => !(c.model === model && c.upstream_model === upstreamName)))
  }

  function setOverride(upstreamName: string, model: string) {
    const previous = value.find((c) => c.upstream_model === upstreamName)
    setOverrides((o) => ({ ...o, [upstreamName]: model }))
    if (previous) {
      onChange(
        value.map((c) =>
          c.upstream_model === upstreamName && model && !value.some((v) => v.model === model)
            ? { ...c, model }
            : c,
        ),
      )
    }
  }

  function setMultiplier(model: string, text: string) {
    setMultiplierText((m) => ({ ...m, [model]: text }))
    const milli = parseMultiplierText(text)
    if (milli === null) return
    onChange(value.map((c) => (c.model === model ? { ...c, multiplier_milli: milli } : c)))
  }

  const poolOptions = pool.map((p) => ({ value: p.model, label: p.model }))

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {onFetchUpstream ? (
          <Button variant="secondary" size="sm" loading={fetching} onClick={onFetchUpstream}>
            {fetching ? editorText.fetching : editorText.fetchUpstream}
          </Button>
        ) : null}
        <Button variant="secondary" size="sm" onClick={() => setPoolOpen(true)} disabled={pool.length === 0}>
          {editorText.pickPool}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={selectable.length === 0}
          onClick={() => addConfigs(selectable)}
        >
          {editorText.selectAll}
        </Button>
        <Button variant="secondary" size="sm" disabled={value.length === 0} onClick={() => onChange([])}>
          {editorText.clear}
        </Button>
      </div>

      {showEditNotice ? (
        <p className="rounded-md bg-bg-soft px-3.5 py-2.5 text-[13px] leading-5 text-ink-2">
          {editorText.editNotice}
        </p>
      ) : null}

      {upstream.length > 0 ? (
        <div className="space-y-2">
          <p className="text-[13px] font-medium text-ink">{editorText.upstreamTitle}</p>
          <ul className="max-h-72 divide-y divide-line overflow-auto rounded-md border border-line">
            {upstream.map((row) => {
              const model = overrides[row.upstream] ?? (row.priced ? row.suggested_model : null) ?? ''
              const listed = model !== '' && excluded.includes(model)
              const usable = model !== '' && poolById.has(model) && !listed
              const checked = value.some((c) => c.upstream_model === row.upstream && c.model === model)
              return (
                <li key={row.upstream} className="flex flex-wrap items-center gap-3 px-3 py-2">
                  <input
                    type="checkbox"
                    aria-label={row.upstream}
                    className="h-4 w-4 accent-brand"
                    disabled={!usable || (!checked && selected.has(model))}
                    checked={checked}
                    onChange={(event) => toggleUpstream(row.upstream, model, event.target.checked)}
                  />
                  <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{row.upstream}</span>
                  <select
                    aria-label={`${row.upstream} ${editorText.standardModel}`}
                    className="h-8 max-w-[14rem] rounded-md bg-bg-soft px-2 text-[13px]"
                    value={model}
                    onChange={(event) => setOverride(row.upstream, event.target.value)}
                  >
                    <option value="">{editorText.unmatched}</option>
                    {poolOptions.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                  {listed ? (
                    <Tag>{editorText.alreadyListed}</Tag>
                  ) : !row.priced && !overrides[row.upstream] ? (
                    <span className="text-[12px] text-ink-3">{editorText.noPrice}</span>
                  ) : null}
                </li>
              )
            })}
          </ul>
        </div>
      ) : onFetchUpstream && !fetching ? (
        <p className="text-[13px] text-ink-3">{editorText.upstreamEmpty}</p>
      ) : null}

      <div className="space-y-2">
        <p className="text-[13px] font-medium text-ink">{editorText.selectedTitle(value.length)}</p>
        {value.length === 0 ? (
          <p className="text-[13px] text-ink-3">{editorText.selectedEmpty}</p>
        ) : (
          <ul className="space-y-2">
            {value.map((config) => {
              const poolRow = poolById.get(config.model)
              const text =
                multiplierText[config.model] ?? (config.multiplier_milli / 1000).toString()
              const invalid = parseMultiplierText(text) === null
              const coefficient = coefficients?.[config.model] ?? 1000
              return (
                <li key={config.model} className="rounded-md border border-line p-3">
                  <div className="flex flex-wrap items-center gap-3">
                    <span className="font-mono text-[13px] text-ink">{config.model}</span>
                    {config.upstream_model && config.upstream_model !== config.model ? (
                      <span className="font-mono text-[12px] text-ink-3">← {config.upstream_model}</span>
                    ) : null}
                    <div className="ml-auto flex items-center gap-2">
                      <label className="text-[13px] text-ink-3" htmlFor={`mult-${config.model}`}>
                        {editorText.multiplier}
                      </label>
                      <Input
                        id={`mult-${config.model}`}
                        className="h-8 w-24"
                        inputMode="decimal"
                        aria-label={`${config.model} ${editorText.multiplier}`}
                        aria-invalid={invalid}
                        value={text}
                        onChange={(event) => setMultiplier(config.model, event.target.value)}
                      />
                      <span className="text-[13px] text-ink-3">×</span>
                      <Button variant="secondary" size="sm" onClick={() => removeModel(config.model)}>
                        {editorText.remove}
                      </Button>
                    </div>
                  </div>
                  {invalid ? (
                    <p className="mt-1 text-[12px] text-danger">{editorText.multiplierError}</p>
                  ) : null}
                  {poolRow ? (
                    <div className="mt-2 space-y-0.5 text-[12px] leading-5 text-ink-3">
                      <p>
                        {editorText.official}：{priceText(poolRow.price)}
                      </p>
                      <p data-testid={`derived-${config.model}`}>
                        {editorText.derived}：
                        <span className="text-ink-2">
                          {priceText(derivePrices(poolRow.price, config.multiplier_milli, coefficient))}
                        </span>
                      </p>
                    </div>
                  ) : null}
                </li>
              )
            })}
          </ul>
        )}
      </div>

      <PoolPickerDialog
        open={poolOpen}
        onOpenChange={setPoolOpen}
        pool={pool}
        disabled={[...excluded, ...value.map((c) => c.model)]}
        onConfirm={(models) => {
          addConfigs(models.map((m) => ({ model: m, upstream: m })))
          setPoolOpen(false)
        }}
      />
    </div>
  )
}

function PoolPickerDialog({
  open,
  onOpenChange,
  pool,
  disabled,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  pool: PoolModelOption[]
  disabled: string[]
  onConfirm: (models: string[]) => void
}) {
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const q = query.trim().toLowerCase()
  const groups = useMemo(() => {
    const map = new Map<string, PoolModelOption[]>()
    for (const p of pool) {
      if (q && !p.model.toLowerCase().includes(q) && !p.vendor.toLowerCase().includes(q)) continue
      const list = map.get(p.vendor) ?? []
      list.push(p)
      map.set(p.vendor, list)
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b))
  }, [pool, q])

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setPicked([])
        onOpenChange(next)
      }}
      title={editorText.poolTitle}
      className="max-w-2xl"
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={() => onOpenChange(false)}>
            {editorText.poolCancel}
          </Button>
          <Button
            size="sm"
            disabled={picked.length === 0}
            onClick={() => {
              onConfirm(picked)
              setPicked([])
            }}
          >
            {editorText.poolConfirm(picked.length)}
          </Button>
        </>
      }
    >
      <Input
        aria-label={editorText.poolSearch}
        placeholder={editorText.poolSearch}
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="mt-3 max-h-80 space-y-3 overflow-auto">
        {groups.map(([vendor, models]) => (
          <div key={vendor}>
            <p className="text-[12px] font-medium uppercase text-ink-3">{vendor}</p>
            <ul className="mt-1 space-y-1">
              {models.map((p) => {
                const off = disabled.includes(p.model)
                return (
                  <li key={p.model} className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-brand"
                      aria-label={`pool ${p.model}`}
                      disabled={off}
                      checked={off || picked.includes(p.model)}
                      onChange={(event) =>
                        setPicked((cur) =>
                          event.target.checked ? [...cur, p.model] : cur.filter((m) => m !== p.model),
                        )
                      }
                    />
                    <span className="font-mono text-[13px]">{p.model}</span>
                    <span className="ml-auto text-[12px] text-ink-3">{priceText(p.price)}</span>
                  </li>
                )
              })}
            </ul>
          </div>
        ))}
      </div>
    </Dialog>
  )
}
