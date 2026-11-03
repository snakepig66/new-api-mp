import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { cn } from '@/lib/cn'
import { ROLE_ADMIN_USER, getSession } from '@/lib/auth'
import { formatYuan } from '@/lib/money'
import {
  Empty,
  ModelName,
  PageHeader,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  useToast,
} from '@/ui'
import { TextInput as AdminTextInput } from '@/features/admin/components'

import {
  adminPoolModelsQuery,
  adminPricesQuery,
  putAdminPoolModel,
  type AdminPoolModel,
  type AdminPrice,
} from '@/features/admin/api'
import {
  ErrorBlock,
  OpsTabNav,
  TableSkeleton,
  type OpsTabLink,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

export const Route = createFileRoute('/console/ops/plaza')({
  beforeLoad: () => {
    const role = getSession()?.user?.role ?? 0
    if (role < ROLE_ADMIN_USER) {
      throw redirect({ to: '/console', replace: true })
    }
  },
  component: PlazaListingPage,
})

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/models', label: '模型池' },
  { to: '/console/ops/plaza', label: '广场上架' },
  { to: '/console/ops/pricing/per-call', label: '按次计费' },
]

function formatPlazaMultiplier(milli: number | undefined): string {
  const safe = typeof milli === 'number' && milli > 0 ? milli : 1000
  return (safe / 1000).toFixed(2)
}

function parsePlazaMultiplier(value: string): number | null {
  if (!value.trim()) return null
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed < 0.001 || parsed > 100) return null
  return Math.round(parsed * 1000)
}

function plazaPrice(price: number | string, multiplierMilli: number | undefined): string {
  const milli = typeof multiplierMilli === 'number' && multiplierMilli > 0 ? multiplierMilli : 1000
  return formatYuan(Math.round(Number(price) * milli) / 1000)
}

function PlazaListingPage() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const models = useQuery(adminPoolModelsQuery())
  const prices = useQuery(adminPricesQuery())

  const [search, setSearch] = useState('')
  const [listedOnly, setListedOnly] = useState(false)
  const [editingMultiplier, setEditingMultiplier] = useState<string | null>(null)
  const [multiplierDraft, setMultiplierDraft] = useState('')

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
    if (listedOnly) {
      list = list.filter((m) => m.enable_plaza !== false)
    }
    return [...list].sort((a, b) => a.name.localeCompare(b.name))
  }, [models.data, search, listedOnly])

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: adminPoolModelsQuery().queryKey })
  }

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
        enable_plaza: model.enable_plaza !== false,
        plaza_multiplier_milli: model.plaza_multiplier_milli,
      }),
    onSuccess: () => {
      refresh()
      toast.show({ title: '模型状态已更新', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const togglePlaza = useMutation({
    mutationFn: (model: AdminPoolModel) =>
      putAdminPoolModel({
        name: model.name,
        origin_vendor: model.origin_vendor,
        context_length: model.context_length,
        capabilities: model.capabilities,
        aliases: model.aliases,
        verify_prompt: model.verify_prompt,
        status: model.status,
        enable_plaza: model.enable_plaza === false,
        plaza_multiplier_milli: model.plaza_multiplier_milli,
      }),
    onSuccess: () => {
      refresh()
      toast.show({ title: '广场上架状态已更新', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  const setPlazaMultiplier = useMutation({
    mutationFn: ({ model, milli }: { model: AdminPoolModel; milli: number }) =>
      putAdminPoolModel({
        name: model.name,
        origin_vendor: model.origin_vendor,
        context_length: model.context_length,
        capabilities: model.capabilities,
        aliases: model.aliases,
        verify_prompt: model.verify_prompt,
        status: model.status,
        enable_plaza: model.enable_plaza !== false,
        plaza_multiplier_milli: milli,
      }),
    onSuccess: () => {
      refresh()
      toast.show({ title: '广场倍率已更新', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function startEditMultiplier(row: AdminPoolModel) {
    setEditingMultiplier(row.name)
    setMultiplierDraft(formatPlazaMultiplier(row.plaza_multiplier_milli))
  }

  function cancelEditMultiplier() {
    setEditingMultiplier(null)
    setMultiplierDraft('')
  }

  function commitEditMultiplier(row: AdminPoolModel) {
    const milli = parsePlazaMultiplier(multiplierDraft)
    if (milli === null) {
      toast.show({ title: '广场倍率需在 0.001 到 100 之间', tone: 'error' })
      return
    }
    setEditingMultiplier(null)
    setMultiplierDraft('')
    if (milli === (row.plaza_multiplier_milli || 1000)) return
    setPlazaMultiplier.mutate({ model: row, milli })
  }

  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
      <PageHeader
        title="广场上架"
        lead="控制模型广场展示哪些模型，并为每个模型设置广场倍率。广场价 = 基价 × 广场倍率。"
      />

      <div className="mt-6 space-y-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-3">
            <div className="w-full sm:w-72">
              <AdminTextInput
                placeholder="搜索模型 ID、厂商..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <label className="inline-flex cursor-pointer items-center gap-2 text-meta text-ink-2 select-none">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-line text-brand accent-brand cursor-pointer"
                checked={listedOnly}
                onChange={(e) => setListedOnly(e.target.checked)}
              />
              只看已上架
            </label>
          </div>
          <div className="flex items-center gap-2 text-meta text-ink-3">
            <span>
              共 <strong className="font-medium text-ink">{filteredRows.length}</strong> 个模型
            </span>
          </div>
        </div>

        {models.isPending || prices.isPending ? (
          <TableSkeleton rows={8} columns={6} />
        ) : models.isError ? (
          <ErrorBlock error={models.error} onRetry={refresh} />
        ) : filteredRows.length === 0 ? (
          <Empty
            title="暂无模型"
            reason={search || listedOnly ? '没有匹配筛选条件的模型' : '标准模型池当前为空'}
            next="可在「模型池」中添加模型及基价"
          />
        ) : (
          <Table minWidth={960}>
            <TableHead>
              <TableRow interactive={false}>
                <TableHeaderCell className="w-[22%]">模型 ID</TableHeaderCell>
                <TableHeaderCell className="w-[18%]">基价（输入 / 输出）</TableHeaderCell>
                <TableHeaderCell className="w-[12%] text-center">广场倍率</TableHeaderCell>
                <TableHeaderCell className="w-[20%]">广场价（输入 / 输出）</TableHeaderCell>
                <TableHeaderCell className="w-[14%] text-center">广场上架</TableHeaderCell>
                <TableHeaderCell className="w-[14%] text-center">模型状态</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {filteredRows.map((row) => {
                const p = latestPriceMap.get(row.name)
                const hasPrice = Boolean(p)
                const listed = row.enable_plaza !== false
                const isStatusToggling =
                  toggleStatus.isPending && toggleStatus.variables?.name === row.name
                const isPlazaToggling =
                  togglePlaza.isPending && togglePlaza.variables?.name === row.name

                return (
                  <TableRow key={row.name}>
                    <TableCell>
                      <ModelName
                        model={row.name}
                        vendor={row.origin_vendor}
                        size="md"
                        className="font-semibold text-ink"
                      />
                    </TableCell>

                    <TableCell>
                      {hasPrice && p ? (
                        <div className="flex flex-col gap-0.5 font-mono text-caption text-ink-2">
                          <span>输入 {formatYuan(p.input_micro_per_mtok)}</span>
                          <span>输出 {formatYuan(p.output_micro_per_mtok)}</span>
                        </div>
                      ) : (
                        <span className="text-caption text-ink-4">无基价</span>
                      )}
                    </TableCell>

                    <TableCell className="text-center">
                      {editingMultiplier === row.name ? (
                        <input
                          aria-label="广场倍率"
                          data-testid="plaza-multiplier-input"
                          className="w-16 rounded-md border border-line bg-card px-1.5 py-0.5 text-center font-mono text-meta text-ink focus:border-brand focus:outline-none"
                          value={multiplierDraft}
                          inputMode="decimal"
                          autoFocus
                          onChange={(e) => setMultiplierDraft(e.target.value)}
                          onBlur={() => commitEditMultiplier(row)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') commitEditMultiplier(row)
                            else if (e.key === 'Escape') cancelEditMultiplier()
                          }}
                        />
                      ) : (
                        <button
                          type="button"
                          aria-label={`编辑广场倍率 ${row.name}`}
                          className="cursor-pointer rounded-md px-2 py-0.5 font-mono text-meta font-medium text-ink-2 hover:bg-brand-soft/50 hover:text-brand-ink"
                          onClick={() => startEditMultiplier(row)}
                        >
                          {formatPlazaMultiplier(row.plaza_multiplier_milli)}x
                        </button>
                      )}
                    </TableCell>

                    <TableCell>
                      {hasPrice && p ? (
                        <div className="flex flex-col gap-0.5 font-mono text-caption">
                          <span className="text-brand">
                            输入 {plazaPrice(p.input_micro_per_mtok, row.plaza_multiplier_milli)}
                          </span>
                          <span className="text-ink">
                            输出 {plazaPrice(p.output_micro_per_mtok, row.plaza_multiplier_milli)}
                          </span>
                        </div>
                      ) : (
                        <span className="text-caption text-ink-4">—</span>
                      )}
                    </TableCell>

                    <TableCell className="text-center">
                      <div className="flex items-center justify-center gap-1.5 whitespace-nowrap">
                        {hasPrice ? (
                          <>
                            <Switch
                              checked={listed}
                              loading={isPlazaToggling}
                              onCheckedChange={() => togglePlaza.mutate(row)}
                              aria-label={listed ? '点击下架广场' : '点击上架广场'}
                            />
                            <span
                              className={cn(
                                'text-caption font-medium',
                                listed ? 'text-brand-ink' : 'text-ink-4',
                              )}
                            >
                              {listed ? '已上架' : '未上架'}
                            </span>
                          </>
                        ) : (
                          <span className="text-caption text-ink-4" title="无基价，不可上架">
                            无基价，不可上架
                          </span>
                        )}
                      </div>
                    </TableCell>

                    <TableCell className="text-center">
                      <div className="flex items-center justify-center gap-2 whitespace-nowrap">
                        <Switch
                          checked={row.status === 'active'}
                          loading={isStatusToggling}
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
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </div>
    </>
  )
}
