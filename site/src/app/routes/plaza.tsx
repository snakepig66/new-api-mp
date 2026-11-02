import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { CreateKeyDialog } from '@/features/console/components/CreateKeyDialog'
import { plazaModelsQuery, type PlazaModel } from '@/features/market/api'
import { formatCompactYuan } from '@/features/market/ModelCard'
import { writeClipboard } from '@/lib/clipboard'
import { cn } from '@/lib/cn'
import { Button, Empty, Input, ModelName, PageHeader, Tag, useToast } from '@/ui'

export const Route = createFileRoute('/plaza')({
  component: PlazaPage,
})

export function PlazaPage() {
  const toast = useToast()
  const plazaQuery = useQuery(plazaModelsQuery())
  const [search, setSearch] = useState('')
  const [selectedVendor, setSelectedVendor] = useState('all')
  const [createKeyOpen, setCreateKeyOpen] = useState(false)
  const [copiedModel, setCopiedModel] = useState<string | null>(null)

  const models = plazaQuery.data ?? []

  const vendors = useMemo(() => {
    const set = new Set<string>()
    for (const m of models) {
      const v = m.origin_vendor || m.vendor
      if (v) set.add(v)
    }
    return Array.from(set).sort()
  }, [models])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return models.filter((m) => {
      const vendor = (m.origin_vendor || m.vendor || '').toLowerCase()
      if (selectedVendor !== 'all' && vendor !== selectedVendor.toLowerCase()) {
        return false
      }
      if (q) {
        const nameMatch = m.model.toLowerCase().includes(q)
        const vendorMatch = vendor.includes(q)
        const aliasMatch = m.aliases?.some((a) => a.toLowerCase().includes(q)) ?? false
        if (!nameMatch && !vendorMatch && !aliasMatch) return false
      }
      return true
    })
  }, [models, search, selectedVendor])

  async function handleCopyModel(modelName: string) {
    const ok = await writeClipboard(modelName)
    if (ok) {
      setCopiedModel(modelName)
      setTimeout(() => setCopiedModel(null), 2000)
      toast.show({ title: '已复制模型名', tone: 'success' })
    } else {
      toast.show({ title: '复制失败', tone: 'error' })
    }
  }

  return (
    <div className="container mx-auto max-w-6xl px-4 py-8">
      <PageHeader
        title={copy.pages.plaza.title}
        lead={copy.pages.plaza.lead}
      />

      {/* Filters */}
      <div className="mt-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="max-w-md flex-1">
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={copy.pages.plaza.searchPlaceholder}
            aria-label={copy.pages.plaza.searchAria}
          />
        </div>

        <div className="flex flex-wrap items-center gap-1.5 overflow-x-auto pb-1">
          <button
            type="button"
            onClick={() => setSelectedVendor('all')}
            className={cn(
              'rounded-full px-3 py-1 text-meta font-medium transition-colors cursor-pointer',
              selectedVendor === 'all'
                ? 'bg-brand text-white'
                : 'bg-surface text-ink-2 hover:bg-surface-hover hover:text-ink border border-line',
            )}
          >
            {copy.pages.plaza.allVendors}
          </button>
          {vendors.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setSelectedVendor(v)}
              className={cn(
                'rounded-full px-3 py-1 text-meta font-medium capitalize transition-colors cursor-pointer',
                selectedVendor === v
                  ? 'bg-brand text-white'
                  : 'bg-surface text-ink-2 hover:bg-surface-hover hover:text-ink border border-line',
              )}
            >
              {v}
            </button>
          ))}
        </div>
      </div>

      {/* Content */}
      {plazaQuery.isLoading ? (
        <div className="mt-8 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div
              key={i}
              className="h-48 rounded-xl border border-line bg-surface/50 animate-pulse"
            />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <div className="mt-12">
          <Empty
            title={copy.pages.plaza.empty}
            reason="没有找到匹配的模型，请尝试其他关键词或厂商筛选。"
          />
        </div>
      ) : (
        <div className="mt-8 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filtered.map((model) => (
            <PlazaModelCard
              key={model.model}
              item={model}
              onCopy={handleCopyModel}
              isCopied={copiedModel === model.model}
              onCallWithKey={() => setCreateKeyOpen(true)}
            />
          ))}
        </div>
      )}

      <CreateKeyDialog
        open={createKeyOpen}
        onOpenChange={setCreateKeyOpen}
        initialKeyType="standard"
        onCreated={() => {}}
      />
    </div>
  )
}

function PlazaModelCard({
  item,
  onCopy,
  isCopied,
  onCallWithKey,
}: {
  item: PlazaModel
  onCopy: (name: string) => void
  isCopied: boolean
  onCallWithKey: (name: string) => void
}) {
  const vendor = item.origin_vendor || item.vendor || '平台'
  const inputPrice = formatCompactYuan(item.prices?.input_micro_per_mtok)
  const outputPrice = formatCompactYuan(item.prices?.output_micro_per_mtok)
  const contextK =
    item.context_length > 0 ? `${Math.round(item.context_length / 1024)}K` : null

  return (
    <article className="flex flex-col justify-between rounded-xl border border-line bg-surface p-5 shadow-xs transition-all hover:border-line-strong hover:shadow-sm">
      <div className="space-y-3.5">
        {/* Header: Name + Vendor Tag + Status */}
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0 flex-1">
            <h3 className="truncate font-semibold text-title text-ink">
              <ModelName model={item.model} size="md" />
            </h3>
            <div className="mt-1 flex items-center gap-1.5 text-meta text-ink-3">
              <span className="capitalize">{vendor}</span>
              {contextK ? (
                <>
                  <span>·</span>
                  <span>{contextK} 上下文</span>
                </>
              ) : null}
            </div>
          </div>
          <Tag tone="brand" dot className="shrink-0 text-caption">
            {copy.pages.plaza.statusAvailable}
          </Tag>
        </div>

        {/* Price display: 元 / 1M tokens */}
        <div className="rounded-lg border border-line bg-bg/80 px-3.5 py-2.5">
          <div className="text-caption text-ink-3">平台官方定价 (元 / 1M tokens)</div>
          <div className="mt-1 flex items-center justify-between text-meta">
            <span className="text-ink-2">
              输入 <span className="font-semibold text-ink tabular-nums">{inputPrice}</span>
            </span>
            <span className="text-ink-2">
              输出 <span className="font-semibold text-ink tabular-nums">{outputPrice}</span>
            </span>
          </div>
        </div>
      </div>

      {/* Footer Actions: 复制模型名 / 用普通密钥调用 */}
      <div className="mt-4 flex items-center gap-2 border-t border-line/60 pt-3">
        <Button
          variant="secondary"
          size="sm"
          className="flex-1"
          onClick={() => onCopy(item.model)}
        >
          {isCopied ? copy.pages.plaza.copied : copy.pages.plaza.copyModelName}
        </Button>
        <Button
          variant="primary"
          size="sm"
          className="flex-1"
          onClick={() => onCallWithKey(item.model)}
        >
          {copy.pages.plaza.callWithStandardKey}
        </Button>
      </div>
    </article>
  )
}
