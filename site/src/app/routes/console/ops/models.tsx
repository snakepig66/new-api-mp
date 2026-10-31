import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import {
  Button,
  Dialog,
  DropdownMenu,
  Empty,
  ModelName,
  PageHeader,
  Segmented,
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

export const Route = createFileRoute('/console/ops/models')({
  component: ModelsAndPricingPage,
})

const page = copy.pages.adminPool

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/models', label: '模型与定价' },
  { to: '/console/ops/pricing/per-call', label: copy.pages.adminCommon.nav.perCallPrices },
]

const CAPABILITY_LABELS: Record<string, string> = {
  text: '文本',
  chat: '对话',
  tools: '工具',
  cache: '缓存',
  vision: '视觉',
  image: '图像',
  embedding: '向量',
  reasoning: '推理',
  search: '搜索',
}

type StatusFilter = 'all' | 'active' | 'retired' | 'unpriced'

const statusFilterOptions = [
  { value: 'all', label: '全部模型' },
  { value: 'active', label: '仅在售' },
  { value: 'retired', label: '已停用' },
  { value: 'unpriced', label: '未配置定价' },
]

function splitList(value: string): string[] {
  return value
    .split(/[,，\n]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function parseNonNegativeInt(value: string): number | null {
  if (!value.trim()) return 0
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < 0) return null
  return parsed
}

interface UnifiedModelForm {
  name: string
  origin_vendor: string
  context_length: string
  capabilities: string
  aliases: string
  verify_prompt: string
  status: string
  // Pricing
  input_yuan: string
  output_yuan: string
  cache_read_yuan: string
  cache_write_yuan: string
}

const emptyForm: UnifiedModelForm = {
  name: '',
  origin_vendor: '',
  context_length: '',
  capabilities: '',
  aliases: '',
  verify_prompt: '',
  status: 'active',
  input_yuan: '',
  output_yuan: '',
  cache_read_yuan: '',
  cache_write_yuan: '',
}

function ModelsAndPricingPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const models = useQuery(adminPoolModelsQuery())
  const prices = useQuery(adminPricesQuery())

  const [search, setSearch] = useState('')
  const [selectedVendor, setSelectedVendor] = useState('all')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState<UnifiedModelForm>(emptyForm)
  const [errors, setErrors] = useState<Partial<Record<keyof UnifiedModelForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<AdminPoolModel | null>(null)

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

  // Aggregate vendor metrics for vendor filter pills
  const vendorList = useMemo(() => {
    const list = models.data ?? []
    const counts = new Map<string, number>()
    for (const m of list) {
      const v = m.origin_vendor?.trim() || 'custom'
      counts.set(v, (counts.get(v) ?? 0) + 1)
    }
    return Array.from(counts.entries())
      .map(([vendor, count]) => ({
        vendor,
        label: vendorLabel(vendor),
        logo: vendorLogo(vendor),
        count,
      }))
      .sort((a, b) => b.count - a.count)
  }, [models.data])

  // Filtered rows
  const filteredRows = useMemo(() => {
    let list = models.data ?? []
    if (search.trim()) {
      const q = search.trim().toLowerCase()
      list = list.filter(
        (m) =>
          m.name.toLowerCase().includes(q) ||
          m.origin_vendor.toLowerCase().includes(q) ||
          m.aliases.some((a) => a.toLowerCase().includes(q)),
      )
    }
    if (selectedVendor !== 'all') {
      list = list.filter((m) => (m.origin_vendor?.trim() || 'custom') === selectedVendor)
    }
    if (statusFilter === 'active') {
      list = list.filter((m) => m.status === 'active')
    } else if (statusFilter === 'retired') {
      list = list.filter((m) => m.status === 'retired')
    } else if (statusFilter === 'unpriced') {
      list = list.filter((m) => !latestPriceMap.has(m.name))
    }
    return list
  }, [models.data, search, selectedVendor, statusFilter, latestPriceMap])

  // Group filtered rows by vendor
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
      rows: vendorRows,
    }))
  }, [filteredRows])

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

  const toggleStatus = useMutation({
    mutationFn: (model: AdminPoolModel) =>
      putAdminPoolModel({
        name: model.name,
        origin_vendor: model.origin_vendor,
        context_length: model.context_length,
        capabilities: model.capabilities,
        aliases: model.aliases,
        verify_prompt: model.verify_prompt,
        status: model.status === 'active' ? 'retired' : 'active',
      }),
    onSuccess: () => {
      refresh()
      toast.show({ title: '模型状态已更新', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function openCreate() {
    setEditing(null)
    setForm(emptyForm)
    setErrors({})
    setSubmitError(null)
    setDialogOpen(true)
  }

  function openEdit(model: AdminPoolModel) {
    const p = latestPriceMap.get(model.name)
    setEditing(model.name)
    setForm({
      name: model.name,
      origin_vendor: model.origin_vendor,
      context_length: model.context_length ? String(model.context_length) : '',
      capabilities: model.capabilities.join(', '),
      aliases: model.aliases.join(', '),
      verify_prompt: model.verify_prompt,
      status: model.status || 'active',
      input_yuan: p ? String(Number(p.input_micro_per_mtok) / 1_000_000) : '',
      output_yuan: p ? String(Number(p.output_micro_per_mtok) / 1_000_000) : '',
      cache_read_yuan: p && p.cache_read_micro_per_mtok ? String(Number(p.cache_read_micro_per_mtok) / 1_000_000) : '',
      cache_write_yuan: p && p.cache_write_micro_per_mtok ? String(Number(p.cache_write_micro_per_mtok) / 1_000_000) : '',
    })
    setErrors({})
    setSubmitError(null)
    setDialogOpen(true)
  }

  function update<K extends keyof UnifiedModelForm>(key: K, value: UnifiedModelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  async function handleSubmit() {
    const next: Partial<Record<keyof UnifiedModelForm, string>> = {}
    const name = form.name.trim()
    const origin = form.origin_vendor.trim()
    const context = parseNonNegativeInt(form.context_length)

    if (!name) next.name = '模型名称不能为空'
    else if (name.length > 128) next.name = '模型名称过长'
    if (context === null) next.context_length = '上下文长度必须为非负整数'

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

    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }

    setIsSubmitting(true)
    setSubmitError(null)

    try {
      // 1. Save model attributes
      await putAdminPoolModel({
        name,
        origin_vendor: origin,
        context_length: context ?? 0,
        capabilities: splitList(form.capabilities),
        aliases: splitList(form.aliases),
        verify_prompt: form.verify_prompt.trim(),
        status: form.status,
      })

      // 2. Save price if specified
      if (hasInputPrice && hasOutputPrice) {
        const inputMicro = Math.round(Number(form.input_yuan) * 1_000_000)
        const outputMicro = Math.round(Number(form.output_yuan) * 1_000_000)
        const cacheReadMicro = form.cache_read_yuan ? Math.round(Number(form.cache_read_yuan) * 1_000_000) : 0
        const cacheWriteMicro = form.cache_write_yuan ? Math.round(Number(form.cache_write_yuan) * 1_000_000) : 0

        await createAdminPrice({
          model: name,
          vendor: origin || 'custom',
          tier: 'flat',
          input_micro_per_mtok: inputMicro,
          output_micro_per_mtok: outputMicro,
          cache_read_micro_per_mtok: cacheReadMicro,
          cache_write_micro_per_mtok: cacheWriteMicro,
          effective_at: shanghaiDateTimeToRfc3339(defaultEffectiveAt()) ?? new Date().toISOString(),
        })
      }

      refresh()
      toast.show({
        title: editing ? '模型与定价修改成功' : '模型与定价创建成功',
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
        title="模型与定价"
        lead="集中管理标准在售模型池及其 Token 计费价格。配置后即时同步至模型广场与调用计费链路。"
        actions={
          <Button size="sm" onClick={openCreate}>
            + 添加模型与定价
          </Button>
        }
      />

      <div className="mt-6 space-y-6">
        {/* 厂商药丸过滤栏 */}
        {vendorList.length > 0 ? (
          <div className="flex flex-wrap items-center gap-2 border-b border-line pb-4">
            <button
              type="button"
              onClick={() => setSelectedVendor('all')}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-caption font-medium transition-colors cursor-pointer',
                selectedVendor === 'all'
                  ? 'bg-brand text-white shadow-xs'
                  : 'bg-card text-ink-2 ring-1 ring-inset ring-line hover:bg-brand-soft/50 hover:text-ink',
              )}
            >
              <span>全部厂商</span>
              <span
                className={cn(
                  'rounded-full px-1.5 py-0.2 text-[11px]',
                  selectedVendor === 'all' ? 'bg-white/20 text-white' : 'bg-line text-ink-3',
                )}
              >
                {models.data?.length ?? 0}
              </span>
            </button>
            {vendorList.map((v) => {
              const active = selectedVendor === v.vendor
              return (
                <button
                  key={v.vendor}
                  type="button"
                  onClick={() => setSelectedVendor(v.vendor)}
                  className={cn(
                    'inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-caption font-medium transition-colors cursor-pointer',
                    active
                      ? 'bg-brand text-white shadow-xs'
                      : 'bg-card text-ink-2 ring-1 ring-inset ring-line hover:bg-brand-soft/50 hover:text-ink',
                  )}
                >
                  {v.logo ? (
                    <img
                      src={withLogoVersion(v.logo)}
                      alt=""
                      className="h-3.5 w-3.5 object-contain"
                      aria-hidden="true"
                    />
                  ) : null}
                  <span>{v.label}</span>
                  <span
                    className={cn(
                      'rounded-full px-1.5 py-0.2 text-[11px]',
                      active ? 'bg-white/20 text-white' : 'bg-line text-ink-3',
                    )}
                  >
                    {v.count}
                  </span>
                </button>
              )
            })}
          </div>
        ) : null}

        {/* 搜索与快捷状态过滤条 */}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-3">
            <div className="w-full sm:w-72">
              <TextInput
                placeholder="搜索模型名称、厂商、别名..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <Segmented
              options={statusFilterOptions}
              value={statusFilter}
              onChange={(v) => setStatusFilter(v as StatusFilter)}
              size="sm"
              ariaLabel="模型状态筛选"
            />
          </div>
          <div className="flex items-center gap-2 text-meta text-ink-3">
            <span>
              共 <strong className="font-medium text-ink">{filteredRows.length}</strong> 个模型
            </span>
            <span>·</span>
            <span>
              已配置定价 <strong className="font-medium text-ink">{latestPriceMap.size}</strong>
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
            reason={
              search || selectedVendor !== 'all' || statusFilter !== 'all'
                ? '没有匹配筛选条件的模型'
                : '标准模型池当前为空'
            }
            next="点击右上角按钮添加模型及定价，或调整筛选条件"
            action={
              <Button variant="secondary" size="sm" onClick={openCreate}>
                + 添加模型与定价
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
                    <h3 className="text-body font-semibold text-ink">
                      {group.label}
                    </h3>
                    <span className="rounded-full bg-line/80 px-2 py-0.5 text-caption font-medium text-ink-3">
                      {group.rows.length} 个模型
                    </span>
                  </div>
                </div>

                {/* 规范高密度数据表格 */}
                <Table minWidth={760}>
                  <TableHead>
                    <TableRow interactive={false}>
                      <TableHeaderCell className="w-[34%]">模型名称 / 能力</TableHeaderCell>
                      <TableHeaderCell className="w-[12%] text-center">上下文</TableHeaderCell>
                      <TableHeaderCell className="w-[32%]">Token 计费 (元/M tok)</TableHeaderCell>
                      <TableHeaderCell className="w-[10%]">状态</TableHeaderCell>
                      <TableHeaderCell numeric className="w-[12%]">操作</TableHeaderCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {group.rows.map((row) => {
                      const p = latestPriceMap.get(row.name)
                      const hasPrice = Boolean(p)
                      const isThisToggling =
                        toggleStatus.isPending && toggleStatus.variables?.name === row.name

                      return (
                        <TableRow key={row.name}>
                          {/* 1. 模型名称 + 别名 + 能力标签 */}
                          <TableCell>
                            <div className="flex flex-col gap-1.5 py-1">
                              <div className="flex items-center gap-2">
                                <ModelName
                                  model={row.name}
                                  size="md"
                                  className="font-semibold text-ink"
                                />
                                {row.status !== 'active' ? (
                                  <span className="rounded bg-line/60 px-1.5 py-0.5 text-[11px] text-ink-4">
                                    已停用
                                  </span>
                                ) : null}
                              </div>
                              <div className="flex flex-wrap items-center gap-1.5">
                                {row.aliases.length > 0 ? (
                                  <span className="text-caption text-ink-3">
                                    别名: {row.aliases.join(', ')}
                                  </span>
                                ) : null}
                                {row.capabilities && row.capabilities.length > 0 ? (
                                  <div className="flex flex-wrap items-center gap-1">
                                    {row.capabilities.map((cap) => (
                                      <Tag
                                        key={cap}
                                        tone="neutral"
                                        className="h-[18px] px-1.5 text-[11px] leading-none text-ink-3"
                                      >
                                        {CAPABILITY_LABELS[cap] ?? cap}
                                      </Tag>
                                    ))}
                                  </div>
                                ) : null}
                              </div>
                            </div>
                          </TableCell>

                          {/* 2. 上下文 */}
                          <TableCell className="text-center">
                            {row.context_length ? (
                              <span className="inline-block rounded-md bg-line/50 px-2 py-0.5 font-mono text-caption font-medium text-ink-2">
                                {Math.round(row.context_length / 1024)}k
                              </span>
                            ) : (
                              <span className="text-ink-4">—</span>
                            )}
                          </TableCell>

                          {/* 3. Token 复合计费 */}
                          <TableCell>
                            {hasPrice && p ? (
                              <div className="flex flex-col gap-0.5 py-0.5">
                                <div className="flex items-center gap-2 font-mono text-meta">
                                  <span className="inline-flex items-center gap-1">
                                    <span className="text-caption text-ink-3">输入</span>
                                    <span className="font-semibold text-brand">
                                      {formatYuan(p.input_micro_per_mtok)}
                                    </span>
                                  </span>
                                  <span className="text-ink-4">/</span>
                                  <span className="inline-flex items-center gap-1">
                                    <span className="text-caption text-ink-3">输出</span>
                                    <span className="font-semibold text-ink">
                                      {formatYuan(p.output_micro_per_mtok)}
                                    </span>
                                  </span>
                                </div>
                                <div className="flex items-center gap-1.5 font-mono text-caption text-ink-3">
                                  {p.cache_read_micro_per_mtok || p.cache_write_micro_per_mtok ? (
                                    <span>
                                      缓存 读{' '}
                                      <span className="text-ink-2">
                                        {formatYuan(p.cache_read_micro_per_mtok)}
                                      </span>{' '}
                                      / 写{' '}
                                      <span className="text-ink-2">
                                        {formatYuan(p.cache_write_micro_per_mtok)}
                                      </span>
                                    </span>
                                  ) : (
                                    <span className="text-ink-4">无独立缓存费率</span>
                                  )}
                                </div>
                              </div>
                            ) : (
                              <Tag tone="warning" className="whitespace-nowrap">
                                未配置定价
                              </Tag>
                            )}
                          </TableCell>

                          {/* 4. 状态 Switch */}
                          <TableCell>
                            <div className="flex items-center gap-2 whitespace-nowrap">
                              <Switch
                                checked={row.status === 'active'}
                                loading={isThisToggling}
                                onCheckedChange={() => toggleStatus.mutate(row)}
                                aria-label={row.status === 'active' ? '点击停用' : '点击启用'}
                              />
                              <span
                                className={cn(
                                  'text-caption font-medium',
                                  row.status === 'active' ? 'text-brand-ink' : 'text-ink-4',
                                )}
                              >
                                {row.status === 'active' ? '在售' : '停用'}
                              </span>
                            </div>
                          </TableCell>

                          {/* 5. 精简操作列 */}
                          <TableCell numeric>
                            <div className="flex items-center justify-end gap-1.5 whitespace-nowrap">
                              <Button
                                variant="secondary"
                                size="sm"
                                onClick={() => openEdit(row)}
                              >
                                编辑
                              </Button>
                              <DropdownMenu
                                align="end"
                                menuLabel="更多操作"
                                trigger={
                                  <Button
                                    variant="ghost"
                                    size="sm"
                                    aria-label="更多操作"
                                    className="px-2 font-mono text-base text-ink-3 hover:text-ink"
                                  >
                                    ⋯
                                  </Button>
                                }
                                items={[
                                  {
                                    key: 'delete',
                                    label: '删除模型',
                                    danger: true,
                                    onSelect: () => setDeleteTarget(row),
                                  },
                                ]}
                              />
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

      {/* 一体化新增 / 编辑模型与定价弹窗 */}
      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!open) {
            setDialogOpen(false)
            setSubmitError(null)
          }
        }}
        title={editing ? `编辑模型与定价：${editing}` : '添加模型与定价'}
        description="配置标准在售模型的基本属性以及全局基准 Token 价格。保存后将自动应用至模型广场。"
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
              {isSubmitting ? '正在保存...' : '保存模型与定价'}
            </Button>
          </>
        }
      >
        <div className="space-y-6">
          {/* Section 1: 基础属性 */}
          <div>
            <h4 className="text-meta font-semibold text-ink">1. 模型基本信息</h4>
            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              <Field
                label="模型唯一标识 (ID)"
                htmlFor="unified-name"
                error={errors.name}
                hint="例如 deepseek-chat 或 gpt-4o"
              >
                <TextInput
                  id="unified-name"
                  value={form.name}
                  disabled={editing !== null}
                  placeholder="deepseek-chat"
                  invalid={Boolean(errors.name)}
                  onChange={(e) => update('name', e.target.value)}
                />
              </Field>

              <Field
                label="厂商标识 (Vendor)"
                htmlFor="unified-vendor"
                hint="用于渲染厂商矢量图标与分类"
              >
                <TextInput
                  id="unified-vendor"
                  value={form.origin_vendor}
                  placeholder="deepseek / openai / anthropic"
                  onChange={(e) => update('origin_vendor', e.target.value)}
                />
              </Field>

              <Field
                label="上下文窗口 (Tokens)"
                htmlFor="unified-context"
                error={errors.context_length}
              >
                <TextInput
                  id="unified-context"
                  value={form.context_length}
                  placeholder="65536"
                  inputMode="numeric"
                  invalid={Boolean(errors.context_length)}
                  onChange={(e) => update('context_length', e.target.value)}
                />
              </Field>

              <Field label="模型状态" htmlFor="unified-status">
                <Select
                  id="unified-status"
                  value={form.status}
                  onChange={(e) => update('status', e.target.value)}
                >
                  <option value="active">正常在售 (Active)</option>
                  <option value="retired">已停用 (Retired)</option>
                </Select>
              </Field>

              <Field
                label="模型别名 (逗号分隔)"
                htmlFor="unified-aliases"
                className="sm:col-span-2"
              >
                <TextInput
                  id="unified-aliases"
                  value={form.aliases}
                  placeholder="deepseek-v3, deepseek"
                  onChange={(e) => update('aliases', e.target.value)}
                />
              </Field>

              <Field
                label="功能能力 / 标签"
                htmlFor="unified-caps"
                className="sm:col-span-2"
              >
                <TextInput
                  id="unified-caps"
                  value={form.capabilities}
                  placeholder="chat, tools, vision"
                  onChange={(e) => update('capabilities', e.target.value)}
                />
              </Field>
            </div>
          </div>

          <hr className="border-line" />

          {/* Section 2: 定价策略 */}
          <div>
            <h4 className="text-meta font-semibold text-ink">2. Token 计费价格（每 1M tokens）</h4>
            <p className="mt-1 text-caption text-ink-3">
              填入输入与输出每 1,000,000 tokens 的人民币价格。留空表示沿用历史版本或暂不设定。
            </p>

            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              <Field
                label="输入单价 (元 / 1M tokens)"
                htmlFor="unified-input-price"
                error={errors.input_yuan}
              >
                <TextInput
                  id="unified-input-price"
                  value={form.input_yuan}
                  placeholder="2.00"
                  inputMode="decimal"
                  invalid={Boolean(errors.input_yuan)}
                  onChange={(e) => update('input_yuan', e.target.value)}
                />
              </Field>

              <Field
                label="输出单价 (元 / 1M tokens)"
                htmlFor="unified-output-price"
                error={errors.output_yuan}
              >
                <TextInput
                  id="unified-output-price"
                  value={form.output_yuan}
                  placeholder="8.00"
                  inputMode="decimal"
                  invalid={Boolean(errors.output_yuan)}
                  onChange={(e) => update('output_yuan', e.target.value)}
                />
              </Field>

              <Field
                label="缓存命中读取 (元 / 1M tokens，可选)"
                htmlFor="unified-cache-read"
              >
                <TextInput
                  id="unified-cache-read"
                  value={form.cache_read_yuan}
                  placeholder="0.50"
                  inputMode="decimal"
                  onChange={(e) => update('cache_read_yuan', e.target.value)}
                />
              </Field>

              <Field
                label="缓存写入创建 (元 / 1M tokens，可选)"
                htmlFor="unified-cache-write"
              >
                <TextInput
                  id="unified-cache-write"
                  value={form.cache_write_yuan}
                  placeholder="2.00"
                  inputMode="decimal"
                  onChange={(e) => update('cache_write_yuan', e.target.value)}
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
