import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  Button,
  buttonClasses,
  Dialog,
  Empty,
  ModelName,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  useToast,
} from '@/ui'

import {
  adminPoolModelsQuery,
  adminPricesQuery,
  createAdminPrice,
  deleteAdminPoolModel,
  putAdminPoolModel,
  type AdminPoolModel,
  type AdminPrice,
} from '@/features/admin/api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  OpsTabNav,
  Select,
  TableSkeleton,
  TextInput,
  type OpsTabLink,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { defaultEffectiveAt, shanghaiDateTimeToRfc3339 } from '@/features/admin/schemas'
import { withLogoVersion } from '@/features/market/model-logo'
import { vendorLabel, vendorLogo } from '@/features/market/vendors'

function formatContext(tokens?: number): string {
  if (!tokens) return '—'
  if (tokens >= 1_000_000) return `${+(tokens / 1_000_000).toFixed(1)}M`
  return `${Math.round(tokens / 1000)}K`
}

export const Route = createFileRoute('/console/ops/models')({
  component: ModelPoolPage,
})

const page = copy.pages.adminPool

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/models', label: '模型池' },
  { to: '/console/ops/plaza', label: '广场上架' },
  { to: '/console/ops/pricing/per-call', label: copy.pages.adminCommon.nav.perCallPrices },
]

const PRESET_VENDORS: { value: string; label: string }[] = [
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'tencent', label: '腾讯 (混元)' },
  { value: 'zhipu', label: '智谱 AI' },
  { value: 'qwen', label: '阿里云千问 (Qwen)' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic (Claude)' },
  { value: 'google', label: 'Google (Gemini)' },
  { value: 'moonshot', label: '月之暗面 (Kimi)' },
  { value: 'minimax', label: 'MiniMax' },
  { value: 'doubao', label: '字节跳动 (豆包)' },
  { value: 'baidu', label: '百度 (文心一言)' },
  { value: 'xai', label: 'xAI (Grok)' },
  { value: '01ai', label: '零一万物 (Yi)' },
]

interface PoolModelForm {
  name: string
  origin_vendor: string
  input_yuan: string
  output_yuan: string
  cache_read_yuan: string
  context_k: string
}

const emptyForm: PoolModelForm = {
  name: '',
  origin_vendor: '',
  input_yuan: '',
  output_yuan: '',
  cache_read_yuan: '',
  context_k: '1000',
}

function ModelPoolPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const models = useQuery(adminPoolModelsQuery())
  const prices = useQuery(adminPricesQuery())

  const [search, setSearch] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<AdminPoolModel | null>(null)
  const [form, setForm] = useState<PoolModelForm>(emptyForm)
  const [errors, setErrors] = useState<Partial<Record<keyof PoolModelForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<AdminPoolModel | null>(null)
  const [customVendorMode, setCustomVendorMode] = useState(false)

  // Map each model name to its latest flat price row
  const latestPriceMap = useMemo(() => {
    const map = new Map<string, AdminPrice>()
    for (const p of prices.data ?? []) {
      const existing = map.get(p.model)
      if (!existing || p.version > existing.version) {
        map.set(p.model, p)
      }
    }
    return map
  }, [prices.data])

  // Filtered rows
  const filteredRows = useMemo(() => {
    let list = models.data ?? []
    if (search.trim()) {
      const q = search.trim().toLowerCase()
      list = list.filter(
        (m) =>
          m.name.toLowerCase().includes(q) ||
          m.origin_vendor.toLowerCase().includes(q),
      )
    }
    return list
  }, [models.data, search])

  // Group filtered rows by vendor, models sorted by name within a group
  const groupedByVendor = useMemo(() => {
    const groups = new Map<string, AdminPoolModel[]>()
    for (const m of filteredRows) {
      const v = m.origin_vendor?.trim() || 'custom'
      const arr = groups.get(v) ?? []
      arr.push(m)
      groups.set(v, arr)
    }
    return Array.from(groups.entries()).map(([vendor, vendorRows]) => ({
      vendor,
      label: vendorLabel(vendor),
      logo: vendorLogo(vendor),
      rows: [...vendorRows].sort((a, b) => a.name.localeCompare(b.name)),
    }))
  }, [filteredRows])

  // Vendor options for the modal select dropdown
  const dialogVendorOptions = useMemo(() => {
    const map = new Map<string, string>()
    for (const v of PRESET_VENDORS) {
      map.set(v.value, v.label)
    }
    for (const m of models.data ?? []) {
      const v = m.origin_vendor?.trim()
      if (v && !map.has(v)) {
        map.set(v, `${vendorLabel(v)} (${v})`)
      }
    }
    return Array.from(map.entries()).map(([value, label]) => ({ value, label }))
  }, [models.data])

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: adminPoolModelsQuery().queryKey })
    void queryClient.invalidateQueries({ queryKey: adminPricesQuery().queryKey })
  }

  const remove = useMutation({
    mutationFn: (name: string) => deleteAdminPoolModel(name),
    onSuccess: () => {
      refresh()
      toast.show({ title: page.models.deleteSuccess, tone: 'success' })
      setDeleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function openCreate() {
    setEditing(null)
    setForm(emptyForm)
    setCustomVendorMode(false)
    setErrors({})
    setSubmitError(null)
    setDialogOpen(true)
  }

  function openEdit(model: AdminPoolModel) {
    const p = latestPriceMap.get(model.name)
    const known = dialogVendorOptions.some((o) => o.value === model.origin_vendor)
    setCustomVendorMode(Boolean(model.origin_vendor && !known))
    setEditing(model)
    setForm({
      name: model.name,
      origin_vendor: model.origin_vendor,
      input_yuan: p ? String(Number(p.input_micro_per_mtok) / 1_000_000) : '',
      output_yuan: p ? String(Number(p.output_micro_per_mtok) / 1_000_000) : '',
      cache_read_yuan:
        p && p.cache_read_micro_per_mtok ? String(Number(p.cache_read_micro_per_mtok) / 1_000_000) : '',
      context_k: model.context_length ? String(model.context_length / 1000) : '',
    })
    setErrors({})
    setSubmitError(null)
    setDialogOpen(true)
  }

  function update<K extends keyof PoolModelForm>(key: K, value: PoolModelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  async function handleSubmit() {
    const next: Partial<Record<keyof PoolModelForm, string>> = {}
    const name = form.name.trim()
    const origin = form.origin_vendor.trim()

    if (!name) next.name = '模型名称不能为空'
    else if (name.length > 128) next.name = '模型名称过长'

    const hasInputPrice = form.input_yuan.trim().length > 0
    const hasOutputPrice = form.output_yuan.trim().length > 0

    if (hasInputPrice) {
      const val = Number(form.input_yuan)
      if (Number.isNaN(val) || val <= 0) next.input_yuan = '请输入大于 0 的数字'
    }
    if (hasOutputPrice) {
      const val = Number(form.output_yuan)
      if (Number.isNaN(val) || val <= 0) next.output_yuan = '请输入大于 0 的数字'
    }

    const contextRaw = form.context_k.trim()
    if (contextRaw) {
      const val = Number(contextRaw)
      if (Number.isNaN(val) || val <= 0) next.context_k = '请输入大于 0 的数字'
    }

    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }

    setIsSubmitting(true)
    setSubmitError(null)

    try {
      // 1. Write the price book entry first: enabling the plaza is validated
      // against the model's base price, so a new model saved before its price
      // exists would be rejected with 「请先配置模型池基价再上架广场」.
      const base = editing
      if (hasInputPrice && hasOutputPrice) {
        const inputMicro = Math.round(Number(form.input_yuan) * 1_000_000)
        const outputMicro = Math.round(Number(form.output_yuan) * 1_000_000)
        const cacheReadMicro = form.cache_read_yuan ? Math.round(Number(form.cache_read_yuan) * 1_000_000) : 0

        await createAdminPrice({
          model: name,
          vendor: origin || base?.origin_vendor || 'custom',
          tier: 'flat',
          input_micro_per_mtok: inputMicro,
          output_micro_per_mtok: outputMicro,
          cache_read_micro_per_mtok: cacheReadMicro,
          cache_write_micro_per_mtok: 0,
          effective_at: shanghaiDateTimeToRfc3339(defaultEffectiveAt()) ?? new Date().toISOString(),
        })
      }

      // 2. Save model attributes, carrying over untouched fields from the
      // edited model (or the original form defaults when creating).
      await putAdminPoolModel({
        name,
        origin_vendor: origin || base?.origin_vendor || '',
        context_length: contextRaw ? Math.round(Number(contextRaw) * 1000) : 0,
        capabilities: base?.capabilities ?? [],
        aliases: base?.aliases ?? [],
        verify_prompt: base?.verify_prompt ?? '',
        status: base?.status ?? 'active',
        enable_plaza: base ? base.enable_plaza !== false : true,
        plaza_multiplier_milli: base?.plaza_multiplier_milli ?? 1000,
      })

      refresh()
      toast.show({
        title: editing ? '模型与基价修改成功' : '模型与基价创建成功',
        tone: 'success',
      })
      setDialogOpen(false)
    } catch (err) {
      setSubmitError(adminErrorMessage(err))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
      <PageHeader
        title="模型池"
        lead="各厂家模型的基价（元 / 1M tokens），广场价 = 基价 × 广场倍率"
        actions={
          <div className="flex items-center gap-2">
            <a href="/console/ops/channels" className={buttonClasses('secondary', 'sm')}>
              广场渠道
            </a>
            <Button size="sm" onClick={openCreate}>
              + 添加模型
            </Button>
          </div>
        }
      />

      <div className="mt-6 space-y-6">
        {/* 搜索条 */}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="w-full sm:w-72">
            <TextInput
              placeholder="搜索模型 ID、厂商..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <div className="flex items-center gap-2 text-meta text-ink-3">
            <span>
              共 <strong className="font-medium text-ink">{filteredRows.length}</strong> 个模型
            </span>
          </div>
        </div>

        {/* 模型列表区块 */}
        {models.isPending || prices.isPending ? (
          <TableSkeleton rows={8} columns={5} />
        ) : models.isError ? (
          <ErrorBlock error={models.error} onRetry={refresh} />
        ) : filteredRows.length === 0 ? (
          <Empty
            title="暂无模型"
            reason={search ? '没有匹配筛选条件的模型' : '标准模型池当前为空'}
            next="点击右上角按钮添加模型及基价，或调整搜索条件"
            action={
              <Button variant="secondary" size="sm" onClick={openCreate}>
                + 添加模型
              </Button>
            }
          />
        ) : (
          <div className="space-y-6">
            {groupedByVendor.map((group) => (
              <div
                key={group.vendor}
                className="overflow-hidden rounded-xl border border-line bg-card/60 shadow-xs backdrop-blur-sm"
              >
                {/* 厂商头部 */}
                <div className="flex items-center justify-between border-b border-line bg-surface/50 px-4 py-3">
                  <div className="flex items-center gap-2.5">
                    {group.logo ? (
                      <img
                        src={withLogoVersion(group.logo)}
                        alt=""
                        className="h-5 w-5 shrink-0 object-contain"
                        aria-hidden="true"
                      />
                    ) : (
                      <span className="flex h-5 w-5 items-center justify-center rounded-full bg-brand-soft text-[10px] font-bold text-brand-ink uppercase">
                        {group.label.slice(0, 1)}
                      </span>
                    )}
                    <h3 className="text-body font-semibold text-ink">{group.label}</h3>
                    <span className="rounded-full bg-line/80 px-2 py-0.5 text-caption font-medium text-ink-3">
                      {group.rows.length} 个模型
                    </span>
                  </div>
                </div>

                <Table minWidth={720}>
                  <TableHead>
                    <TableRow interactive={false}>
                      <TableHeaderCell className="w-[30%]">模型 ID</TableHeaderCell>
                      <TableHeaderCell className="w-[10%] text-right">上下文</TableHeaderCell>
                      <TableHeaderCell className="w-[16%] text-right">输入基价</TableHeaderCell>
                      <TableHeaderCell className="w-[16%] text-right">输出基价</TableHeaderCell>
                      <TableHeaderCell className="w-[16%] text-right">缓存命中基价</TableHeaderCell>
                      <TableHeaderCell numeric className="w-[12%]">操作</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {group.rows.map((row) => {
                      const p = latestPriceMap.get(row.name)
                      return (
                        <TableRow key={row.name}>
                          <TableCell>
                            <ModelName
                              model={row.name}
                              vendor={row.origin_vendor || group.vendor}
                              size="md"
                              className="font-semibold text-ink"
                            />
                          </TableCell>
                          <TableCell className="text-right font-mono text-meta text-ink-2">
                            {formatContext(row.context_length)}
                          </TableCell>
                          <TableCell className="text-right font-mono text-meta text-brand">
                            {p ? formatYuan(p.input_micro_per_mtok) : '—'}
                          </TableCell>
                          <TableCell className="text-right font-mono text-meta text-ink">
                            {p ? formatYuan(p.output_micro_per_mtok) : '—'}
                          </TableCell>
                          <TableCell className="text-right font-mono text-meta text-ink-2">
                            {p && p.cache_read_micro_per_mtok
                              ? formatYuan(p.cache_read_micro_per_mtok)
                              : '—'}
                          </TableCell>
                          <TableCell numeric>
                            <div className="flex items-center justify-end gap-1.5 whitespace-nowrap">
                              <Button variant="secondary" size="sm" onClick={() => openEdit(row)}>
                                编辑
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => setDeleteTarget(row)}
                                className="text-danger hover:bg-danger/10"
                              >
                                删除
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* 新增 / 编辑模型与基价弹窗 */}
      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!open) {
            setDialogOpen(false)
            setSubmitError(null)
          }
        }}
        title={editing ? `编辑模型：${editing.name}` : '添加模型'}
        description="维护模型所属厂家与每 1M tokens 的基价。别名、能力、上下文、倍率与上架状态保持不变。"
        className="max-h-[calc(100vh-3rem)] max-w-2xl overflow-y-auto"
        footer={
          <>
            <Button
              variant="secondary"
              disabled={isSubmitting}
              onClick={() => setDialogOpen(false)}
            >
              取消
            </Button>
            <Button loading={isSubmitting} onClick={handleSubmit}>
              {isSubmitting ? '正在保存...' : '保存模型'}
            </Button>
          </>
        }
      >
        <div className="space-y-6">
          <div>
            <h4 className="text-meta font-semibold text-ink">模型基本信息</h4>
            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              <Field
                label="模型唯一标识 (ID)"
                htmlFor="pool-name"
                error={errors.name}
                hint="例如 deepseek-chat 或 gpt-4o"
              >
                <TextInput
                  id="pool-name"
                  value={form.name}
                  disabled={editing !== null}
                  placeholder="deepseek-chat"
                  invalid={Boolean(errors.name)}
                  onChange={(e) => update('name', e.target.value)}
                />
              </Field>

              <Field
                label="厂商标识 (Vendor)"
                htmlFor="pool-vendor"
                hint="选择或自定义厂商，用于分组与品牌图标"
              >
                <Select
                  id="pool-vendor"
                  value={
                    customVendorMode
                      ? '__custom__'
                      : dialogVendorOptions.some((o) => o.value === form.origin_vendor)
                        ? form.origin_vendor
                        : ''
                  }
                  onChange={(e) => {
                    const val = e.target.value
                    if (val === '__custom__') {
                      setCustomVendorMode(true)
                      update('origin_vendor', '')
                    } else {
                      setCustomVendorMode(false)
                      update('origin_vendor', val)
                    }
                  }}
                >
                  <option value="" disabled>
                    请选择厂商...
                  </option>
                  {dialogVendorOptions.map((v) => (
                    <option key={v.value} value={v.value}>
                      {v.label} ({v.value})
                    </option>
                  ))}
                  <option value="__custom__">其他厂商（手动输入）...</option>
                </Select>
                {customVendorMode && (
                  <div className="mt-2">
                    <TextInput
                      placeholder="输入自定义厂商标识 slug，例如 mistral, groq"
                      value={form.origin_vendor}
                      onChange={(e) => update('origin_vendor', e.target.value.trim().toLowerCase())}
                      autoFocus
                    />
                  </div>
                )}
              </Field>

              <Field
                label="上下文长度 (K tokens)"
                htmlFor="pool-context"
                error={errors.context_k}
                hint="默认 1000（即 1M）；128 表示 128K"
              >
                <TextInput
                  id="pool-context"
                  value={form.context_k}
                  placeholder="128"
                  inputMode="decimal"
                  invalid={Boolean(errors.context_k)}
                  onChange={(e) => update('context_k', e.target.value)}
                />
              </Field>
            </div>
          </div>

          <hr className="border-line" />

          <div>
            <h4 className="text-meta font-semibold text-ink">基价（每 1M tokens）</h4>
            <p className="mt-1 text-caption text-ink-3">
              填入输入与输出每 1,000,000 tokens 的人民币价格。留空表示沿用历史版本或暂不设定。
            </p>

            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              <Field
                label="输入单价 (元 / 1M tokens)"
                htmlFor="pool-input-price"
                error={errors.input_yuan}
              >
                <TextInput
                  id="pool-input-price"
                  value={form.input_yuan}
                  placeholder="2.00"
                  inputMode="decimal"
                  invalid={Boolean(errors.input_yuan)}
                  onChange={(e) => update('input_yuan', e.target.value)}
                />
              </Field>

              <Field
                label="输出单价 (元 / 1M tokens)"
                htmlFor="pool-output-price"
                error={errors.output_yuan}
              >
                <TextInput
                  id="pool-output-price"
                  value={form.output_yuan}
                  placeholder="8.00"
                  inputMode="decimal"
                  invalid={Boolean(errors.output_yuan)}
                  onChange={(e) => update('output_yuan', e.target.value)}
                />
              </Field>

              <Field
                label="缓存命中单价 (元 / 1M tokens，可选)"
                htmlFor="pool-cache-read"
                className="sm:col-span-2"
                hint="仅当模型支持上下文缓存且命中时享受优惠价时填写；若不支持或无优惠可留空"
              >
                <TextInput
                  id="pool-cache-read"
                  value={form.cache_read_yuan}
                  placeholder="0.50（留空表示无独立缓存命中价格）"
                  inputMode="decimal"
                  onChange={(e) => update('cache_read_yuan', e.target.value)}
                />
              </Field>
            </div>
          </div>

          {submitError ? (
            <p
              role="alert"
              className="border border-danger/25 bg-danger/10 px-3 py-2 text-meta text-danger"
            >
              {submitError}
            </p>
          ) : null}
        </div>
      </Dialog>

      <ConfirmDialog
        open={deleteTarget !== null}
        title={deleteTarget ? `确定删除模型 ${deleteTarget.name}？` : ''}
        description="删除标准模型后，相关配置将立即移除。若有正在售卖该模型的渠道，需要先下架该渠道。"
        confirmLabel="确认删除"
        loading={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.name)
        }}
        onClose={() => setDeleteTarget(null)}
      />
    </>
  )
}
