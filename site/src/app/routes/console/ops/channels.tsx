/**
 * 运营后台 · 广场渠道。
 *
 * 广场渠道就是 new-api 的 channels 表：站长在这里维护上游地址、Key 与支持的
 * 模型，所有普通密钥调用广场模型时共用这些渠道。列表接口不返回 Key，编辑时
 * Key 留空表示不修改；后端再做一遍校验，前端校验只是更早的提示。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { formatDateTime } from '@/lib/time'
import {
  Button,
  buttonClasses,
  Dialog,
  Empty,
  PageHeader,
  Pagination,
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
  channelDefaultBaseUrlsQuery,
  channelPoolModelsQuery,
  channelsQuery,
  createChannel,
  deleteChannel,
  testChannel,
  updateChannel,
  updateChannelStatus,
  type Channel,
  type ChannelInput,
  type ChannelTestResult,
} from '@/features/admin/channels-api'
import {
  ConfirmDialog,
  ErrorBlock,
  Field,
  Notice,
  Select,
  TableSkeleton,
  TextArea,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

export const Route = createFileRoute('/console/ops/channels')({
  component: ChannelsPage,
})

const PAGE_SIZE = 20

const STATUS_ENABLED = 1
const STATUS_MANUAL = 2
const STATUS_AUTO = 3

const CHANNEL_TYPES: { value: number; label: string }[] = [
  { value: 1, label: 'OpenAI 兼容' },
  { value: 14, label: 'Anthropic (Claude)' },
  { value: 24, label: 'Google (Gemini)' },
  { value: 8, label: '自定义 (Custom)' },
]

const CHANNEL_TYPE_LABELS: Record<number, string> = Object.fromEntries(
  CHANNEL_TYPES.map((t) => [t.value, t.label]),
)

function channelTypeLabel(type: number): string {
  return CHANNEL_TYPE_LABELS[type] ?? `类型 ${type}`
}

function statusLabel(status: number): { label: string; tone: 'success' | 'warning' | 'danger' | 'neutral' } {
  if (status === STATUS_ENABLED) return { label: '启用', tone: 'success' }
  if (status === STATUS_MANUAL) return { label: '手动禁用', tone: 'warning' }
  if (status === STATUS_AUTO) return { label: '自动禁用', tone: 'danger' }
  return { label: '未知', tone: 'neutral' }
}

function splitModels(value: string): string[] {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function parseMapping(raw: string | null): { from: string; to: string }[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return []
    return Object.entries(parsed as Record<string, unknown>).map(([from, to]) => ({
      from,
      to: typeof to === 'string' ? to : String(to),
    }))
  } catch {
    return []
  }
}

function buildMapping(rows: { from: string; to: string }[]): string {
  const map: Record<string, string> = {}
  for (const row of rows) {
    const from = row.from.trim()
    const to = row.to.trim()
    if (from) map[from] = to
  }
  return JSON.stringify(map)
}

interface ChannelForm {
  type: number
  name: string
  base_url: string
  key: string
  models: string
  mapping: { from: string; to: string }[]
  group: string
  priority: string
  weight: string
  test_model: string
  setting: string | null
  other: string
  settings: string
}

const emptyForm: ChannelForm = {
  type: 1,
  name: '',
  base_url: '',
  key: '',
  models: '',
  mapping: [],
  group: 'default',
  priority: '0',
  weight: '0',
  test_model: '',
  setting: null,
  other: '',
  settings: '',
}

function ChannelsPage() {
  const toast = useToast()
  const queryClient = useQueryClient()

  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  const [page, setPage] = useState(1)

  const list = useQuery(channelsQuery({ page, pageSize: PAGE_SIZE, keyword }))
  const defaults = useQuery(channelDefaultBaseUrlsQuery())

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Channel | null>(null)
  const [form, setForm] = useState<ChannelForm>(emptyForm)
  const [errors, setErrors] = useState<Partial<Record<keyof ChannelForm, string>>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null)
  const [testTarget, setTestTarget] = useState<Channel | null>(null)
  const [testModelDraft, setTestModelDraft] = useState('')
  const [testResult, setTestResult] = useState<ChannelTestResult | null>(null)

  const poolModels = useQuery({ ...channelPoolModelsQuery(), enabled: dialogOpen })

  const total = list.data?.total ?? 0

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['admin', 'channels'] })
  }

  const toggleStatus = useMutation({
    mutationFn: (channel: Channel) =>
      updateChannelStatus(
        channel.id,
        channel.status === STATUS_ENABLED ? STATUS_MANUAL : STATUS_ENABLED,
      ),
    onSuccess: () => {
      refresh()
      toast.show({ title: '渠道状态已更新', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const remove = useMutation({
    mutationFn: (id: number) => deleteChannel(id),
    onSuccess: () => {
      refresh()
      toast.show({ title: '渠道已删除', tone: 'success' })
      setDeleteTarget(null)
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const test = useMutation({
    mutationFn: ({ id, model }: { id: number; model?: string }) => testChannel(id, model),
    onSuccess: (result) => {
      setTestResult(result)
      refresh()
    },
  })

  function runSearch() {
    setPage(1)
    setKeyword(search.trim())
  }

  function openCreate() {
    setEditing(null)
    setForm(emptyForm)
    setErrors({})
    setSubmitError(null)
    setDialogOpen(true)
  }

  function openEdit(channel: Channel) {
    setEditing(channel)
    setForm({
      type: channel.type,
      name: channel.name,
      base_url: channel.base_url ?? '',
      key: '',
      models: channel.models,
      mapping: parseMapping(channel.model_mapping),
      group: channel.group || 'default',
      priority: String(channel.priority ?? 0),
      weight: String(channel.weight ?? 0),
      test_model: channel.test_model ?? '',
      setting: channel.setting,
      other: channel.other,
      settings: channel.settings,
    })
    setErrors({})
    setSubmitError(null)
    setDialogOpen(true)
  }

  function update<K extends keyof ChannelForm>(key: K, value: ChannelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({ ...current, [key]: undefined }))
    setSubmitError(null)
  }

  function toggleModel(name: string) {
    const set = new Set(splitModels(form.models))
    if (set.has(name)) set.delete(name)
    else set.add(name)
    update('models', Array.from(set).join(','))
  }

  function handleSubmit() {
    const next: Partial<Record<keyof ChannelForm, string>> = {}
    const name = form.name.trim()
    const group = form.group.trim() || 'default'
    const priority = Number(form.priority)
    const weight = Number(form.weight)

    if (!name) next.name = '渠道名称不能为空'
    if (!editing && !form.key.trim()) next.key = '渠道 Key 不能为空'
    if (!Number.isInteger(priority)) next.priority = '优先级必须为整数'
    if (!Number.isInteger(weight) || weight < 0) next.weight = '权重必须为非负整数'

    if (Object.keys(next).length > 0) {
      setErrors(next)
      return
    }

    const mapping = buildMapping(form.mapping)
    const input: ChannelInput = {
      ...(editing ? { id: editing.id } : {}),
      type: form.type,
      name,
      base_url: form.base_url.trim(),
      key: form.key.trim(),
      models: form.models,
      model_mapping: mapping === '{}' && editing ? (editing.model_mapping ?? '') : mapping,
      group,
      priority,
      weight,
      test_model: form.test_model.trim(),
      ...(form.setting ? { setting: form.setting } : {}),
      ...(form.other ? { other: form.other } : {}),
      ...(form.settings ? { settings: form.settings } : {}),
    }

    save.mutate(input)
  }

  const save = useMutation({
    mutationFn: (input: ChannelInput) =>
      input.id ? updateChannel(input) : createChannel(input),
    onSuccess: () => {
      refresh()
      toast.show({
        title: editing ? '渠道修改成功' : '渠道创建成功',
        tone: 'success',
      })
      setDialogOpen(false)
    },
    onError: (error) => setSubmitError(adminErrorMessage(error)),
  })

  function openTest(channel: Channel) {
    setTestTarget(channel)
    setTestModelDraft(channel.test_model ?? '')
    setTestResult(null)
  }

  function startTest() {
    if (!testTarget) return
    test.mutate({ id: testTarget.id, model: testModelDraft.trim() || undefined })
  }

  const selectedModels = useMemo(() => new Set(splitModels(form.models)), [form.models])
  const defaultBaseURL = defaults.data?.[form.type] ?? ''

  return (
    <>
      <PageHeader
        title="广场渠道"
        lead="维护所有普通密钥调用广场模型时共用的上游渠道。Key 只在保存时提交，不会在页面回显。"
        actions={
          <Button size="sm" onClick={openCreate}>
            + 添加渠道
          </Button>
        }
      />

      <div className="mt-6 space-y-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex w-full items-center gap-2 sm:w-auto">
            <div className="w-full sm:w-72">
              <TextInput
                placeholder="搜索渠道名称、模型..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') runSearch()
                }}
              />
            </div>
            <Button variant="secondary" size="sm" onClick={runSearch}>
              搜索
            </Button>
          </div>
          <div className="text-meta text-ink-3">
            共 <strong className="font-medium text-ink">{total}</strong> 个渠道
          </div>
        </div>

        {list.isPending ? (
          <TableSkeleton rows={6} columns={8} />
        ) : list.isError ? (
          <ErrorBlock error={list.error} onRetry={refresh} />
        ) : (list.data?.items.length ?? 0) === 0 ? (
          <Empty
            title="暂无渠道"
            reason={keyword ? '没有匹配搜索条件的渠道' : '还没有添加任何广场渠道'}
            next="点击右上角按钮添加渠道，普通密钥才能调用广场模型"
            action={
              <Button variant="secondary" size="sm" onClick={openCreate}>
                + 添加渠道
              </Button>
            }
          />
        ) : (
          <div className="overflow-hidden rounded-xl border border-line bg-card/60 shadow-xs">
            <Table minWidth={960}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell className="w-[6%]">ID</TableHeaderCell>
                  <TableHeaderCell className="w-[18%]">名称</TableHeaderCell>
                  <TableHeaderCell className="w-[13%]">类型</TableHeaderCell>
                  <TableHeaderCell className="w-[18%]">上游地址</TableHeaderCell>
                  <TableHeaderCell className="w-[7%] text-center">模型数</TableHeaderCell>
                  <TableHeaderCell className="w-[10%]">状态</TableHeaderCell>
                  <TableHeaderCell className="w-[9%] text-center">响应</TableHeaderCell>
                  <TableHeaderCell className="w-[10%]">最近测试</TableHeaderCell>
                  <TableHeaderCell numeric className="w-[9%]">优先级 / 操作</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {list.data?.items.map((channel) => {
                  const status = statusLabel(channel.status)
                  const enabled = channel.status === STATUS_ENABLED
                  return (
                    <TableRow key={channel.id}>
                      <TableCell className="font-mono text-meta text-ink-3">{channel.id}</TableCell>
                      <TableCell>
                        <div className="font-medium text-ink">{channel.name || '未命名'}</div>
                      </TableCell>
                      <TableCell className="text-meta text-ink-2">
                        {channelTypeLabel(channel.type)}
                      </TableCell>
                      <TableCell className="max-w-[220px] truncate font-mono text-caption text-ink-2">
                        {channel.base_url || defaultBaseURL || '—'}
                      </TableCell>
                      <TableCell className="text-center text-meta">
                        {splitModels(channel.models).length}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2 whitespace-nowrap">
                          <Switch
                            checked={enabled}
                            loading={
                              toggleStatus.isPending &&
                              toggleStatus.variables?.id === channel.id
                            }
                            onCheckedChange={() => toggleStatus.mutate(channel)}
                            aria-label={enabled ? `禁用渠道 ${channel.name}` : `启用渠道 ${channel.name}`}
                          />
                          <Tag tone={status.tone} className="whitespace-nowrap">
                            {status.label}
                          </Tag>
                        </div>
                      </TableCell>
                      <TableCell className="text-center text-meta tabular-nums text-ink-2">
                        {channel.response_time > 0 ? `${channel.response_time}ms` : '—'}
                      </TableCell>
                      <TableCell className="text-meta text-ink-3">
                        {channel.test_time > 0 ? formatDateTime(channel.test_time) : '—'}
                      </TableCell>
                      <TableCell numeric>
                        <div className="flex items-center justify-end gap-1.5 whitespace-nowrap">
                          <span className="mr-1 font-mono text-meta text-ink-2">
                            {channel.priority}
                          </span>
                          <Button variant="ghost" size="sm" onClick={() => openTest(channel)}>
                            测试
                          </Button>
                          <Button variant="secondary" size="sm" onClick={() => openEdit(channel)}>
                            编辑
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-danger"
                            onClick={() => setDeleteTarget(channel)}
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
        )}

        {total > PAGE_SIZE ? (
          <Pagination
            page={page}
            pageSize={PAGE_SIZE}
            total={total}
            onPageChange={setPage}
            compact
            showSummary
            ariaLabel="渠道分页"
          />
        ) : null}
      </div>

      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!open) {
            setDialogOpen(false)
            setSubmitError(null)
          }
        }}
        title={editing ? `编辑渠道：${editing.name}` : '添加渠道'}
        description="配置上游地址、Key、支持的模型以及路由分组。Key 只写不回显。"
        className="max-h-[calc(100vh-3rem)] max-w-2xl overflow-y-auto"
        footer={
          <>
            <Button variant="secondary" disabled={save.isPending} onClick={() => setDialogOpen(false)}>
              取消
            </Button>
            <Button loading={save.isPending} onClick={handleSubmit}>
              {save.isPending ? '正在保存...' : '保存渠道'}
            </Button>
          </>
        }
      >
        <div className="space-y-5">
          {submitError ? <Notice tone="warning">{submitError}</Notice> : null}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="类型" htmlFor="channel-type">
              <Select
                id="channel-type"
                value={String(form.type)}
                onChange={(e) => update('type', Number(e.target.value))}
              >
                {CHANNEL_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </Select>
            </Field>

            <Field label="名称" htmlFor="channel-name" error={errors.name}>
              <TextInput
                id="channel-name"
                value={form.name}
                placeholder="例如 OpenAI 主渠道"
                invalid={Boolean(errors.name)}
                onChange={(e) => update('name', e.target.value)}
              />
            </Field>
          </div>

          <Field
            label="上游地址"
            htmlFor="channel-base-url"
            hint={defaultBaseURL ? `留空则使用类型默认：${defaultBaseURL}` : '留空则使用类型内置默认地址'}
          >
            <TextInput
              id="channel-base-url"
              value={form.base_url}
              placeholder={defaultBaseURL || 'https://api.example.com'}
              mono
              onChange={(e) => update('base_url', e.target.value)}
            />
          </Field>

          <Field
            label="Key"
            htmlFor="channel-key"
            error={errors.key}
            hint={editing ? '留空表示不修改；多个 Key 可每行一个' : '多个 Key 可每行一个'}
          >
            <TextArea
              id="channel-key"
              value={form.key}
              rows={3}
              invalid={Boolean(errors.key)}
              placeholder={editing ? '留空保持不变' : 'sk-...（每行一个）'}
              onChange={(e) => update('key', e.target.value)}
            />
          </Field>

          <Field label="支持的模型" htmlFor="channel-models" hint="多个模型用英文逗号分隔，可从模型池勾选或在下方手动输入">
            <TextArea
              id="channel-models"
              value={form.models}
              rows={2}
              placeholder="gpt-4o, gpt-4o-mini"
              onChange={(e) => update('models', e.target.value)}
            />
            {poolModels.data && poolModels.data.length > 0 ? (
              <div className="mt-2 flex max-h-40 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-line bg-white/40 p-2">
                {poolModels.data.map((name) => {
                  const active = selectedModels.has(name)
                  return (
                    <button
                      key={name}
                      type="button"
                      aria-pressed={active}
                      onClick={() => toggleModel(name)}
                      className={buttonClasses(active ? 'primary' : 'secondary', 'sm')}
                    >
                      {name}
                    </button>
                  )
                })}
              </div>
            ) : null}
          </Field>

          <Field label="模型映射" hint="左侧为请求模型名，右侧为上游模型名；省略表示不映射">
            <div className="space-y-2">
              {form.mapping.map((row, index) => (
                <div key={index} className="flex items-center gap-2">
                  <TextInput
                    aria-label={`映射源 ${index + 1}`}
                    value={row.from}
                    placeholder="请求模型"
                    mono
                    onChange={(e) => {
                      const next = form.mapping.slice()
                      next[index] = { ...next[index], from: e.target.value }
                      update('mapping', next)
                    }}
                  />
                  <span className="text-ink-3">→</span>
                  <TextInput
                    aria-label={`映射目标 ${index + 1}`}
                    value={row.to}
                    placeholder="上游模型"
                    mono
                    onChange={(e) => {
                      const next = form.mapping.slice()
                      next[index] = { ...next[index], to: e.target.value }
                      update('mapping', next)
                    }}
                  />
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`删除映射 ${index + 1}`}
                    onClick={() => update('mapping', form.mapping.filter((_, i) => i !== index))}
                  >
                    移除
                  </Button>
                </div>
              ))}
              <Button
                variant="secondary"
                size="sm"
                onClick={() => update('mapping', [...form.mapping, { from: '', to: '' }])}
              >
                + 添加映射
              </Button>
            </div>
          </Field>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="分组" htmlFor="channel-group" hint="默认 default">
              <TextInput
                id="channel-group"
                value={form.group}
                onChange={(e) => update('group', e.target.value)}
              />
            </Field>
            <Field label="优先级" htmlFor="channel-priority" error={errors.priority}>
              <TextInput
                id="channel-priority"
                value={form.priority}
                inputMode="numeric"
                invalid={Boolean(errors.priority)}
                onChange={(e) => update('priority', e.target.value)}
              />
            </Field>
            <Field label="权重" htmlFor="channel-weight" error={errors.weight}>
              <TextInput
                id="channel-weight"
                value={form.weight}
                inputMode="numeric"
                invalid={Boolean(errors.weight)}
                onChange={(e) => update('weight', e.target.value)}
              />
            </Field>
          </div>

          <Field label="测试模型" htmlFor="channel-test-model" hint="一键测试时使用的模型，留空由后端自动选择">
            <TextInput
              id="channel-test-model"
              value={form.test_model}
              placeholder="gpt-4o-mini"
              onChange={(e) => update('test_model', e.target.value)}
            />
          </Field>
        </div>
      </Dialog>

      <Dialog
        open={testTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setTestTarget(null)
            setTestResult(null)
          }
        }}
        title="测试渠道"
        description={testTarget ? `渠道 ${testTarget.name}（#${testTarget.id}）` : undefined}
        footer={
          <>
            <Button variant="secondary" onClick={() => setTestTarget(null)}>
              关闭
            </Button>
            <Button loading={test.isPending} onClick={startTest}>
              {test.isPending ? '测试中...' : '开始测试'}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <Field label="测试模型" htmlFor="channel-test-model-runtime" hint="留空时由后端自动选择">
            <TextInput
              id="channel-test-model-runtime"
              value={testModelDraft}
              placeholder="可选"
              onChange={(e) => setTestModelDraft(e.target.value)}
            />
          </Field>
          {testResult ? (
            testResult.success ? (
              <Notice tone="brand" title="测试成功">
                耗时 {testResult.time.toFixed(2)} 秒
              </Notice>
            ) : (
              <Notice tone="warning" title="测试失败">
                {testResult.message || '上游未返回可用响应'}
              </Notice>
            )
          ) : null}
        </div>
      </Dialog>

      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除渠道"
        description={
          deleteTarget
            ? `确定删除渠道「${deleteTarget.name}」(#${deleteTarget.id})？该操作不可撤销。`
            : undefined
        }
        confirmLabel="删除"
        loading={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.id)
        }}
        onClose={() => setDeleteTarget(null)}
      />
    </>
  )
}
