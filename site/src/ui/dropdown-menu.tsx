import {
  cloneElement,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
} from 'react'

import { cn } from '@/lib/cn'

export interface DropdownMenuItem {
  /** Falls back to the array index; pass a stable key for dynamic menus. */
  key?: string
  label: ReactNode
  onSelect?: () => void
  icon?: ReactNode
  danger?: boolean
  disabled?: boolean
}

export interface DropdownMenuProps {
  /** The trigger must be a single element; it receives the menu aria props. */
  // React's own default for ReactElement is `unknown` here; `any` lets callers
  // pass any button-like element without re-typing its props.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  trigger: ReactElement<any>
  items: DropdownMenuItem[]
  align?: 'start' | 'end'
  /** Accessible name for the menu. */
  menuLabel?: string
  disabled?: boolean
  className?: string
  menuClassName?: string
}

/**
 * Lightweight menu: no extra dependency. Closes on outside pointer-down and
 * Escape, returns focus to the trigger, and supports Up/Down/Home/End.
 */
export function DropdownMenu({
  trigger,
  items,
  align = 'start',
  menuLabel = '菜单',
  disabled = false,
  className,
  menuClassName,
}: DropdownMenuProps) {
  const [open, setOpen] = useState(false)
  const wrapperRef = useRef<HTMLSpanElement | null>(null)
  const menuRef = useRef<HTMLDivElement | null>(null)
  const menuId = useId()

  function close(returnFocus = true) {
    setOpen(false)
    if (!returnFocus) return
    window.requestAnimationFrame(() => {
      wrapperRef.current
        ?.querySelector<HTMLElement>('[data-dropdown-trigger]')
        ?.focus()
    })
  }

  useEffect(() => {
    if (!open) return

    function handlePointerDown(event: PointerEvent) {
      if (!wrapperRef.current?.contains(event.target as Node)) setOpen(false)
    }

    function handleDocumentKeyDown(event: globalThis.KeyboardEvent) {
      if (event.key === 'Escape') {
        event.stopPropagation()
        close()
      } else if (event.key === 'Tab') {
        setOpen(false)
      }
    }

    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleDocumentKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleDocumentKeyDown)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const frame = window.requestAnimationFrame(() => {
      focusItem(0)
    })
    return () => window.cancelAnimationFrame(frame)
  }, [open])

  function focusItem(index: number) {
    const nodes = menuRef.current?.querySelectorAll<HTMLElement>(
      '[role="menuitem"]:not([disabled])',
    )
    nodes?.[index]?.focus()
  }

  function handleMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const nodes = Array.from(
      menuRef.current?.querySelectorAll<HTMLElement>(
        '[role="menuitem"]:not([disabled])',
      ) ?? [],
    )
    if (nodes.length === 0) return

    const current = nodes.indexOf(document.activeElement as HTMLElement)
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      nodes[current < 0 ? 0 : (current + 1) % nodes.length]?.focus()
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      nodes[current < 0 ? nodes.length - 1 : (current - 1 + nodes.length) % nodes.length]?.focus()
    } else if (event.key === 'Home') {
      event.preventDefault()
      nodes[0]?.focus()
    } else if (event.key === 'End') {
      event.preventDefault()
      nodes[nodes.length - 1]?.focus()
    } else if (event.key === 'Escape') {
      event.preventDefault()
      // Keep Escape from also closing a surrounding Drawer/Dialog.
      event.stopPropagation()
      close()
    } else if (event.key === 'Tab') {
      setOpen(false)
    }
  }

  const originalOnClick = (
    trigger.props as { onClick?: (event: MouseEvent<HTMLElement>) => void }
  ).onClick

  const triggerNode = cloneElement(trigger, {
    'data-dropdown-trigger': '',
    'aria-haspopup': 'menu',
    'aria-expanded': open,
    'aria-controls': open ? menuId : undefined,
    onClick: (event: MouseEvent<HTMLElement>) => {
      originalOnClick?.(event)
      if (event.defaultPrevented || disabled) return
      setOpen((current) => !current)
    },
  })

  return (
    <span ref={wrapperRef} className={cn('relative inline-flex', className)}>
      {triggerNode}
      {open ? (
        <div
          ref={menuRef}
          id={menuId}
          role="menu"
          aria-label={menuLabel}
          onKeyDown={handleMenuKeyDown}
          className={cn(
            'absolute top-full z-50 mt-1 min-w-[180px] rounded-md border border-line bg-bg p-1',
            align === 'end' ? 'right-0' : 'left-0',
            menuClassName,
          )}
        >
          {items.map((item, index) => (
            <button
              key={item.key ?? index}
              type="button"
              role="menuitem"
              tabIndex={-1}
              disabled={item.disabled}
              onClick={() => {
                if (item.disabled) return
                item.onSelect?.()
                close()
              }}
              className={cn(
                'flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-[13px] leading-5 transition-colors focus:outline-none',
                item.danger
                  ? 'text-danger hover:bg-danger/5 focus:bg-danger/5'
                  : 'text-ink hover:bg-bg-soft focus:bg-bg-soft',
                item.disabled &&
                  'cursor-not-allowed opacity-50 hover:bg-transparent focus:bg-transparent',
              )}
            >
              {item.icon ? <span className="shrink-0">{item.icon}</span> : null}
              <span className="min-w-0 flex-1 truncate">{item.label}</span>
            </button>
          ))}
        </div>
      ) : null}
    </span>
  )
}
