import type { UseFormReturn } from 'react-hook-form'

import { copy } from '@/copy'
import { Input, SellerId } from '@/ui'

import { parseSellerIds } from '../format'
import type { PrefFormValues } from '../schemas'
import { STRATEGIES } from '../types'
import { Field, Select } from './form'

const page = copy.pages.consoleKeyDetail

/**
 * The routing/limits fields shared by the create dialog and the key detail
 * page. Keeping one component means a new option (e.g. a future strategy)
 * cannot be added to only one of the two screens.
 */
export function KeyRoutingFields({
  form,
  idPrefix = 'key',
  showScope = true,
}: {
  form: UseFormReturn<PrefFormValues>
  idPrefix?: string
  showScope?: boolean
}) {
  const preferIds = parseSellerIds(form.watch('prefer')).ids
  const blockIds = parseSellerIds(form.watch('block')).ids
  const onlyIds = parseSellerIds(form.watch('only')).ids

  return (
    <>
      <Field
        label={page.config.strategy}
        htmlFor={`${idPrefix}-strategy`}
        error={form.formState.errors.strategy?.message}
      >
        <Select id={`${idPrefix}-strategy`} {...form.register('strategy')}>
          {STRATEGIES.map((strategy) => (
            <option key={strategy} value={strategy}>
              {copy.pages.consoleKeys.strategy[strategy]}
            </option>
          ))}
        </Select>
      </Field>

      {showScope ? (
        <Field
          label={copy.pages.consoleKeys.createDialog.scope.label}
          htmlFor={`${idPrefix}-scope`}
          hint={copy.pages.consoleKeys.createDialog.scope.hint}
        >
          <Select id={`${idPrefix}-scope`} {...form.register('modelScope')}>
            <option value="all">{copy.pages.consoleKeys.createDialog.scope.all}</option>
            <option value="selected">{copy.pages.consoleKeys.createDialog.scope.selected}</option>
          </Select>
        </Field>
      ) : null}

      <Field
        label={page.config.priceCap}
        htmlFor={`${idPrefix}-price-cap`}
        hint={page.config.priceCapHint}
        error={form.formState.errors.priceCap?.message}
      >
        <Input
          id={`${idPrefix}-price-cap`}
          inputMode="decimal"
          placeholder="0.85"
          {...form.register('priceCap')}
        />
      </Field>

      <Field
        label={page.config.rpm}
        htmlFor={`${idPrefix}-rpm`}
        hint={page.config.rpmHint}
        error={form.formState.errors.rpm?.message}
      >
        <Input
          id={`${idPrefix}-rpm`}
          inputMode="numeric"
          placeholder="60"
          {...form.register('rpm')}
        />
      </Field>

      <Field
        label={page.config.prefer}
        htmlFor={`${idPrefix}-prefer`}
        hint={page.config.preferHint}
        error={form.formState.errors.prefer?.message}
      >
        <Input
          id={`${idPrefix}-prefer`}
          placeholder={page.sellerIds.placeholder}
          {...form.register('prefer')}
        />
        <SellerChips
          ids={preferIds}
          onRemove={(sellerId) =>
            form.setValue(
              'prefer',
              preferIds.filter((value) => value !== sellerId).join(', '),
              { shouldDirty: true },
            )
          }
        />
      </Field>

      <Field
        label={page.config.block}
        htmlFor={`${idPrefix}-block`}
        hint={page.config.blockHint}
        error={form.formState.errors.block?.message}
      >
        <Input
          id={`${idPrefix}-block`}
          placeholder={page.sellerIds.placeholder}
          {...form.register('block')}
        />
        <SellerChips
          ids={blockIds}
          onRemove={(sellerId) =>
            form.setValue(
              'block',
              blockIds.filter((value) => value !== sellerId).join(', '),
              { shouldDirty: true },
            )
          }
        />
      </Field>

      <Field
        label={page.config.only}
        htmlFor={`${idPrefix}-only`}
        hint={page.config.onlyHint}
        error={form.formState.errors.only?.message}
      >
        <Input
          id={`${idPrefix}-only`}
          inputMode="numeric"
          placeholder="10023"
          {...form.register('only')}
        />
        {onlyIds.length === 1 ? (
          <div className="mt-1.5">
            <SellerId id={onlyIds[0]} />
          </div>
        ) : null}
      </Field>

      <div className="space-y-3 sm:col-span-2">
        <label className="inline-flex items-start gap-2 text-[13px] text-ink">
          <input
            type="checkbox"
            className="mt-0.5 h-3.5 w-3.5 accent-brand"
            {...form.register('allowFallback')}
          />
          <span>
            {page.config.allowFallback}
            <span className="mt-0.5 block text-[13px] leading-5 text-ink-3">
              {page.config.allowFallbackHint}
            </span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-[13px] text-ink">
          <input
            type="checkbox"
            className="mt-0.5 h-3.5 w-3.5 accent-brand"
            {...form.register('officialOnly')}
          />
          <span>
            {page.config.officialOnly}
            <span className="mt-0.5 block text-[13px] leading-5 text-ink-3">
              {page.config.officialOnlyHint}
            </span>
          </span>
        </label>
      </div>
    </>
  )
}

function SellerChips({ ids, onRemove }: { ids: number[]; onRemove: (id: number) => void }) {
  if (ids.length === 0) return null
  return (
    <div className="mt-2 flex flex-wrap gap-1.5">
      {ids.map((id) => (
        <button
          key={id}
          type="button"
          onClick={() => onRemove(id)}
          className="rounded-sm border border-line bg-bg-soft px-2 py-0.5 text-[13px] text-ink-2 hover:border-line-strong"
        >
          #{id} ×
        </button>
      ))}
    </div>
  )
}
