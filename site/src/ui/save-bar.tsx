import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

import { Button } from './button'

/* TODO: move these labels into copy.ts when the shared copy file is touched. */
const labels = {
  save: '保存',
  saving: '正在保存',
  cancel: '取消',
  dirty: '有未保存的更改',
}

export interface SaveBarProps {
  /** Whether there are unsaved changes. Save is disabled when false. */
  dirty?: boolean
  saving?: boolean
  onSave?: () => void
  onCancel?: () => void
  /** Replaces the built-in status line (dirty / saving). */
  hint?: ReactNode
  saveLabel?: string
  savingLabel?: string
  cancelLabel?: string
  saveDisabled?: boolean
  cancelDisabled?: boolean
  /** Pin to the viewport bottom for page-level forms. Drawers leave it off. */
  fixed?: boolean
  className?: string
}

/**
 * Bottom action bar for long forms. Drop it into Drawer's footer slot: the
 * drawer body scrolls while this bar stays put at the bottom of the panel.
 */
export function SaveBar({
  dirty = false,
  saving = false,
  onSave,
  onCancel,
  hint,
  saveLabel = labels.save,
  savingLabel = labels.saving,
  cancelLabel = labels.cancel,
  saveDisabled = false,
  cancelDisabled = false,
  fixed = false,
  className,
}: SaveBarProps) {
  const status = hint ?? (saving ? savingLabel : dirty ? labels.dirty : null)
  const canSave = dirty && !saving && !saveDisabled

  return (
    <div
      className={cn(
        'glass-bar flex flex-wrap items-center justify-between gap-3 border-t border-white/70 px-5 py-4',
        fixed && 'fixed inset-x-0 bottom-0 z-30',
        className,
      )}
    >
      <div
        aria-live="polite"
        className="flex min-w-0 flex-1 items-center gap-2 text-[13px] leading-5 text-ink-3"
      >
        {dirty && !saving ? (
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-brand" aria-hidden="true" />
        ) : null}
        {status ? <span className="min-w-0">{status}</span> : null}
      </div>

      <div className="flex shrink-0 items-center gap-2">
        {onCancel ? (
          <Button
            variant="secondary"
            size="sm"
            onClick={onCancel}
            disabled={cancelDisabled || saving}
          >
            {cancelLabel}
          </Button>
        ) : null}
        <Button
          size="sm"
          onClick={onSave}
          loading={saving}
          disabled={!canSave}
        >
          {saving ? savingLabel : saveLabel}
        </Button>
      </div>
    </div>
  )
}
