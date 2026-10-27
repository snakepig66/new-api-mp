import * as DialogPrimitive from '@radix-ui/react-dialog'
import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export type DrawerWidth = 'sm' | 'md' | 'lg'

const widthClass: Record<DrawerWidth, string> = {
  /* Full-bleed on phones; a fixed column once the viewport can hold it. */
  sm: 'sm:max-w-sm',
  md: 'sm:max-w-md',
  lg: 'sm:max-w-xl',
}

export interface DrawerProps {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  /** Optional trigger element; rendered as the drawer trigger. */
  trigger?: ReactNode
  title: string
  description?: string
  children?: ReactNode
  /**
   * Fixed footer. Render a SaveBar here: the bar draws its own top hairline,
   * so this slot adds no border of its own.
   */
  footer?: ReactNode
  /** Panel width from the `sm` breakpoint up. Defaults to `md`. */
  width?: DrawerWidth
  className?: string
  bodyClassName?: string
}

/**
 * Right-side panel in the same restrained language as Dialog: white surface,
 * one hairline on the left, no shadow. Radix supplies focus trapping, Escape,
 * outside-click dismissal, body scroll lock and the dialog/aria-modal roles.
 */
export function Drawer({
  open,
  onOpenChange,
  trigger,
  title,
  description,
  children,
  footer,
  width = 'md',
  className,
  bodyClassName,
}: DrawerProps) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      {trigger ? <DialogPrimitive.Trigger asChild>{trigger}</DialogPrimitive.Trigger> : null}
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="dialog-overlay fixed inset-0 z-40 bg-black/40" />
        <DialogPrimitive.Content
          className={cn(
            'fixed inset-y-0 right-0 z-50 flex w-full flex-col border-l border-line bg-bg outline-none',
            widthClass[width],
            className,
          )}
        >
          <div className="flex shrink-0 items-start justify-between gap-4 border-b border-line px-6 py-5">
            <div className="min-w-0">
              <DialogPrimitive.Title className="text-[18px] font-semibold leading-7 tracking-tight text-ink">
                {title}
              </DialogPrimitive.Title>
              <DialogPrimitive.Description
                className={cn(
                  'mt-1 text-[14px] leading-6',
                  description ? 'text-ink-3' : 'sr-only',
                )}
              >
                {description ?? title}
              </DialogPrimitive.Description>
            </div>
            <DialogPrimitive.Close
              className="-mr-2 grid h-8 w-8 shrink-0 place-items-center rounded-full text-ink-3 transition-colors hover:bg-bg-soft hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
              aria-label={copy.actions.close}
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
                <path
                  d="M2 2l10 10M12 2L2 12"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                />
              </svg>
            </DialogPrimitive.Close>
          </div>

          <div
            className={cn(
              'min-h-0 flex-1 overflow-y-auto px-6 py-5',
              bodyClassName,
            )}
          >
            {children}
          </div>

          {footer ? <div className="shrink-0">{footer}</div> : null}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

export const DrawerClose = DialogPrimitive.Close
