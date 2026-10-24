import * as DialogPrimitive from '@radix-ui/react-dialog'
import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export interface DialogProps {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  /** Optional trigger element; rendered as the dialog trigger. */
  trigger?: ReactNode
  title: string
  description?: string
  children?: ReactNode
  footer?: ReactNode
  className?: string
}

/**
 * A restrained modal: one hairline frame, no shadow theatre, actions on a
 * footer rule. Radix supplies focus trapping and escape handling.
 */
export function Dialog({
  open,
  onOpenChange,
  trigger,
  title,
  description,
  children,
  footer,
  className,
}: DialogProps) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      {trigger ? <DialogPrimitive.Trigger asChild>{trigger}</DialogPrimitive.Trigger> : null}
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="dialog-overlay fixed inset-0 z-40 bg-ink/25 backdrop-blur-[1px]" />
        <DialogPrimitive.Content
          className={cn(
            'dialog-content fixed left-1/2 top-1/2 z-50 w-[calc(100vw-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2',
            'rounded-[6px] border border-line bg-surface p-5 shadow-[0_16px_48px_rgba(16,32,29,0.18)]',
            className,
          )}
        >
          <div className="pr-8">
            <DialogPrimitive.Title className="text-[16px] font-semibold tracking-tight text-ink">
              {title}
            </DialogPrimitive.Title>
            <DialogPrimitive.Description
              className={cn('mt-1 text-[13px] leading-6', description ? 'text-muted' : 'sr-only')}
            >
              {description ?? title}
            </DialogPrimitive.Description>
          </div>
          {children ? <div className="mt-4">{children}</div> : null}
          {footer ? (
            <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-line pt-4">
              {footer}
            </div>
          ) : null}
          <DialogPrimitive.Close
            className="absolute right-3 top-3 grid h-7 w-7 place-items-center rounded-[4px] text-muted transition-colors hover:bg-wash hover:text-ink"
            aria-label={copy.actions.close}
          >
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
              <path d="M2 2l10 10M12 2L2 12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

export const DialogClose = DialogPrimitive.Close
