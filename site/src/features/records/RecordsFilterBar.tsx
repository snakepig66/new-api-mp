/**
 * Shared filter bar for the usage-records page (买方「我的调用」+ 卖方「我卖出的」).
 *
 * Fully controlled: the parent owns the state (usually the URL search params)
 * and gets the next state back through onChange. The Key/渠道 and 分组 choices
 * are passed in as props because each view loads its own option list; which
 * filter field the Key/渠道 select edits is decided by `subjectField`.
 *
 * Picking a named 时间范围 replaces start/end with that window; 「自定义」keeps
 * whatever dates the user picked, with 结束日期 counted through end of day.
 */

import { copy } from '@/copy'
import { Button, Input, Segmented, Select, type SelectOption } from '@/ui'

import {
  presetToRange,
  type DatePreset,
  type RecordDatePreset,
  type RecordsFilterState,
} from './filters'

const filtersCopy = copy.pages.records.filters

/** 'custom' has no fixed window, so it never recomputes start/end. */
function presetRange(preset: RecordDatePreset): Pick<RecordsFilterState, 'start' | 'end'> | null {
  if (preset === 'custom') return null
  const [start, end] = presetToRange(preset as DatePreset)
  return { start, end }
}

/** yyyy-mm-dd in local time; what <input type="date"> both emits and accepts. */
function toDateInput(unixSeconds: number | undefined): string {
  if (unixSeconds === undefined || !Number.isFinite(unixSeconds) || unixSeconds <= 0) return ''
  const date = new Date(unixSeconds * 1000)
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

/** Local midnight of the typed date in unix seconds; '' is undefined. */
function fromDateInput(text: string, endOfDay = false): number | undefined {
  if (!text) return undefined
  const midnight = new Date(`${text}T00:00:00`).getTime()
  if (!Number.isFinite(midnight)) return undefined
  return Math.floor((midnight + (endOfDay ? 86_399_000 : 0)) / 1000)
}

/** Which filter field the Key (买方) or 渠道 (卖方) select edits. */
export type RecordsSubjectField = 'token_id' | 'listing_id'

export interface RecordsFilterBarProps {
  /** Current filter state, including the active time-range preset. */
  value: RecordsFilterState
  /** Called with the next state on any control change. */
  onChange: (next: RecordsFilterState) => void
  /** Which filter field the Key/渠道 select edits. */
  subjectField?: RecordsSubjectField
  /** Key (买方) or 渠道 (卖方) select options, passed in by each view. */
  subjectOptions?: SelectOption[]
  /** Label of the Key/渠道 select; defaults to the 买方 wording. */
  subjectLabel?: string
  /** 「全部」option of the Key/渠道 select. */
  subjectAllLabel?: string
  /** 分组 select options; empty hides the 分组 control. */
  groupOptions?: SelectOption[]
  /** Refresh button busy state while the queries are refetching. */
  refreshing?: boolean
  onRefresh?: () => void
  className?: string
}

export function RecordsFilterBar({
  value,
  onChange,
  subjectField = 'token_id',
  subjectOptions,
  subjectLabel = filtersCopy.subjectToken,
  subjectAllLabel = filtersCopy.subjectTokenAll,
  groupOptions,
  refreshing = false,
  onRefresh,
  className,
}: RecordsFilterBarProps) {
  function patch(next: Partial<RecordsFilterState>) {
    onChange({ ...value, ...next })
  }

  function selectPreset(preset: RecordDatePreset) {
    const range = presetRange(preset)
    patch({ preset, ...(range ?? { start: undefined, end: undefined }) })
  }

  function selectSubject(text: string) {
    const id = Number(text)
    const numeric = Number.isInteger(id) && id > 0 ? id : undefined
    // Only one of the two id filters belongs to a view; clear the other so a
    // stale value never leaks into buildRecordQuery when the props flip views.
    const otherField: RecordsSubjectField = subjectField === 'token_id' ? 'listing_id' : 'token_id'
    patch({ [subjectField]: numeric, [otherField]: undefined })
  }

  function selectStream(text: string) {
    patch({ stream: text === '' ? undefined : text === 'stream' })
  }

  function selectStatus(text: string) {
    patch({ status: text || undefined })
  }

  const typeOptions = [
    { value: '', label: filtersCopy.typeAll },
    { value: 'stream', label: filtersCopy.typeStream },
    { value: 'nonstream', label: filtersCopy.typeNonStream },
  ]
  const statusOptions = [
    { value: '', label: filtersCopy.statusAll },
    { value: 'success', label: filtersCopy.statusSuccess },
    { value: 'failed', label: filtersCopy.statusFailed },
  ]
  const subjectValue = value[subjectField] !== undefined ? String(value[subjectField]) : ''

  return (
    <div className={className} aria-label={filtersCopy.ariaLabel}>
      <div className="flex flex-wrap items-end gap-x-5 gap-y-4">
        <div>
          <span className="mb-2 block text-body font-medium text-ink">{filtersCopy.range}</span>
          <Segmented
            ariaLabel={filtersCopy.range}
            value={value.preset}
            onChange={(next) => selectPreset(next as RecordDatePreset)}
            options={[
              { value: 'today', label: filtersCopy.rangeToday },
              { value: '7d', label: filtersCopy.range7d },
              { value: '30d', label: filtersCopy.range30d },
              { value: 'custom', label: filtersCopy.rangeCustom },
            ]}
          />
        </div>

        {value.preset === 'custom' ? (
          <div className="flex items-end gap-3">
            <label className="block">
              <span className="mb-2 block text-body font-medium text-ink">
                {filtersCopy.customStart}
              </span>
              <Input
                type="date"
                className="w-[160px]"
                value={toDateInput(value.start)}
                onChange={(event) => patch({ start: fromDateInput(event.target.value) })}
              />
            </label>
            <label className="block">
              <span className="mb-2 block text-body font-medium text-ink">
                {filtersCopy.customEnd}
              </span>
              <Input
                type="date"
                className="w-[160px]"
                value={toDateInput(value.end)}
                onChange={(event) =>
                  patch({ end: fromDateInput(event.target.value, /* endOfDay */ true) })
                }
              />
            </label>
          </div>
        ) : null}

        <Select
          label={subjectLabel}
          className="w-[180px]"
          value={subjectValue}
          onChange={(event) => selectSubject(event.target.value)}
          options={
            subjectOptions?.length
              ? [{ value: '', label: subjectAllLabel }, ...subjectOptions]
              : [{ value: '', label: subjectAllLabel, disabled: true }]
          }
        />

        <label className="block">
          <span className="mb-2 block text-body font-medium text-ink">{filtersCopy.model}</span>
          <Input
            className="w-[200px]"
            placeholder={filtersCopy.modelPlaceholder}
            value={value.model ?? ''}
            onChange={(event) => patch({ model: event.target.value || undefined })}
          />
        </label>

        {groupOptions && groupOptions.length > 0 ? (
          <Select
            label={filtersCopy.group}
            className="w-[160px]"
            value={value.group ?? ''}
            onChange={(event) => patch({ group: event.target.value || undefined })}
            options={[{ value: '', label: filtersCopy.groupAll }, ...groupOptions]}
          />
        ) : null}

        <Select
          label={filtersCopy.type}
          className="w-[140px]"
          value={value.stream === undefined ? '' : value.stream ? 'stream' : 'nonstream'}
          onChange={(event) => selectStream(event.target.value)}
          options={typeOptions}
        />

        <Select
          label={filtersCopy.status}
          className="w-[140px]"
          value={value.status ?? ''}
          onChange={(event) => selectStatus(event.target.value)}
          options={statusOptions}
        />

        <Button
          variant="secondary"
          size="lg"
          className="px-4"
          disabled={!onRefresh}
          aria-busy={refreshing || undefined}
          onClick={onRefresh}
        >
          {filtersCopy.refresh}
        </Button>
      </div>
    </div>
  )
}
