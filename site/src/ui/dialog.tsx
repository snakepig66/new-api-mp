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
 * A restrained modal: white card, 12px radius, one hairline, no shadow.
 * Radix supplies focus trapping and escape handling.
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
        <DialogPrimitive.Overlay className="dialog-overlay fixed inset-0 z-40 bg-[rgba(30,45,110,0.28)] backdrop-blur-[3px]" />
        <DialogPrimitive.Content
          className={cn(
            'dialog-content fixed left-1/2 top-1/2 z-50 w-[calc(100vw-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2',
            'glass-strong rounded-lg p-6',
            className,
          )}
        >
          <div className="pr-8">
            <DialogPrimitive.Title className="text-[18px] font-semibold leading-7 tracking-tight text-ink">
              {title}
            </DialogPrimitive.Title>
            <DialogPrimitive.Description
              className={cn('mt-1 text-[14px] leading-6', description ? 'text-ink-3' : 'sr-only')}
            >
              {description ?? title}
            </DialogPrimitive.Description>
          </div>
          {children ? <div className="mt-5">{children}</div> : null}
          {footer ? (
            <div className="mt-6 flex flex-wrap justify-end gap-2 border-t border-line pt-5">
              {footer}
            </div>
          ) : null}
          <DialogPrimitive.Close
            className="absolute right-3 top-3 grid h-8 w-8 place-items-center rounded-full text-ink-3 transition-colors hover:bg-white/70 hover:text-ink"
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
