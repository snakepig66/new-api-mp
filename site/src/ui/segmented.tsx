import {
  forwardRef,
  useRef,
  type KeyboardEvent,
  type ReactNode,
} from 'react'

import { cn } from '@/lib/cn'

export interface SegmentedOption {
  value: string
  label: ReactNode
  disabled?: boolean
}

export type SegmentedSize = 'sm' | 'md'

export interface SegmentedProps {
  options: SegmentedOption[]
  value: string
  onChange: (value: string) => void
  /** `sm` is the compact table-toolbar size. */
  size?: SegmentedSize
  disabled?: boolean
  /** Accessible name for the group. */
  ariaLabel?: string
  className?: string
}

/**
 * Segmented control as a radiogroup, not a tablist: it picks one value for a
 * form/filter (radio semantics: checked state, one selection) rather than
 * switching between content panels (tab semantics own panels and arrow
 * activation). Arrow keys move the selection, like a native radio group.
 */
export const Segmented = forwardRef<HTMLDivElement, SegmentedProps>(function Segmented(
  {
    options,
    value,
    onChange,
    size = 'md',
    disabled = false,
    ariaLabel = '选项',
    className,
  },
  ref,
) {
  const buttonRefs = useRef<Array<HTMLButtonElement | null>>([])

  function enabledIndexes(): number[] {
    return options.flatMap((option, index) => (option.disabled ? [] : [index]))
  }

  function step(from: number, delta: number): number | null {
    const enabled = enabledIndexes()
    if (enabled.length === 0) return null
    const position = enabled.indexOf(from)
    if (position < 0) return enabled[0] ?? null
    return enabled[(position + delta + enabled.length) % enabled.length] ?? null
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (disabled) return

    const current = options.findIndex((option) => option.value === value)
    let next: number | null = null

    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
      next = step(current, 1)
    } else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
      next = step(current, -1)
    } else if (event.key === 'Home') {
      next = enabledIndexes()[0] ?? null
    } else if (event.key === 'End') {
      const enabled = enabledIndexes()
      next = enabled[enabled.length - 1] ?? null
    } else {
      return
    }

    if (next === null) return
    event.preventDefault()

    const option = options[next]
    if (option && next !== current) onChange(option.value)
    buttonRefs.current[next]?.focus()
  }

  return (
    <div
      ref={ref}
      role="radiogroup"
      aria-label={ariaLabel}
      aria-disabled={disabled || undefined}
      onKeyDown={handleKeyDown}
      className={cn(
        'inline-flex items-center gap-0.5 rounded-full border border-white/70 bg-white/45 p-0.5 shadow-[inset_0_1px_2px_rgba(77,107,254,0.08)] backdrop-blur-md',
        disabled && 'opacity-50',
        className,
      )}
    >
      {options.map((option, index) => {
        const selected = option.value === value
        const locked = disabled || option.disabled

        return (
          <button
            key={option.value}
            ref={(node) => {
              buttonRefs.current[index] = node
            }}
            type="button"
            role="radio"
            aria-checked={selected}
            disabled={locked}
            tabIndex={selected ? 0 : -1}
            onClick={() => {
              if (locked || selected) return
              onChange(option.value)
            }}
            className={cn(
              'rounded-full border font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand',
              size === 'sm' ? 'h-7 px-2.5 text-caption' : 'h-8 px-3 text-meta',
              selected
                ? 'border-white/80 bg-white/90 text-brand-ink shadow-[0_2px_8px_rgba(77,107,254,0.14)]'
                : 'border-transparent text-ink-3 hover:text-ink',
              locked && 'cursor-not-allowed',
            )}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
})
