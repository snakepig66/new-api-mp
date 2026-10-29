import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  Button,
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
  Section,
  Select,
  TableSkeleton,
  TextInput,
  type OpsTabLink,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { defaultEffectiveAt, shanghaiDateTimeToRfc3339 } from '@/features/admin/schemas'

export const Route = createFileRoute('/console/ops/models')({
  component: ModelsAndPricingPage,
})

const page = copy.pages.adminPool
const labels = page.status

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/models', label: '模型与定价' },
  { to: '/console/ops/pricing/per-call', label: copy.pages.adminCommon.nav.perCallPrices },
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

function statusLabel(status: string): string {
  return (labels as Record<string, string>)[status] ?? labels.unknown
}

function StatusTag({ status }: { status: string }) {
  return (
    <Tag tone={status === 'active' ? 'brand' : 'neutral'} dot={status === 'active'}>
      {statusLabel(status)}
    </Tag>
  )
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

  const rows = useMemo(() => {
    const list = models.data ?? []
    if (!search.trim()) return list
    const q = search.trim().toLowerCase()
    return list.filter(
      (m) =>
        m.name.toLowerCase().includes(q) ||
        m.origin_vendor.toLowerCase().includes(q) ||
        m.aliases.some((a) => a.toLowerCase().includes(q)),
    )
  }, [models.data, search])

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
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="w-full max-w-xs">
            <TextInput
              placeholder="搜索模型名称、厂商、别名..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <span className="text-meta text-ink-3">
            共 {rows.length} 个模型 (已配置定价: {latestPriceMap.size})
          </span>
        </div>

        <Section title="标准模型与定价表">
          {models.isPending || prices.isPending ? (
            <TableSkeleton rows={8} columns={7} />
          ) : models.isError ? (
            <ErrorBlock error={models.error} onRetry={refresh} />
          ) : rows.length === 0 ? (
            <Empty
              title="暂无模型"
              reason={search ? '没有匹配搜索条件的模型' : '标准模型池当前为空'}
              next="点击右上角按钮添加第一个模型及定价"
              action={
                <Button variant="secondary" size="sm" onClick={openCreate}>
                  + 添加模型与定价
                </Button>
              }
            />
          ) : (
            <Table minWidth={1000}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>模型名称</TableHeaderCell>
                  <TableHeaderCell>厂商</TableHeaderCell>
                  <TableHeaderCell numeric>上下文</TableHeaderCell>
                  <TableHeaderCell numeric>输入单价 (元/M tok)</TableHeaderCell>
                  <TableHeaderCell numeric>输出单价 (元/M tok)</TableHeaderCell>
                  <TableHeaderCell numeric>缓存读/写 (元/M tok)</TableHeaderCell>
                  <TableHeaderCell>状态</TableHeaderCell>
                  <TableHeaderCell numeric>操作</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {rows.map((row) => {
                  const p = latestPriceMap.get(row.name)
                  const hasPrice = Boolean(p)
                  return (
                    <TableRow key={row.name}>
                      <TableCell>
                        <div className="flex flex-col gap-0.5">
                          <ModelName model={row.name} size="sm" />
                          {row.aliases.length > 0 ? (
                            <span className="text-caption text-ink-3">
                              别名: {row.aliases.join(', ')}
                            </span>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell className="text-meta text-ink-2">
                        {row.origin_vendor || '—'}
                      </TableCell>
                      <TableCell numeric className="font-mono text-meta text-ink-2">
                        {row.context_length
                          ? `${Math.round(row.context_length / 1024)}k`
                          : '—'}
                      </TableCell>
                      <TableCell numeric className="font-mono text-meta">
                        {hasPrice && p ? (
                          <span className="text-brand font-medium">
                            {formatYuan(p.input_micro_per_mtok)}
                          </span>
                        ) : (
                          <span className="text-ink-4">未配置</span>
                        )}
                      </TableCell>
                      <TableCell numeric className="font-mono text-meta">
                        {hasPrice && p ? (
                          <span className="text-ink font-medium">
                            {formatYuan(p.output_micro_per_mtok)}
                          </span>
                        ) : (
                          <span className="text-ink-4">未配置</span>
                        )}
                      </TableCell>
                      <TableCell numeric className="font-mono text-caption text-ink-3">
                        {hasPrice && p && p.cache_read_micro_per_mtok
                          ? `${formatYuan(p.cache_read_micro_per_mtok)} / ${formatYuan(p.cache_write_micro_per_mtok)}`
                          : '—'}
                      </TableCell>
                      <TableCell>
                        <StatusTag status={row.status} />
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1.5">
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => openEdit(row)}
                          >
                            编辑
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            loading={toggleStatus.isPending}
                            onClick={() => toggleStatus.mutate(row)}
                          >
                            {row.status === 'active' ? '停用' : '启用'}
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            onClick={() => setDeleteTarget(row)}
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
          )}
        </Section>
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
