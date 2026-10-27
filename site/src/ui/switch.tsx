import {
  forwardRef,
  useId,
  useState,
  type ButtonHTMLAttributes,
  type MouseEvent,
  type ReactNode,
} from 'react'

import { cn } from '@/lib/cn'

export interface SwitchProps
  extends Omit<
    ButtonHTMLAttributes<HTMLButtonElement>,
    'children' | 'onChange' | 'role' | 'type'
  > {
  checked?: boolean
  defaultChecked?: boolean
  onCheckedChange?: (checked: boolean) => void
  loading?: boolean
  label?: ReactNode
  description?: ReactNode
  /** Hidden field name so the switch can take part in a form. */
  name?: string
  /** Classes for the wrapper element. */
  className?: string
  /** Classes for the switch track itself. */
  switchClassName?: string
}

/**
 * Accessible switch: a real button with role="switch", so Space and Enter work
 * natively. The label is associated through htmlFor, keeping the control a
 * single tab stop.
 */
export const Switch = forwardRef<HTMLButtonElement, SwitchProps>(function Switch(
  {
    checked,
    defaultChecked = false,
    onCheckedChange,
    disabled = false,
    loading = false,
    label,
    description,
    name,
    className,
    switchClassName,
    onClick,
    id,
    ...props
  },
  ref,
) {
  const [internal, setInternal] = useState(defaultChecked)
  const generatedId = useId()
  const controlId = id ?? generatedId
  const descriptionId = useId()
  const isControlled = checked !== undefined
  const value = isControlled ? checked : internal
  const locked = disabled || loading

  function handleClick(event: MouseEvent<HTMLButtonElement>) {
    onClick?.(event)
    if (event.defaultPrevented || locked) return
    const next = !value
    if (!isControlled) setInternal(next)
    onCheckedChange?.(next)
  }

  return (
    <div className={cn('inline-flex items-start gap-3', className)}>
      <button
        {...props}
        ref={ref}
        id={controlId}
        type="button"
        role="switch"
        aria-checked={value}
        aria-describedby={description ? descriptionId : undefined}
        aria-busy={loading || undefined}
        disabled={locked}
        onClick={handleClick}
        className={cn(
          'relative inline-flex h-6 w-10 shrink-0 items-center rounded-full border transition-colors',
          'focus-visible:border-brand focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-brand-soft',
          value ? 'border-brand bg-brand' : 'border-line-strong bg-bg-soft',
          locked && 'cursor-not-allowed opacity-50',
          switchClassName,
        )}
      >
        <span
          className={cn(
            'pointer-events-none grid h-4 w-4 place-items-center rounded-full bg-white transition-transform',
            value ? 'translate-x-[19px]' : 'translate-x-[3px]',
          )}
        >
          {loading ? (
            <svg
              className="h-3 w-3 animate-spin text-ink-3"
              viewBox="0 0 16 16"
              fill="none"
              aria-hidden="true"
            >
              <circle
                cx="8"
                cy="8"
                r="6"
                stroke="currentColor"
                strokeOpacity="0.25"
                strokeWidth="2"
              />
              <path
                d="M14 8a6 6 0 0 0-6-6"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          ) : null}
        </span>
      </button>

      {label || description ? (
        <span className="min-w-0">
          {label ? (
            <label
              htmlFor={controlId}
              className={cn(
                'block text-[14px] font-medium leading-5 text-ink',
                disabled || loading ? 'cursor-not-allowed' : 'cursor-pointer',
              )}
            >
              {label}
            </label>
          ) : null}
          {description ? (
            <span id={descriptionId} className="mt-0.5 block text-[13px] leading-5 text-ink-3">
              {description}
            </span>
          ) : null}
        </span>
      ) : null}

      {name ? <input type="hidden" name={name} value={value ? 'on' : 'off'} /> : null}
    </div>
  )
})
