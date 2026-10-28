/**
 * 渠道批量操作条（我的渠道表格视图）。
 *
 * 勾选至少一条渠道后固定在页面底部（复用 SaveBar 容器）：
 *  - 批量改倍率：batchListingModels({listing_ids, multiplier_milli})；
 *  - 全部启用：batchListingModels({listing_ids, enabled: true})。
 *
 * 服务端是整批事务：任何一条被拒绝（MODEL_CHANGE_REJECTED）都不会生效，
 * 此时在条内逐条列出原因和最早可变更时间，选择保持不变方便调整后重试。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'

import { copy } from '@/copy'
import { formatMultiplier } from '@/lib/multiplier'
import { formatDateTime } from '@/lib/time'
import { Button, Input, SaveBar, useToast } from '@/ui'

import {
  batchListingModels,
  modelChangeErrors,
  sellerMultiplierBoundsQuery,
  type ModelChangeError,
  type MultiplierBounds,
  type SellerChannelKind,
  type SellerListing,
} from './api'
import { notify } from './components'
import { sellerErrorMessage } from './errors'

const page = copy.pages.sellerListings

export interface ListingBatchBarProps {
  /** Currently checked rows; the bar disappears when the page clears them. */
  listings: SellerListing[]
  /** Channel kind per listing id, used to intersect multiplier bounds. */
  kindById: ReadonlyMap<number, SellerChannelKind>
  onClear: () => void
}

/** 允许的倍率范围：所选渠道各类别上下限的交集，再夹进绝对边界。 */
function allowedRange(
  listings: readonly SellerListing[],
  kindById: ReadonlyMap<number, SellerChannelKind>,
  bounds: MultiplierBounds | undefined,
): { min: number; max: number } {
  let min = bounds?.absolute_min_milli ?? 1
  let max = bounds?.absolute_max_milli ?? 1000
  const kinds = new Set<SellerChannelKind>()
  for (const listing of listings) kinds.add(kindById.get(listing.id) ?? 'third_party')
  for (const kind of kinds) {
    const kindBounds = bounds?.kinds?.[kind]
    if (!kindBounds) continue
    min = Math.max(min, kindBounds.min_milli)
    max = Math.min(max, kindBounds.max_milli)
  }
  if (min > max) [min, max] = [max, min]
  return { min, max }
}

export function ListingBatchBar({ listings, kindById, onClear }: ListingBatchBarProps) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [milliText, setMilliText] = useState('')
  const [rejected, setRejected] = useState<ModelChangeError[]>([])
  const bounds = useQuery(sellerMultiplierBoundsQuery())

  const ids = useMemo(() => listings.map((listing) => listing.id), [listings])
  const range = useMemo(
    () => allowedRange(listings, kindById, bounds.data),
    [listings, kindById, bounds.data],
  )

  const parsed = Number(milliText)
  const milli = Math.round(parsed * 1000)
  const validMilli =
    milliText.trim() !== '' &&
    Number.isFinite(parsed) &&
    parsed > 0 &&
    milli >= range.min &&
    milli <= range.max

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ['marketplace', 'seller'] })
  }

  function handleBatchError(error: unknown) {
    const rows = modelChangeErrors(error)
    if (rows.length > 0) {
      setRejected(rows)
      notify(toast, { title: page.batch.rejectedTitle, tone: 'error' })
    } else {
      setRejected([])
      notify(toast, { title: sellerErrorMessage(error), tone: 'error' })
    }
  }

  const apply = useMutation({
    mutationFn: () => batchListingModels({ listing_ids: ids, multiplier_milli: milli }),
    onSuccess: () => {
      setRejected([])
      refresh()
      notify(toast, { title: page.batch.applied, tone: 'success' })
      onClear()
    },
    onError: handleBatchError,
  })

  const enableAll = useMutation({
    mutationFn: () => batchListingModels({ listing_ids: ids, enabled: true }),
    onSuccess: () => {
      setRejected([])
      refresh()
      notify(toast, { title: page.batch.enabled, tone: 'success' })
      onClear()
    },
    onError: handleBatchError,
  })

  const busy = apply.isPending || enableAll.isPending

  return (
    <SaveBar
      fixed
      hint={
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
          <span className="shrink-0 font-medium text-ink">
            {page.batch.selected(listings.length)}
          </span>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <label htmlFor="batch-multiplier" className="text-meta text-ink-3">
              {page.batch.multiplierLabel}
            </label>
            <Input
              id="batch-multiplier"
              className="w-24"
              inputMode="decimal"
              disabled={busy}
              value={milliText}
              onChange={(event) => {
                setMilliText(event.target.value)
                setRejected([])
              }}
            />
            <span className="text-caption text-ink-3">
              {page.batch.multiplierHint(
                formatMultiplier(range.min),
                formatMultiplier(range.max),
              )}
            </span>
          </div>
          <Button
            variant="secondary"
            size="sm"
            loading={enableAll.isPending}
            disabled={apply.isPending}
            onClick={() => enableAll.mutate()}
          >
            {page.batch.enableAll}
          </Button>
          {rejected.length > 0 ? (
            <div className="w-full text-meta leading-5 text-danger" role="alert">
              <p>{page.batch.rejectedTitle}</p>
              <ul className="mt-1 list-disc pl-5">
                {rejected.map((row) => (
                  <li key={`${row.listing_id}:${row.model}:${row.code}`}>
                    {page.batch.rejectedRow(row.listing_id, row.model, row.message)}
                    {row.earliest_change_at
                      ? `（${page.batch.earliest(formatDateTime(row.earliest_change_at))}）`
                      : null}
                  </li>
                ))}
              </ul>
              <p className="mt-1 text-ink-3">{page.batch.allOrNothing}</p>
            </div>
          ) : null}
        </div>
      }
      dirty={validMilli}
      saving={apply.isPending}
      saveLabel={page.batch.apply}
      savingLabel={page.batch.applying}
      saveDisabled={!validMilli}
      onSave={() => apply.mutate()}
      onCancel={onClear}
      cancelLabel={page.batch.clear}
      cancelDisabled={busy}
    />
  )
}
